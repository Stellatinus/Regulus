package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CodexCircle/Regulus/internal/auth"
)

// A script with a read-only API token reads /api/agents; the same token is
// refused on every route that asks for a recent proof of the password, even
// when it holds the permission of the route (#406).
func TestAPITokenReadsAndIsRefusedOnEveryStepUpRoute(t *testing.T) {
	h := startRemoteHub(t)
	admin := newAPI(t, h, auth.RoleAdmin)
	call := func(secret, method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, strings.NewReader(string(b)))
		req.Header.Set("Authorization", "Bearer "+secret)
		rec := httptest.NewRecorder()
		admin.handler.ServeHTTP(rec, req)
		return rec
	}
	token := func(perms ...string) string {
		rec := admin.do("POST", "/api/tokens", map[string]any{"name": "script", "permissions": perms})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create token: %d %s", rec.Code, rec.Body)
		}
		var out struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Token
	}

	readOnly := token() // the default: everything that ends in .read
	if rec := call(readOnly, "GET", "/api/agents", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET /api/agents with a read-only token: %d %s", rec.Code, rec.Body)
	}
	if rec := call(readOnly, "GET", "/api/updates", nil); rec.Code != http.StatusForbidden {
		t.Errorf("a read-only token reads the updates (core.update): %d", rec.Code)
	}

	// The widest token a user can make: it still cannot pass a step-up.
	wide := token(auth.PermServersRead, auth.PermServersManage, auth.PermCoreUpdate, auth.PermPluginsManage)
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/api/agents/listener", map[string]any{"port": 8443}},
		{"POST", "/api/enrollment-tokens", map[string]string{"name": "db1"}},
		{"DELETE", "/api/agents/agent-nope", struct{}{}},
		{"PUT", "/api/updates/settings", map[string]string{"mode": "notify"}},
		{"POST", "/api/updates/rollout", map[string]any{}},
		{"POST", "/api/updates/apply", map[string]any{}},
		{"POST", "/api/store/plugins/disks/install", map[string]any{}},
		{"POST", "/api/store/plugins/disks/update", map[string]any{}},
		{"DELETE", "/api/store/plugins/disks", struct{}{}},
	} {
		rec := call(wide, r.method, r.path, r.body)
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusForbidden || body.Code != auth.StepUpUnavailableCode {
			t.Errorf("%s %s: status = %d, body = %s", r.method, r.path, rec.Code, rec.Body)
		}
	}
	// What does not need a proof works with the permission.
	if rec := call(wide, "GET", "/api/enrollment-tokens", nil); rec.Code != http.StatusOK {
		t.Errorf("list enrollment tokens: %d %s", rec.Code, rec.Body)
	}
	// The live stream is for the dashboard.
	if rec := call(wide, "GET", "/api/stream", nil); rec.Code != http.StatusForbidden {
		t.Errorf("the stream with a token: %d", rec.Code)
	}
	// Revoked from the API, refused at the next request.
	var list []auth.APIToken
	_ = json.Unmarshal(admin.do("GET", "/api/tokens", nil).Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("tokens = %+v", list)
	}
	for _, tk := range list {
		if rec := admin.do("DELETE", "/api/tokens/"+itoa64(tk.ID), struct{}{}); rec.Code != http.StatusNoContent {
			t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
		}
	}
	if rec := call(readOnly, "GET", "/api/agents", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a revoked token: %d", rec.Code)
	}
}

func itoa64(n int64) string { b, _ := json.Marshal(n); return string(b) }
