package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CodexCircle/Regulus/internal/plugin"
	"github.com/CodexCircle/Regulus/internal/plugin/sdk"
)

// The constants of the program are what plugin.yaml says: Regulus refuses a
// plugin that answers Describe with anything else (docs/plugins/writing-a-plugin.md).
func TestTheProgramMatchesItsManifest(t *testing.T) {
	f, err := os.Open("plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	m, err := plugin.ParseManifest(f)
	if err != nil {
		t.Fatalf("plugin.yaml: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("plugin.yaml: %v", err)
	}
	if m.ID != id || m.Version != version || m.API != api {
		t.Errorf("manifest says %s %s api %d, the program says %s %s api %d", m.ID, m.Version, m.API, id, version, api)
	}
	if filepath.Base(mustAbs(t, ".")) != m.ID {
		t.Errorf("the directory is not named after the id %q", m.ID)
	}
	if m.Components.UI == nil {
		t.Fatal("the manifest names no widget module")
	}
	if _, err := os.Stat(m.Components.UI.Module); err != nil {
		t.Errorf("the widget module of the manifest: %v", err)
	}
	for _, p := range m.Permissions {
		if !strings.HasPrefix(p.ID, m.ID+".") || p.Description == "" {
			t.Errorf("permission %+v: it must start with the plugin id and say what it allows", p)
		}
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestCountStartKeepsTheCountInTheDataDirectory(t *testing.T) {
	dir := t.TempDir()
	for want := 1; want <= 3; want++ {
		if n, err := countStart(dir); err != nil || n != want {
			t.Fatalf("start %d: got %d, %v", want, n, err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(dir, startsFile)); strings.TrimSpace(string(data)) != "3" {
		t.Errorf("file = %q", data)
	}
	if n, err := countStart(""); err != nil || n != 1 {
		t.Errorf("without a data directory: %d, %v", n, err)
	}
	// A damaged file starts the count again instead of failing.
	if err := os.WriteFile(filepath.Join(dir, startsFile), []byte("not a number"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := countStart(dir); err != nil || n != 1 {
		t.Errorf("damaged file: %d, %v", n, err)
	}
}

func TestHealthFollowsTheDataDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := writable(dir); err != nil {
		t.Fatalf("a writable directory is unhealthy: %v", err)
	}
	if err := writable(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing directory is healthy")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".health-*")); len(left) != 0 {
		t.Errorf("the health check left %v", left)
	}
}

// Run by hand, outside Regulus, the program says what is wrong and exits.
func TestRunWithoutRegulusExplains(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	bin := filepath.Join(t.TempDir(), "example-hello")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin)
	cmd.Env = []string{} // no REGULUS_PLUGIN_SOCKET
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), sdk.EnvSocket) {
		t.Errorf("output %q, error %v: want an error that names %s", out, err, sdk.EnvSocket)
	}
}
