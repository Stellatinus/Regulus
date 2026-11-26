package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/plugin"
)

const disksManifest = `id: disks
version: 0.1.0-alpha.1
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
permissions:
  - id: disks.read
    description: See the disks.
`

// installUI writes a plugin with a ui component in a plugins directory, as
// <dir>/<id>/ (current false) or as <dir>/<id>/<version>/ with a current link.
func installUI(t *testing.T, dir, manifest, module string, current bool) {
	t.Helper()
	id := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(manifest, "\n", 2)[0], "id:"))
	root := filepath.Join(dir, id)
	if current {
		root = filepath.Join(dir, id, "0.1.0-alpha.1")
	}
	if err := os.MkdirAll(filepath.Join(root, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ui", "disks.js"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if current {
		if err := os.Symlink("0.1.0-alpha.1", filepath.Join(dir, id, "current")); err != nil {
			t.Fatal(err)
		}
	}
}

func uiAPI(t *testing.T, h *Hub, role auth.Role, dir string) *api {
	t.Helper()
	return withPluginsDir(newAPI(t, h, role), h, dir)
}

// withPluginsDir is the same logged-in person on a handler that reads the
// dashboard's plugin pages from dir (a hub allows one setup, so one person).
func withPluginsDir(a *api, h *Hub, dir string) *api {
	c := *a
	c.handler = NewHandler(Deps{UI: fstest.MapFS{}, Agents: h, Layouts: h, Log: quiet(), Auth: h.auth, Fleet: h, Updates: h.updates, Rollout: h, Plugins: h, Disks: h, PluginsDir: dir, Version: h.version, Certificates: h})
	return &c
}

func TestThePluginUIListHasTheTabsOfInstalledPlugins(t *testing.T) {
	h := startHub(t)
	dir := t.TempDir()
	installUI(t, dir, disksManifest, "export {}", true)
	// A plugin with no ui is not listed; a broken directory is skipped.
	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	viewer := uiAPI(t, h, auth.RoleViewer, dir)

	rec := viewer.do("GET", "/api/plugins/ui", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Plugins []struct {
			ID          string   `json:"id"`
			Version     string   `json:"version"`
			Module      string   `json:"module"`
			Permissions []string `json:"permissions"`
			NodeTab     struct {
				ID    string                  `json:"id"`
				Title struct{ EN, IT string } `json:"title"`
			} `json:"node_tab"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Plugins) != 1 {
		t.Fatalf("%s (%v)", rec.Body, err)
	}
	p := body.Plugins[0]
	if p.ID != "disks" || p.Version != "0.1.0-alpha.1" || p.Module != "ui/disks.js" || p.NodeTab.ID != "disks" || p.NodeTab.Title.IT != "Dischi" || len(p.Permissions) != 1 || p.Permissions[0] != "disks.read" {
		t.Errorf("plugin = %+v", p)
	}

	// With no plugins directory the list is empty, never an error.
	none := withPluginsDir(viewer, h, "")
	if rec := none.do("GET", "/api/plugins/ui", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"plugins":[]`) {
		t.Errorf("no dir: %d %s", rec.Code, rec.Body)
	}
}

func TestThePluginPageIsSandboxedAndNeedsALogin(t *testing.T) {
	h := startHub(t)
	dir := t.TempDir()
	installUI(t, dir, disksManifest, `document.getElementById("root").textContent = "hi"`, false)
	viewer := uiAPI(t, h, auth.RoleViewer, dir)

	rec := viewer.do("GET", "/api/plugins/disks/ui", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page: %d %s", rec.Code, rec.Body)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"sandbox allow-scripts", "default-src 'none'", "connect-src 'none'", "script-src 'sha256-", "frame-ancestors 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "allow-same-origin") || strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src 'unsafe-inline'") {
		t.Errorf("CSP %q is too open", csp)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" || !strings.Contains(rec.Body.String(), `textContent = "hi"`) {
		t.Errorf("headers %v, body %s", rec.Header(), rec.Body)
	}

	// The module file, with the policy of an empty origin.
	rec = viewer.do("GET", "/api/plugins/disks/module", nil)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("module: %d %v", rec.Code, rec.Header())
	}
	// Nothing else of the plugin is served, and an id that is not a plugin id
	// never reaches the file system.
	for _, p := range []string{"/api/plugins/disks/plugin.yaml", "/api/plugins/disks/ui/plugin.yaml", "/api/plugins/disks/ui/../plugin.yaml"} {
		if rec := viewer.do("GET", p, nil); rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "version:") {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
	for _, id := range []string{"..", "%2e%2e", "nobody", "Disks"} {
		for _, suffix := range []string{"/ui", "/module"} {
			if rec := viewer.do("GET", "/api/plugins/"+id+suffix, nil); rec.Code == http.StatusOK {
				t.Errorf("id %s%s: %d", id, suffix, rec.Code)
			}
		}
	}
	// No session, no page.
	anon := httptest.NewRecorder()
	viewer.handler.ServeHTTP(anon, httptest.NewRequest("GET", "/api/plugins/disks/ui", nil))
	if anon.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", anon.Code)
	}
}

