package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/CodexCircle/Regulus/internal/agent"
	"github.com/CodexCircle/Regulus/internal/config"
)

// defaultAgentConfig is where the installer puts the configuration of an agent.
const defaultAgentConfig = "/etc/regulus/agent.yaml"

// agentUnit is the service of an agent.
const agentUnit = "regulus-agent.service"

// setHubEnv is what `regulus agent set-hub` needs from the machine; tests
// replace it.
type setHubEnv struct {
	getenv  func(string) string
	stdout  io.Writer
	stderr  io.Writer
	isRoot  func() bool
	restart func(ctx context.Context) error
}

func realSetHubEnv() setHubEnv {
	return setHubEnv{
		getenv: os.Getenv, stdout: os.Stdout, stderr: os.Stderr,
		isRoot: func() bool { return os.Geteuid() == 0 },
		restart: func(ctx context.Context) error {
			out, err := exec.CommandContext(ctx, "systemctl", "restart", agentUnit).CombinedOutput()
			if err != nil {
				return fmt.Errorf("%w: %s", err, out)
			}
			return nil
		},
	}
}

// runAgentSetHub implements `regulus agent set-hub HOST:PORT`: it points an
// enrolled agent at another address of its hub, for a hub whose address
// changed (a dynamic IP address). The agent trusts its hub by the fingerprint
// of the hub's CA, not by the address, so nothing is enrolled again: the new
// address must answer with that CA, which is checked before the configuration
// is written, and the agent is restarted.
func runAgentSetHub(args []string) int {
	return agentSetHub(context.Background(), args, realSetHubEnv())
}

func agentSetHub(ctx context.Context, args []string, env setHubEnv) int {
	fs := flag.NewFlagSet("agent set-hub", flag.ContinueOnError)
	fs.SetOutput(env.stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprint(env.stderr, `Usage: regulus agent set-hub [flags] HOST:PORT

Points this enrolled agent at another address of its hub, for when the hub's
address changed. It checks that the address answers with the hub's CA (the one
this agent was enrolled with), writes it as hub: in the configuration file and
restarts the agent. Use a name instead of an IP address if you can: a name
follows a change of address by itself.

`)
		fs.PrintDefaults()
	}
	path := fs.String("config", "", "agent configuration file (env REGULUS_CONFIG; default "+defaultAgentConfig+")")
	dataDir := fs.String("data-dir", "", "the agent's data directory, when not the one in the configuration file")
	noCheck := fs.Bool("no-check", false, "write the address without checking that the hub answers (for a hub that is switched off now)")
	noRestart := fs.Bool("no-restart", false, "do not restart the agent")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	addr := fs.Arg(0)

	file := *path
	if file == "" {
		file = env.getenv("REGULUS_CONFIG")
	}
	if file == "" {
		file = defaultAgentConfig
	}
	loadArgs := []string{"--config", file, "--hub", addr}
	if *dataDir != "" {
		loadArgs = append(loadArgs, "--data-dir", *dataDir)
	}
	cfg, err := config.LoadAgent(loadArgs, env.getenv, env.stderr)
	if err != nil {
		_, _ = fmt.Fprintf(env.stderr, "regulus: %v\n", err)
		return 2
	}
	creds, err := agent.LoadCredentials(cfg.DataDir)
	if err != nil {
		if errors.Is(err, agent.ErrNotEnrolled) {
			_, _ = fmt.Fprintf(env.stderr, "regulus: this agent is not enrolled (no credentials in %s): enroll it with the command of Servers → Add a server instead\n", cfg.DataDir)
		} else {
			_, _ = fmt.Fprintf(env.stderr, "regulus: cannot load the agent credentials from %s: %v\n", cfg.DataDir, err)
		}
		return 1
	}

	if !*noCheck {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := creds.CheckHub(cctx, addr)
		cancel()
		if err != nil {
			_, _ = fmt.Fprintf(env.stderr, "regulus: %s does not answer as this agent's hub: %v\nNothing was changed. Check the address, or use --no-check if the hub is switched off now.\n", addr, err)
			return 1
		}
		_, _ = fmt.Fprintf(env.stdout, "%s answers with this agent's hub CA.\n", addr)
	}

	if err := config.SetAgentHub(file, addr); err != nil {
		_, _ = fmt.Fprintf(env.stderr, "regulus: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(env.stdout, "Saved hub: %s in %s.\n", addr, file)

	switch {
	case *noRestart:
		_, _ = fmt.Fprintf(env.stdout, "Restart the agent to use it: sudo systemctl restart %s\n", agentUnit)
	case !env.isRoot():
		_, _ = fmt.Fprintf(env.stdout, "The agent reads it at start. Run: sudo systemctl restart %s\n", agentUnit)
	default:
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := env.restart(rctx)
		cancel()
		if err != nil {
			_, _ = fmt.Fprintf(env.stderr, "regulus: could not restart the agent: %v\nRun: systemctl restart %s\n", err, agentUnit)
			return 1
		}
		_, _ = fmt.Fprintln(env.stdout, "The agent was restarted.")
	}
	return 0
}
