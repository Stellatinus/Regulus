package console

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The Settings row of agent_listen shows the effective value and where it
// comes from: the configuration file, the environment, or the dashboard, which
// keeps its address in the hub's database (#423).
func TestAgentListenSettingOrigins(t *testing.T) {
	withLine := func(t *testing.T) string {
		t.Helper()
		data, err := os.ReadFile("testdata/config.yaml")
		if err != nil {
			t.Fatal(err)
		}
		path := t.TempDir() + "/config.yaml"
		text := strings.Replace(string(data), "# agent_listen: \":8443\"", "agent_listen: \":9443\"", 1)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, tc := range []struct {
		name   string
		file   func(*testing.T) string
		env    string
		value  string
		origin string
	}{
		{"settings-agent-listen-dashboard", func(*testing.T) string { return "testdata/config.yaml" }, "", ":8443", "dashboard"},
		{"settings-agent-listen-file", withLine, "", ":9443", "file"},
		{"settings-agent-listen-env", func(*testing.T) string { return "testdata/config.yaml" }, ":7443", ":7443", "environment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := fakeSystem()
			env := tc.env
			sys.Getenv = func(k string) string {
				if k == "REGULUS_AGENT_LISTEN" {
					return env
				}
				return ""
			}
			snap := Collect(t.Context(), sys, tc.file(t), "v0.3.0")
			var found bool
			for _, e := range snap.Settings {
				if e.Key == "agent_listen" {
					found = true
					if e.Value != tc.value || e.Origin != tc.origin {
						t.Errorf("agent_listen = %q from %q, want %q from %q", e.Value, e.Origin, tc.value, tc.origin)
					}
				}
			}
			if !found {
				t.Fatal("no agent_listen setting")
			}
			m := New(Options{System: sys, ConfigFile: tc.file(t), Version: "v0.3.0", Unicode: true})
			next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 48})
			m = settle(t, next.(Model), next.(Model).collect())
			m = sendKey(t, m, rune1('2'), key(tea.KeyDown))
			golden(t, tc.name, trimLines(m.View()))
		})
	}
}
