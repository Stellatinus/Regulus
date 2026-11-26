package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/CodexCircle/Regulus/internal/store"
	"github.com/CodexCircle/Regulus/internal/update"
)

// runPluginApply is the root-owned half of the plugin store: systemd starts it
// (regulus-plugin-apply.service) when the hub leaves a request to install,
// update or remove a plugin. It verifies the signed index and the package
// again, unpacks into the plugins directory and switches the version.
func runPluginApply(args []string) int {
	fs := flag.NewFlagSet("plugin-apply", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "/var/lib/regulus", "the hub's data directory, where the request and the staged package are")
	stateDir := fs.String("state-dir", "/var/lib/regulus-update", "directory owned by root for the lock and the signature trust cache (shared with update-apply)")
	pluginsDir := fs.String("plugins-dir", "", "the plugins directory (the hub's plugins_dir); it must be owned by root")
	configFile := fs.String("config", "/etc/regulus/config.yaml", "the hub's configuration file, read only for plugin_sources (the signer of a custom source)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "regulus: plugin-apply writes files owned by root; systemd runs it as root")
		return 1
	}
	if *pluginsDir == "" {
		fmt.Fprintln(os.Stderr, "regulus: plugin-apply needs --plugins-dir")
		return 2
	}
	// Whatever the caller's umask, what is installed is readable by the hub.
	syscall.Umask(0o022)
	if err := os.MkdirAll(*stateDir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "regulus: %v\n", err)
		return 1
	}
	sources, err := store.LoadSources(*configFile, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "regulus: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	unlock, err := update.LockState(ctx, *stateDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "regulus: %v\n", err)
		return 1
	}
	defer unlock()

	ver := ""
	if _, ok := update.ParseBuildVersion(version); ok {
		ver = version
	}
	a := &store.Applier{
		DataDir:    *dataDir,
		StateDir:   *stateDir,
		PluginsDir: *pluginsDir,
		Sources:    sources,
		Verifier:   store.SigstoreVerifier{Trust: update.PublicTrust(filepath.Join(*stateDir, "tuf"))},
		Version:    ver,
		Machine:    store.Machine{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Log:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	res, err := a.Apply(ctx)
	switch {
	case errors.Is(err, store.ErrNoRequest):
		fmt.Fprintln(os.Stderr, "regulus: there is no plugin request")
		return 0
	case err != nil:
		fmt.Fprintf(os.Stderr, "regulus: %s %s: %v\n", res.Action, res.Plugin, err)
		return 1
	}
	fmt.Printf("regulus: %s %s %s done\n", res.Action, res.Plugin, res.Version)
	return 0
}
