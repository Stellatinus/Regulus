package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/store"
)

type fakeStore struct{}

func (fakeStore) Catalogue(context.Context) store.CatalogueJSON {
	latest := "0.1.0"
	return store.CatalogueJSON{
		Sources: []store.SourceJSON{{Name: "regulus-plugins", Trust: "official", State: "ok", IssuedAt: "2026-12-01T09:00:00Z", Expires: "2026-12-22T09:00:00Z"}},
		Plugins: []store.PluginJSON{{ID: "disks", Name: "Disks", Source: "regulus-plugins", Audience: "admin", Latest: &latest,
			Versions: []store.VersionJSON{{Version: "0.1.0", Channel: "stable", Compatible: true,
				Permissions: []store.Permission{{ID: "disks.read", Description: "See disks."}}, Provides: []string{}, Needs: []store.Need{}}}}},
	}
}

func storeAPI(t *testing.T, role auth.Role, src StoreSource) *api {
	t.Helper()
	h := startHub(t)
	a := newAPI(t, h, role)
	a.handler = NewHandler(Deps{UI: fstest.MapFS{}, Agents: h, Layouts: h, Log: quiet(), Auth: h.auth, Store: src})
	return a
}

func TestStoreCatalogueContract(t *testing.T) {
	rec := storeAPI(t, auth.RoleAdmin, fakeStore{}).do("GET", "/api/store", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	p := body["plugins"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "name", "description", "source", "homepage", "audience", "installed", "latest", "versions"} {
		if _, ok := p[k]; !ok {
			t.Errorf("plugin has no %q: %v", k, p)
		}
	}
	v := p["versions"].([]any)[0].(map[string]any)
	for _, k := range []string{"version", "channel", "released_at", "compatible", "permissions", "provides", "needs", "yanked"} {
		if _, ok := v[k]; !ok {
			t.Errorf("version has no %q: %v", k, v)
		}
	}
	if p["installed"] != nil {
		t.Errorf("installed = %v, want null", p["installed"])
	}
	s := body["sources"].([]any)[0].(map[string]any)
	if s["state"] != "ok" || s["trust"] != "official" {
		t.Errorf("source = %v", s)
	}
}

func TestStoreWithoutASourceIsEmptyNotAnError(t *testing.T) {
	rec := storeAPI(t, auth.RoleAdmin, nil).do("GET", "/api/store", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"plugins":[]`) || !strings.Contains(rec.Body.String(), `"sources":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestStoreNeedsThePluginsPermission(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleViewer, auth.RoleOperator} {
		a := storeAPI(t, role, fakeStore{})
		if rec := a.do("GET", "/api/store", nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s: GET = %d, want 403", role, rec.Code)
		}
		if rec := a.do("POST", "/api/store/plugins/disks/install", nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s: install = %d, want 403", role, rec.Code)
		}
	}
}

// fakeInstaller records what the handlers pass and answers as told.
type fakeInstaller struct {
	err                               error
	by, id, version, source, lastKind string
}

func (f *fakeInstaller) Install(_ context.Context, by, id, version, source string) (store.Outcome, error) {
	f.lastKind, f.by, f.id, f.version, f.source = "install", by, id, version, source
	return store.Outcome{Plugin: id, Action: "install", Version: "0.1.0", Source: "regulus-plugins"}, f.err
}
func (f *fakeInstaller) Update(_ context.Context, by, id, version string) (store.Outcome, error) {
	f.lastKind, f.by, f.id, f.version = "update", by, id, version
	return store.Outcome{Plugin: id, Action: "install", Version: "0.2.0", From: "0.1.0"}, f.err
}
func (f *fakeInstaller) Remove(_ context.Context, by, id string) (store.Outcome, error) {
	f.lastKind, f.by, f.id = "remove", by, id
	return store.Outcome{Plugin: id, Action: "remove"}, f.err
}

func storeAPIWith(t *testing.T, role auth.Role, in StoreInstaller) *api {
	t.Helper()
	h := startHub(t)
	a := newAPI(t, h, role)
	a.handler = NewHandler(Deps{UI: fstest.MapFS{}, Agents: h, Layouts: h, Log: quiet(), Auth: h.auth, Store: fakeStore{}, StoreInstaller: in})
	return a
}

func TestStoreLifecycleRoutes(t *testing.T) {
	f := &fakeInstaller{}
	a := storeAPIWith(t, auth.RoleAdmin, f)
	for _, c := range []struct {
		method, path string
		body         any
		kind         string
	}{
		{"POST", "/api/store/plugins/disks/install", map[string]string{"version": "0.1.0", "source": "regulus-plugins"}, "install"},
		{"POST", "/api/store/plugins/disks/update", map[string]string{}, "update"},
		{"DELETE", "/api/store/plugins/disks", nil, "remove"},
	} {
		rec := a.do(c.method, c.path, c.body)
		if rec.Code != http.StatusOK || f.lastKind != c.kind || f.id != "disks" || f.by == "" {
			t.Errorf("%s %s: %d %s (kind %q, id %q, by %q)", c.method, c.path, rec.Code, rec.Body, f.lastKind, f.id, f.by)
		}
	}
}

func TestStoreRefusalsKeepTheirCodeAndStatus(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{&store.Error{CodeStr: store.ErrCodeNotFound, Msg: "x"}, http.StatusNotFound, "plugin_not_found"},
		{&store.Error{CodeStr: store.ErrCodeAlreadyInstalled, Msg: "x"}, http.StatusConflict, "already_installed"},
		{&store.Error{CodeStr: store.ErrCodeHelperTimeout, Msg: "x"}, http.StatusGatewayTimeout, "helper_timeout"},
		{&store.Error{CodeStr: store.ErrCodeHelperFailed, Msg: "x", Helper: store.CodeChecksum}, http.StatusUnprocessableEntity, "helper_failed"},
		{&store.Error{CodeStr: store.ErrCodePluginsOff, Msg: "x"}, http.StatusConflict, "plugins_off"},
	} {
		a := storeAPIWith(t, auth.RoleAdmin, &fakeInstaller{err: c.err})
		rec := a.do("POST", "/api/store/plugins/disks/install", map[string]string{})
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != c.status || body["code"] != c.code {
			t.Errorf("%v: %d %s, want %d %s", c.err, rec.Code, rec.Body, c.status, c.code)
		}
	}
}

func TestStoreWritesNeedThePermissionAndTheInstaller(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleViewer, auth.RoleOperator} {
		a := storeAPIWith(t, role, &fakeInstaller{})
		for _, m := range [][2]string{{"POST", "/api/store/plugins/disks/install"}, {"POST", "/api/store/plugins/disks/update"}, {"DELETE", "/api/store/plugins/disks"}} {
			if rec := a.do(m[0], m[1], nil); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s %s = %d, want 403", role, m[0], m[1], rec.Code)
			}
		}
	}
	// No installer (plugins off): a clear refusal, never a fake success.
	rec := storeAPIWith(t, auth.RoleAdmin, nil).do("POST", "/api/store/plugins/disks/install", map[string]string{})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "plugins_off") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// progressInstaller is a fakeInstaller that also reports progress.
