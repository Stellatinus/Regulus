package plugin

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tabManifest = `id: disks
version: 0.1.0
name: Disks
api: 1
components:
  ui:
    module: ui/disks.js
    node_tab:
      id: disks
      title:
        en: Disks
        it: Dischi
`

func TestNodeTabInTheManifest(t *testing.T) {
	m, err := ParseManifest(strings.NewReader(tabManifest))
	if err != nil {
		t.Fatal(err)
	}
	tab := m.Components.UI.NodeTab
	if tab == nil || tab.ID != "disks" || tab.Title.TitleFor("it") != "Dischi" || tab.Title.TitleFor("en") != "Disks" || tab.Title.TitleFor("fr") != "Disks" {
		t.Errorf("tab = %+v", tab)
	}
	noIT := strings.Replace(tabManifest, "        it: Dischi\n", "", 1)
	m, err = ParseManifest(strings.NewReader(noIT))
	if err != nil || m.Components.UI.NodeTab.Title.TitleFor("it") != "Disks" {
		t.Errorf("Italian falls back to English: %v, %+v", err, m.Components.UI.NodeTab)
	}
	for name, bad := range map[string]string{
		"no english":  strings.Replace(tabManifest, "en: Disks", "en: \"\"", 1),
		"bad id":      strings.Replace(tabManifest, "id: disks\n      title", "id: Disks\n      title", 1),
		"long title":  strings.Replace(tabManifest, "en: Disks", "en: "+strings.Repeat("x", 25), 1),
		"two titles":  strings.Replace(tabManifest, "        it: Dischi\n", "        it: Dischi\n        fr: Disques\n", 1),
		"unknown key": strings.Replace(tabManifest, "      id: disks\n", "      id: disks\n      icon: x\n", 1),
	} {
		if _, err := ParseManifest(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func writeUIPlugin(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"plugin.yaml": tabManifest, "ui/disks.js": "export const x = 1"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadUIFollowsCurrentAndStaysInsideThePlugin(t *testing.T) {
	dir := t.TempDir()
	writeUIPlugin(t, filepath.Join(dir, "disks"))
	u, err := LoadUI(dir, "disks")
	if err != nil || u.Root != filepath.Join(dir, "disks") {
		t.Fatalf("plain layout: %v, %+v", err, u)
	}

	// Versions side by side: current names the one that runs.
	dir = t.TempDir()
	writeUIPlugin(t, filepath.Join(dir, "disks", "0.1.0"))
	if err := os.Symlink("0.1.0", filepath.Join(dir, "disks", "current")); err != nil {
		t.Fatal(err)
	}
	u, err = LoadUI(dir, "disks")
	if err != nil || u.Root != filepath.Join(dir, "disks", "current") {
		t.Fatalf("versioned layout: %v, %+v", err, u)
	}
	if code, err := u.ReadModule(); err != nil || string(code) != "export const x = 1" {
		t.Errorf("module = %q, %v", code, err)
	}

	for _, id := range []string{"", "..", "../disks", "a/b", "Disks", "disks/"} {
		if _, err := LoadUI(dir, id); !errors.Is(err, ErrNoPlugin) {
			t.Errorf("id %q: %v", id, err)
		}
	}
	if _, err := LoadUI("", "disks"); !errors.Is(err, ErrNoPlugin) {
		t.Errorf("no dir: %v", err)
	}
}

func TestReadModuleRefusesALinkOutOfThePlugin(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "disks")
	writeUIPlugin(t, root)
	secret := filepath.Join(t.TempDir(), "secret.js")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "ui", "disks.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "ui", "disks.js")); err != nil {
		t.Fatal(err)
	}
	u, err := LoadUI(dir, "disks")
	if err != nil {
		t.Fatal(err)
	}
	if code, err := u.ReadModule(); err == nil {
		t.Errorf("read %q through a link", code)
	}
}

func TestNoUIAndListUI(t *testing.T) {
	dir := t.TempDir()
	writeUIPlugin(t, filepath.Join(dir, "disks"))
	hubOnly := "id: svc\nversion: 0.1.0\nname: Svc\napi: 1\ncomponents:\n  hub:\n    command: bin/svc\n"
	if err := os.MkdirAll(filepath.Join(dir, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "svc", "plugin.yaml"), []byte(hubOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUI(dir, "svc"); !errors.Is(err, ErrNoUI) {
		t.Errorf("a plugin with no ui: %v", err)
	}
	if got := ListUI(dir); len(got) != 1 || got[0].Manifest.ID != "disks" {
		t.Errorf("ListUI = %+v", got)
	}
}

func TestUIShellHashesExactlyTheTwoScripts(t *testing.T) {
	page, csp, err := UIShell([]byte(`root.textContent = "x < y"`))
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{bootScript, `root.textContent = "x < y"`} {
		sum := sha256.Sum256([]byte(code))
		h := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if !strings.Contains(csp, h) || !strings.Contains(string(page), ">"+code+"</script>") {
			t.Errorf("script %.30q: hash %s not in %q, or not in the page", code, h, csp)
		}
	}
	if strings.Count(csp, "sha256-") != 2 || strings.Contains(csp, "allow-same-origin") {
		t.Errorf("csp = %q", csp)
	}
	for _, bad := range []string{"a</script>b", "a</SCRIPT >", "<!-- x"} {
		if _, _, err := UIShell([]byte(bad)); !errors.Is(err, ErrUnsafeModule) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestBridgeAllowlist(t *testing.T) {
	if _, ok := LookupOp("servers.delete"); ok {
		t.Error("an operation that is not in the list was found")
	}
	op, ok := LookupOp("inventory.disks")
	if !ok || op.PluginPermission("disks") != "disks.read" || op.Core != "inventory.read" {
		t.Errorf("op = %+v", op)
	}
	m, _ := ParseManifest(strings.NewReader(tabManifest))
	if m.Requests("disks.read") {
		t.Error("a manifest with no permissions requests one")
	}
}
