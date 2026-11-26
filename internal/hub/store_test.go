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

func TestInstallIsNotImplementedAndSaysSo(t *testing.T) {
	rec := storeAPI(t, auth.RoleAdmin, fakeStore{}).do("POST", "/api/store/plugins/disks/install", nil)
	if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), "root helper") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