type progressInstaller struct {
	fakeInstaller
	p  store.Progress
	ok bool
}

func (f *progressInstaller) Progress() (store.Progress, bool) { return f.p, f.ok }

func TestStoreProgressRoute(t *testing.T) {
	type answer struct {
		Progress *struct {
			Plugin  string `json:"plugin"`
			Action  string `json:"action"`
			State   string `json:"state"`
			Step    string `json:"step"`
			Percent int    `json:"percent"`
			Code    string `json:"code"`
			Helper  string `json:"helper_code"`
		} `json:"progress"`
	}
	read := func(a *api) (int, answer) {
		rec := a.do("GET", "/api/store/progress", nil)
		var out answer
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	// The operation in progress, with the step and the percentage.
	in := &progressInstaller{p: store.Progress{Plugin: "disks", Action: "install", State: store.ProgressRunning, Step: store.StepDownloading, Percent: 42}, ok: true}
	code, out := read(storeAPIWith(t, auth.RoleAdmin, in))
	if code != http.StatusOK || out.Progress == nil || out.Progress.Step != "downloading" || out.Progress.Percent != 42 || out.Progress.Plugin != "disks" {
		t.Fatalf("running: %d %+v", code, out)
	}
	// A failed one carries its codes, not text to show.
	in = &progressInstaller{p: store.Progress{Plugin: "disks", Action: "update", State: store.ProgressFailed, Percent: -1, Code: store.ErrCodeHelperFailed, Helper: store.CodeChecksum}, ok: true}
	if _, out = read(storeAPIWith(t, auth.RoleAdmin, in)); out.Progress == nil || out.Progress.State != "failed" || out.Progress.Code != "helper_failed" || out.Progress.Helper != "checksum" {
		t.Fatalf("failed: %+v", out)
	}
	// Nothing in progress, an installer that does not report, and no installer at all: null.
	for name, in := range map[string]StoreInstaller{"idle": &progressInstaller{}, "no progress source": &fakeInstaller{}, "plugins off": nil} {
		if code, out = read(storeAPIWith(t, auth.RoleAdmin, in)); code != http.StatusOK || out.Progress != nil {
			t.Errorf("%s: %d %+v", name, code, out)
		}
	}
	// It is for those who manage plugins.
	for _, role := range []auth.Role{auth.RoleViewer, auth.RoleOperator} {
		if code, _ := read(storeAPIWith(t, role, &progressInstaller{})); code != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", role, code)
		}
	}
}
