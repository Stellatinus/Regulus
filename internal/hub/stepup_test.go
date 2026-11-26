package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/CodexCircle/Regulus/internal/auth"
)

// The fleet and update actions that need a recent proof of the password
// (#279, docs/authentication.md). A session without one gets 403 with the
// code the dashboard acts on; the same session can do them after
// POST /api/account/reauth.
func TestFleetAndUpdateActionsNeedARecentProof(t *testing.T) {
	h := startRemoteHub(t)
	admin := newAPI(t, h, auth.RoleAdmin)
	// A session created before the proof was kept (reauth_at NULL) has none.
	if _, err := h.db.ExecContext(context.Background(), `UPDATE sessions SET reauth_at = NULL`); err != nil {
		t.Fatal(err)
	}
	routes := []struct {
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
	}
	for _, r := range routes {
		rec := admin.do(r.method, r.path, r.body)
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusForbidden || body.Code != auth.ReauthRequiredCode {
			t.Errorf("%s %s without a proof: status = %d, body = %s", r.method, r.path, rec.Code, rec.Body)
		}
	}
	// Reading and harmless actions are not affected.
	for _, r := range []struct{ method, path string }{{"GET", "/api/enrollment-tokens"}, {"GET", "/api/updates"}, {"POST", "/api/updates/rollout/stop"}} {
		var body any
		if r.method == "POST" {
			body = map[string]any{}
		}
		if rec := admin.do(r.method, r.path, body); rec.Code == http.StatusForbidden {
			t.Errorf("%s %s: refused: %s", r.method, r.path, rec.Body)
		}
	}

	if rec := admin.do("POST", "/api/account/reauth", map[string]string{"password": "correct horse battery"}); rec.Code != http.StatusOK {
		t.Fatalf("reauth: status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := admin.do("POST", "/api/enrollment-tokens", map[string]string{"name": "db1"}); rec.Code != http.StatusCreated {
		t.Errorf("after the proof: status = %d, body = %s", rec.Code, rec.Body)
	}
}
