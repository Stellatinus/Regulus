package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CodexCircle/Regulus/internal/agent"
	"github.com/CodexCircle/Regulus/internal/config"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"github.com/CodexCircle/Regulus/internal/logging"
	"github.com/CodexCircle/Regulus/internal/plugin"
	"github.com/CodexCircle/Regulus/internal/update"
)

// exitRejected is the exit status of an agent the hub does not accept (revoked
// or removed). The service unit does not restart on it: retrying cannot help.
const exitRejected = 3

// runAgent implements `regulus agent`: a remote agent that connects out to a
// hub. The first run enrolls it with the token; later runs only need the hub
// address and the saved credentials.
func runAgent(args []string) int {
	if len(args) > 0 && args[0] == "set-hub" {
		return runAgentSetHub(args[1:])
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return agentMain(ctx, args, os.Getenv, os.Stderr)
}

func agentMain(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) int {
	cfg, err := config.LoadAgent(args, getenv, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 2
	}
	log, err := logging.New(cfg.Log, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 2
	}

	creds, err := agent.LoadCredentials(cfg.DataDir)
	switch {
	case err == nil:
		if cfg.Token != "" {
			log.Info("already enrolled: the token is ignored", "agent", creds.AgentID)
		}
	case errors.Is(err, agent.ErrNotEnrolled):
		if cfg.Token == "" || cfg.CAFingerprint == "" {
			log.Error("this agent is not enrolled: give the token and the CA fingerprint shown by the dashboard (--token, --ca-fingerprint)")
			return 2
		}
		creds, err = agent.Enroll(ctx, cfg.Hub, cfg.CAFingerprint, cfg.Token, version, cfg.DataDir)
		if err != nil {
			log.Error("enrollment failed", "err", err)
			return 1
		}
		log.Info("enrolled", "agent", creds.AgentID, "hub", cfg.Hub)
	default:
		log.Error("cannot load the agent credentials", "dir", cfg.DataDir, "err", err)
		return 1
	}

	if cfg.EnrollOnly {
		return 0
	}

	conn, err := creds.Dial(cfg.Hub)
	if err != nil {
		log.Error("cannot connect", "err", err)
		return 1
	}
	defer func() { _ = conn.Close() }()

	log.Info("starting agent", "version", version, "agent", creds.AgentID, "hub", cfg.Hub)
	opts := agent.Options{Version: version, ID: creds.AgentID, Log: log}
	if cfg.Updates {
		opts.UpdateDir = cfg.DataDir
		opts.Verifier = &update.SigstoreVerifier{Trust: update.PublicTrustReporting(filepath.Join(cfg.DataDir, "tuf"), func(st update.TrustState) {
			if st.Embedded {
				log.Warn("the Sigstore trusted root could not be fetched: releases are verified with the root compiled into this binary, which does not know about keys revoked since", "reason", st.Reason)
			}
		})}
	}
	// Plugins run beside the agent, each in its own process: nothing they do
	// reaches agent.Run, and the agent stops them when it stops. Their state
	// goes to the hub with the metrics.
	plugins, stopPlugins := startPlugins(ctx, cfg, log)
	opts.Plugins = plugins
	err = agent.Run(ctx, conn, opts)
	stopPlugins()
	var rejected *agent.RejectedError
	if errors.As(err, &rejected) {
		log.Error("the hub does not accept this agent: it was revoked or removed. To use this server again, remove the agent data directory and enroll with a new token", "dir", cfg.DataDir, "err", err)
		return exitRejected
	}
	if err != nil {
		log.Error("agent stopped", "err", err)
		return 1
	}
	return 0
}

// startPlugins runs the plugins of cfg.PluginsDir, if set, until ctx ends.
// status reports their state for the hub (nil when plugins are off); stop
// stops them and waits until they are gone.
func startPlugins(ctx context.Context, cfg config.Agent, log *slog.Logger) (status func() []*agentv1.PluginStatus, stop func()) {
	if cfg.PluginsDir == "" {
		return nil, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	sup := &plugin.Supervisor{
		Dir:         cfg.PluginsDir,
		RunDir:      filepath.Join(cfg.DataDir, "plugins-run"),
		DataDir:     filepath.Join(cfg.DataDir, "plugins-data"),
		Role:        plugin.RoleAgent,
		CoreVersion: version,
		Log:         log.With("component", "plugins"),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := sup.Run(ctx); err != nil {
			log.Error("plugins are not running", "err", err)
		}
	}()
	return func() []*agentv1.PluginStatus { return pluginReport(sup.Status()) }, func() { cancel(); <-done }
}

// pluginReport turns the supervisor's view into the agent protocol's.
func pluginReport(sts []plugin.Status) []*agentv1.PluginStatus {
	out := make([]*agentv1.PluginStatus, 0, len(sts))
	for _, st := range sts {
		p := &agentv1.PluginStatus{Id: st.ID, Version: st.Version, State: st.State, Restarts: uint32(max(st.Restarts, 0)), LastError: st.LastError}
		if !st.Since.IsZero() {
			p.Since = timestamppb.New(st.Since)
		}
		out = append(out, p)
	}
	return out
}