func TestAModuleThatCouldCloseItsScriptIsNotShown(t *testing.T) {
	h := startHub(t)
	dir := t.TempDir()
	installUI(t, dir, disksManifest, `const s = "</SCRIPT><script>alert(1)</script>"`, false)
	viewer := uiAPI(t, h, auth.RoleViewer, dir)
	rec := viewer.do("GET", "/api/plugins/disks/ui", nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), codePluginUnsafeModule) {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestACurrentLinkThatLeavesThePluginIsRefused(t *testing.T) {
	h := startHub(t)
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "disks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "plugin.yaml"), []byte(disksManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "disks", "current")); err != nil {
		t.Fatal(err)
	}
	viewer := uiAPI(t, h, auth.RoleViewer, dir)
	if rec := viewer.do("GET", "/api/plugins/disks/ui", nil); rec.Code != http.StatusNotFound {
		t.Errorf("page: %d", rec.Code)
	}
	if rec := viewer.do("GET", "/api/plugins/ui", nil); strings.Contains(rec.Body.String(), "disks") {
		t.Errorf("listed: %s", rec.Body)
	}
}

func TestTheBridgeNeedsThePluginAndThePersonToAllow(t *testing.T) {
	h := startHub(t)
	recordDisks(t, h, "a", time.Now(), sata("sda", "SECRET-SERIAL"))
	dir := t.TempDir()
	installUI(t, dir, disksManifest, "export {}", false)
	// A plugin that has a ui but does not request disks.read.
	quiet := "id: quiet\nversion: 0.1.0\nname: Quiet\napi: 1\ncomponents:\n  ui:\n    module: ui/disks.js\n"
	installUI(t, dir, quiet, "export {}", false)

	admin := uiAPI(t, h, auth.RoleAdmin, dir)
	call := func(a *api, id string, body any) (int, string) {
		rec := a.do("POST", "/api/plugins/"+id+"/call", body)
		return rec.Code, rec.Body.String()
	}

	code, body := call(admin, "disks", map[string]any{"op": "inventory.disks", "params": map[string]string{"node": "a"}})
	if code != http.StatusOK || !strings.Contains(body, "SECRET-SERIAL") || !strings.Contains(body, `"agent":"a"`) {
		t.Fatalf("disks: %d %s", code, body)
	}
	code, body = call(admin, "disks", map[string]any{"op": "inventory.disk_moves"})
	if code != http.StatusOK || !strings.Contains(body, `"moves":[]`) {
		t.Errorf("moves: %d %s", code, body)
	}

	cases := []struct {
		name   string
		a      *api
		id     string
		body   any
		status int
		code   string
	}{
		{"unknown plugin", admin, "nobody", map[string]any{"op": "inventory.disks"}, 404, codePluginNotFound},
		{"unknown op", admin, "disks", map[string]any{"op": "servers.delete"}, 404, codePluginOpUnknown},
		{"permission not requested", admin, "quiet", map[string]any{"op": "inventory.disks", "params": map[string]string{"node": "a"}}, 403, codePluginNotRequested},
		{"no node", admin, "disks", map[string]any{"op": "inventory.disks"}, 400, codePluginBadParams},
		{"unknown param", admin, "disks", map[string]any{"op": "inventory.disks", "params": map[string]any{"node": "a", "x": 1}}, 400, codePluginBadParams},
		{"limit out of range", admin, "disks", map[string]any{"op": "inventory.disk_moves", "params": map[string]int{"limit": 5000}}, 400, codePluginBadParams},
	}
	for _, c := range cases {
		code, body := call(c.a, c.id, c.body)
		if code != c.status || !strings.Contains(body, `"code":"`+c.code+`"`) {
			t.Errorf("%s: %d %s", c.name, code, body)
		}
	}

	// The plugin asks for it, the person may not: the narrower wins, and the
	// serial number does not leak.
	h2 := startHub(t)
	recordDisks(t, h2, "a", time.Now(), sata("sda", "SECRET-SERIAL"))
	viewer := uiAPI(t, h2, auth.RoleViewer, dir)
	code, body = call(viewer, "disks", map[string]any{"op": "inventory.disks", "params": map[string]string{"node": "a"}})
	if code != http.StatusForbidden || !strings.Contains(body, codePluginForbidden) || strings.Contains(body, "SECRET-SERIAL") {
		t.Errorf("viewer: %d %s", code, body)
	}
}

func TestEveryBridgeOperationNamesARealPermissionAndIsRun(t *testing.T) {
	real := map[string]bool{}
	for _, p := range auth.Permissions() {
		real[p.ID] = true
	}
	for _, op := range plugin.Ops {
		if !real[op.Core] {
			t.Errorf("%s needs %q, which is not a permission of the core", op.Name, op.Core)
		}
		if op.Name != "inventory.disks" && op.Name != "inventory.disk_moves" {
			t.Errorf("%s is in the allowlist but the bridge does not run it", op.Name)
		}
	}
}
