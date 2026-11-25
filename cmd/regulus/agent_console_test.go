package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/console"
	"github.com/CodexCircle/Regulus/internal/localstatus"
)

// On a server whose role is agent, `regulus status` and `regulus` show the
// agent view, never the hub's (#434).
func agentEnv(t *testing.T, role, unit string) (env, *bytes.Buffer, *bool) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(cfg, []byte("hub: hub.example:8443\ndata_dir: "+filepath.Join(dir, "data")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	opened := new(bool)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	e := env{
		getenv: func(k string) string {
			if k == "REGULUS_CONFIG" {
				return cfg
			}
			return ""
		},
		stdout:   out,
		stderr:   &bytes.Buffer{},
		readFile: func(string) ([]byte, error) { return []byte(role), nil },
		agentSystem: console.AgentSystem{
			Getenv:       func(string) string { return "" },
			Now:          func() time.Time { return now },
			Hostname:     func() (string, error) { return "web2", nil },
			ServiceState: func(context.Context) string { return unit },
			Logs: func(context.Context, int) ([]string, error) {
				return []string{`level=INFO msg="connected to the hub" agent=a1`}, nil
			},
		},
		runAgent: func(console.AgentOptions) error { *opened = true; return nil },
		run:      func(console.Options) error { t.Error("the hub console must not open on an agent server"); return nil },
	}
	return e, out, opened
}

func TestStatusOnAnAgentServer(t *testing.T) {
	e, out, _ := agentEnv(t, "agent\n", "active")
	// c.config is empty: the agent view takes the default file when it exists,
	// so pass the file by flag.
	cfg := e.getenv("REGULUS_CONFIG")
	code := consoleCommand([]string{"--plain", "--config", cfg}, e, false)
	if code != 0 {
		t.Fatalf("exit status %d, output %s", code, out)
	}
	for _, want := range []string{"role=agent", "service=active", "running=true", "link=connected", "hub=hub.example:8443"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output has no %q:\n%s", want, out)
		}
	}
	if strings.Contains(out.String(), "setup_token") || strings.Contains(out.String(), "agent_listen") {
		t.Errorf("the hub's items are shown on an agent server:\n%s", out)
	}
}

func TestStatusOnAnAgentServerWithTheUnitStopped(t *testing.T) {
	e, _, _ := agentEnv(t, "agent\n", "inactive")
	if code := consoleCommand([]string{"--plain", "--config", e.getenv("REGULUS_CONFIG")}, e, false); code != exitNotRunning {
		t.Errorf("exit status %d, want %d", code, exitNotRunning)
	}
}

func TestConsoleOnAnAgentServerOpensTheAgentScreen(t *testing.T) {
	e, _, opened := agentEnv(t, "agent\n", "active")
	e.terminal = terminalInfo{interactive: true, width: 120, height: 40}
	if code := consoleCommand([]string{"--config", e.getenv("REGULUS_CONFIG")}, e, true); code != 0 {
		t.Fatalf("exit status %d", code)
	}
	if !*opened {
		t.Error("the agent console was not opened")
	}
}

func TestAHubKeepsItsOwnView(t *testing.T) {
	e, out, _ := agentEnv(t, "", "active")
	e.system = console.System{
		Getenv:       e.getenv,
		Now:          time.Now,
		ServiceState: func(context.Context) string { return "inactive" },
		Fetch:        func(context.Context, string) (localstatus.Status, error) { return localstatus.Status{}, os.ErrNotExist },
		Addrs:        func() []string { return nil },
		Logs:         func(context.Context, int) ([]string, error) { return nil, nil },
	}
	consoleCommand([]string{"--plain"}, e, false)
	if strings.Contains(out.String(), "role=agent") {
		t.Errorf("a server without the agent role got the agent view:\n%s", out)
	}
}
