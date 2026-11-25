package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const agentYAML = `# Regulus agent
hub: "192.168.1.20:8443"   # the hub
data_dir: /var/lib/regulus-agent
updates: true
`

func TestSetAgentHubKeepsTheRestOfTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte(agentYAML), 0o640); err != nil {
		t.Fatal(err)
	}
	for _, hub := range []string{"hub.lan:8443", "192.168.1.99:8443", "[fd00::1]:8443"} {
		if err := SetAgentHub(path, hub); err != nil {
			t.Fatalf("%s: %v", hub, err)
		}
		cfg, err := LoadAgent([]string{"--config", path}, func(string) string { return "" }, os.Stderr)
		if err != nil || cfg.Hub != hub || cfg.DataDir != "/var/lib/regulus-agent" || !cfg.Updates {
			t.Fatalf("%s: loaded %+v, %v", hub, cfg, err)
		}
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "# Regulus agent\n") || strings.Count(string(data), "hub:") != 1 {
		t.Errorf("the file changed beyond the hub line:\n%s", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
}

func TestSetAgentHubRefusesABadAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte(agentYAML), 0o640); err != nil {
		t.Fatal(err)
	}
	for _, hub := range []string{"", "hub.lan", ":8443", "hub.lan:0", "hub.lan:http", "hub.lan:70000"} {
		if err := SetAgentHub(path, hub); err == nil {
			t.Errorf("%q was accepted", hub)
		}
	}
	if data, _ := os.ReadFile(path); string(data) != agentYAML {
		t.Errorf("the file changed:\n%s", data)
	}
	if err := SetAgentHub(filepath.Join(t.TempDir(), "missing.yaml"), "hub.lan:8443"); err == nil {
		t.Error("a missing file was accepted")
	}
}
