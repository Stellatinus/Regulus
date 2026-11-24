package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// tokenAPI is a handler with the account endpoints and a few routes that stand
// for the hub's: one read, one that changes something, one that needs a
// recent password and one that is for people only.
func tokenAPI(t *testing.T, s *Service) *client {
	t.Helper()
	mux := http.NewServeMux()
	s.Register(mux)
	api := http.NewServeMux()
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		_ = json.NewEncoder(w).Encode(map[string]any{"user": u.Username, "permissions": u.Permissions})
	})
	api.Handle("GET /api/agents", RequirePermission(PermServersRead, ok))
	api.Handle("PATCH /api/agents/{id}", RequirePermission(PermServersManage, RequireJSON(ok)))
	api.Handle("POST /api/enrollment-tokens", RequirePermission(PermServersManage, s.RequireStepUp(RequireJSON(ok))))
	api.Handle("GET /api/stream", RequirePermission(PermServersRead, RefuseTokens(ok)))
	for _, r := range APIRoutes(s) {
		api.Handle(r.Pattern, r.Handler)
	}
	mux.Handle("/api/", s.Require(api))
	return &client{t: t, handler: mux}
}

func bearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// makeToken makes a token as the logged-in client and returns it with its secret.
func makeToken(t *testing.T, c *client, body map[string]any) (APIToken, string) {
	t.Helper()
	rec := c.do("POST", "/api/tokens", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create token %v: status = %d, body = %s", body, rec.Code, rec.Body)
	}
	var out struct {
		APIToken
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.APIToken, out.Token
}

func TestAPITokenIsReadOnlyByDefaultAndOnlyItsHashIsStored(t *testing.T) {
	s := newService(t)
	admin, _ := setup(t, s)
	_ = admin
	c := tokenAPI(t, s)
	loginAs(t, c, "admin", goodPassword)

	tok, secret := makeToken(t, c, map[string]any{"name": "backup script"})
	if !strings.HasPrefix(secret, TokenPrefix) || len(secret) != len(TokenPrefix)+43 {
		t.Errorf("secret = %q", secret)
	}
	if tok.Prefix != secret[:8] || tok.Name != "backup script" || tok.ExpiresAt == nil {
		t.Errorf("token = %+v", tok)
	}
	if d := tok.ExpiresAt.Sub(tok.CreatedAt); d < 89*24*time.Hour || d > 91*24*time.Hour {
		t.Errorf("default lifetime = %v", d)
	}
	for _, p := range tok.Permissions {
		if !strings.HasSuffix(p, ".read") {
			t.Errorf("a default token holds %s", p)
		}
	}
	// The secret is nowhere in the database, only its hash.
	var stored string
	if err := s.store.db.QueryRow(`SELECT token_hash FROM api_tokens WHERE id = ?`, tok.ID).Scan(&stored); err != nil || stored != hashToken(secret) || strings.Contains(stored, secret) {
		t.Errorf("stored = %q, %v", stored, err)
	}
	// And not in the list either.
	if rec := c.do("GET", "/api/tokens", nil); strings.Contains(rec.Body.String(), secret) || !strings.Contains(rec.Body.String(), "backup script") {
		t.Errorf("list = %s", rec.Body)
	}

	script := &client{t: t, handler: c.handler}
	if rec := script.do("GET", "/api/agents", nil, bearer(secret)); rec.Code != http.StatusOK {
		t.Fatalf("read with the token: status = %d, body = %s", rec.Code, rec.Body)
	}
	// It changes nothing: no permission to.
	if rec := script.do("PATCH", "/api/agents/a1", map[string]string{"name": "x"}, bearer(secret)); rec.Code != http.StatusForbidden {
		t.Errorf("write with a read-only token: status = %d", rec.Code)
	}
	// No bearer, no cookie: refused as before.
	if rec := script.do("GET", "/api/agents", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no credentials: status = %d", rec.Code)
	}
}

func TestAPITokenIsRefusedOnStepUpAndPeopleOnlyRoutes(t *testing.T) {
	s := newService(t)
	setup(t, s)
	c := tokenAPI(t, s)
	loginAs(t, c, "admin", goodPassword)
	_, secret := makeToken(t, c, map[string]any{"name": "wide", "permissions": []string{"servers.read", "servers.manage"}})
	script := &client{t: t, handler: c.handler}

	// The same permission is enough for a rename and not for a step-up route.
	if rec := script.do("PATCH", "/api/agents/a1", map[string]string{"name": "x"}, bearer(secret)); rec.Code != http.StatusOK {
		t.Errorf("rename: status = %d, body = %s", rec.Code, rec.Body)
	}
	rec := script.do("POST", "/api/enrollment-tokens", map[string]string{"name": "x"}, bearer(secret))
	if rec.Code != http.StatusForbidden || codeOf(t, rec) != StepUpUnavailableCode {
		t.Errorf("step-up route: status = %d, body = %s", rec.Code, rec.Body)
	}
	// Even a fresh login on the owner's side changes nothing for the token.
	loginAs(t, c, "admin", goodPassword)
	rec = script.do("POST", "/api/enrollment-tokens", map[string]string{"name": "x"}, bearer(secret))
	if rec.Code != http.StatusForbidden || codeOf(t, rec) != StepUpUnavailableCode {
		t.Errorf("step-up route after a login: status = %d, body = %s", rec.Code, rec.Body)
	}
	// The live stream and the account are for people.
	for _, p := range []string{"/api/stream", "/api/account/sessions", "/api/account/two-factor"} {
		rec := script.do("GET", p, nil, bearer(secret))
		if rec.Code != http.StatusForbidden || codeOf(t, rec) != NotForTokensCode {
			t.Errorf("GET %s: status = %d, body = %s", p, rec.Code, rec.Body)
		}
	}
	if rec := script.do("POST", "/api/account/password", map[string]string{"current_password": goodPassword, "new_password": otherPassword}, bearer(secret)); rec.Code != http.StatusForbidden || codeOf(t, rec) != NotForTokensCode {
		t.Errorf("password change: status = %d, body = %s", rec.Code, rec.Body)
	}
	// Every account route that needs a permission a token cannot hold says no.
	for _, r := range stepUpRoutes {
		rec := script.do(r.method, r.path, r.body, bearer(secret))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with a token: status = %d", r.method, r.path, rec.Code)
		}
	}
	// A token cannot make tokens or list them.
	if rec := script.do("POST", "/api/tokens", map[string]any{"name": "child"}, bearer(secret)); rec.Code != http.StatusForbidden {
		t.Errorf("a token made a token: status = %d", rec.Code)
	}
	if rec := script.do("GET", "/api/tokens", nil, bearer(secret)); rec.Code != http.StatusForbidden {
		t.Errorf("a token listed tokens: status = %d", rec.Code)
	}
}

func TestAPITokenPermissionsAreTheIntersectionWithTheOwnersNow(t *testing.T) {
	s := newService(t)
	admin, _ := setup(t, s)
	addUser(t, s, admin, "ops", RoleOperator)
	// The operator may not make tokens until given the permission: a custom role.
	if _, err := s.CreateRole(t.Context(), admin, Role("ops-tokens"), "", []string{PermServersRead, PermServersManage, PermTokensCreate}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateUser(t.Context(), admin, "ops", UserChange{Role: ptr(Role("ops-tokens"))}); err != nil {
		t.Fatal(err)
	}
	c := tokenAPI(t, s)
	loginAs(t, c, "ops", otherPassword)

	// Not a permission they hold, nor one no token may hold.
	for _, p := range []string{"core.update", "users.manage", "tokens.create", "nonsense"} {
		if rec := c.do("POST", "/api/tokens", map[string]any{"name": "x", "permissions": []string{p}}); rec.Code != http.StatusBadRequest {
			t.Errorf("permission %s: status = %d, body = %s", p, rec.Code, rec.Body)
		}
	}
	if rec := c.do("POST", "/api/tokens", map[string]any{"name": "forever", "no_expiry": true}); rec.Code != http.StatusBadRequest {
		t.Errorf("no expiry without tokens.manage: status = %d", rec.Code)
	}
	_, secret := makeToken(t, c, map[string]any{"name": "ops script", "permissions": []string{"servers.read", "servers.manage"}})
	script := &client{t: t, handler: c.handler}
	if rec := script.do("PATCH", "/api/agents/a1", map[string]string{"name": "x"}, bearer(secret)); rec.Code != http.StatusOK {
		t.Fatalf("with both permissions: status = %d", rec.Code)
	}
	// The owner loses servers.manage: the token narrows at once.
	if _, err := s.CreateRole(t.Context(), admin, Role("ops-read"), "", []string{PermServersRead}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateUser(t.Context(), admin, "ops", UserChange{Role: ptr(Role("ops-read"))}); err != nil {
		t.Fatal(err)
	}
	if rec := script.do("PATCH", "/api/agents/a1", map[string]string{"name": "x"}, bearer(secret)); rec.Code != http.StatusForbidden {
		t.Errorf("after the owner lost the permission: status = %d", rec.Code)
	}
	if rec := script.do("GET", "/api/agents", nil, bearer(secret)); rec.Code != http.StatusOK {
		t.Errorf("the read the owner still has: status = %d", rec.Code)
	}
	// A disabled owner's tokens stop working.
	if _, err := s.UpdateUser(t.Context(), admin, "ops", UserChange{Disabled: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if rec := script.do("GET", "/api/agents", nil, bearer(secret)); rec.Code != http.StatusUnauthorized {
		t.Errorf("disabled owner: status = %d", rec.Code)
	}
	// Deleting the owner deletes the tokens.
	if err := s.DeleteUser(t.Context(), admin, "ops"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM api_tokens`).Scan(&n); err != nil || n != 0 {
		t.Errorf("tokens left after deleting the owner: %d, %v", n, err)
	}
}

func TestAPITokenExpiryRevocationAndNoCookie(t *testing.T) {
	s := newService(t)
	now := clockAt(s)
	setup(t, s)
	c := tokenAPI(t, s)
	loginAs(t, c, "admin", goodPassword)
	short, secret := makeToken(t, c, map[string]any{"name": "short", "expires_in_days": 1})
	_, forever := makeToken(t, c, map[string]any{"name": "forever", "no_expiry": true})
	revoked, revSecret := makeToken(t, c, map[string]any{"name": "revoked"})
	script := &client{t: t, handler: c.handler}

	// A cookie on a request with a bearer token is ignored: the token is the identity.
	rec := script.do("GET", "/api/agents", nil, bearer(secret), func(r *http.Request) { r.AddCookie(c.cookie) })
	if rec.Code != http.StatusOK || strings.Contains(rec.Header().Get("Set-Cookie"), CookieName) {
		t.Errorf("token with a cookie: status = %d, cookie = %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}
	if rec := script.do("GET", "/api/agents", nil, bearer("rgt_notatoken"), func(r *http.Request) { r.AddCookie(c.cookie) }); rec.Code != http.StatusUnauthorized {
		t.Errorf("a wrong token with a good cookie: status = %d", rec.Code)
	}

	if rec := c.do("DELETE", "/api/tokens/"+itoa(revoked.ID), struct{}{}); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := script.do("GET", "/api/agents", nil, bearer(revSecret)); rec.Code != http.StatusUnauthorized {
		t.Errorf("a revoked token: status = %d", rec.Code)
	}

	*now = now.Add(25 * time.Hour)
	if rec := script.do("GET", "/api/agents", nil, bearer(secret)); rec.Code != http.StatusUnauthorized {
		t.Errorf("an expired token: status = %d", rec.Code)
	}
	script.do("GET", "/api/agents", nil, bearer(secret)) // again: one audit line only
	if rec := script.do("GET", "/api/agents", nil, bearer(forever)); rec.Code != http.StatusOK {
		t.Errorf("a token without expiry: status = %d", rec.Code)
	}
	_ = short

	events := map[string]int{}
	entries, err := s.Audit(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		events[e.Event]++
	}
	if events[EventAPITokenCreated] != 3 || events[EventAPITokenRevoked] != 1 || events[EventAPITokenExpired] != 1 {
		t.Errorf("audit events = %v", events)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestAPITokenListAndRevokeBelongToTheOwnerUnlessManaged(t *testing.T) {
	s := newService(t)
	admin, _ := setup(t, s)
	if _, err := s.CreateRole(t.Context(), admin, Role("maker"), "", []string{PermServersRead, PermTokensCreate}); err != nil {
		t.Fatal(err)
	}
	addUser(t, s, admin, "gina", "maker")
	ca, cg := tokenAPI(t, s), tokenAPI(t, s)
	loginAs(t, ca, "admin", goodPassword)
	loginAs(t, cg, "gina", otherPassword)
	adminTok, _ := makeToken(t, ca, map[string]any{"name": "admins"})
	ginaTok, _ := makeToken(t, cg, map[string]any{"name": "ginas"})

	var mine []APIToken
	_ = json.Unmarshal(cg.do("GET", "/api/tokens", nil).Body.Bytes(), &mine)
	if len(mine) != 1 || mine[0].Name != "ginas" {
		t.Errorf("gina sees %+v", mine)
	}
	if rec := cg.do("GET", "/api/tokens?all=true", nil); rec.Code != http.StatusForbidden {
		t.Errorf("all without tokens.manage: status = %d", rec.Code)
	}
	// Someone else's token is not found, not forbidden.
	if rec := cg.do("DELETE", "/api/tokens/"+itoa(adminTok.ID), struct{}{}); rec.Code != http.StatusNotFound {
		t.Errorf("revoking another's token: status = %d", rec.Code)
	}
	var all []APIToken
	_ = json.Unmarshal(ca.do("GET", "/api/tokens?all=true", nil).Body.Bytes(), &all)
	if len(all) != 2 {
		t.Errorf("admin sees %d tokens", len(all))
	}
	if rec := ca.do("DELETE", "/api/tokens/"+itoa(ginaTok.ID), struct{}{}); rec.Code != http.StatusNoContent {
		t.Errorf("admin revokes gina's: status = %d", rec.Code)
	}
	if rec := ca.do("DELETE", "/api/tokens/99999", struct{}{}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown token: status = %d", rec.Code)
	}
}

func TestAPITokenCreationNeedsARecentPasswordAndTheCountIsBounded(t *testing.T) {
	s := newService(t)
	now := clockAt(s)
	setup(t, s)
	c := tokenAPI(t, s)
	loginAs(t, c, "admin", goodPassword)
	*now = now.Add(StepUpWindow + time.Second)
	rec := c.do("POST", "/api/tokens", map[string]any{"name": "x"})
	if rec.Code != http.StatusForbidden || codeOf(t, rec) != ReauthRequiredCode {
		t.Fatalf("without a proof: status = %d, body = %s", rec.Code, rec.Body)
	}
	loginAs(t, c, "admin", goodPassword)
	for i := range maxTokensPerUser {
		makeToken(t, c, map[string]any{"name": "t" + itoa(int64(i))})
	}
	if rec := c.do("POST", "/api/tokens", map[string]any{"name": "one too many"}); rec.Code != http.StatusBadRequest {
		t.Errorf("past the limit: status = %d", rec.Code)
	}
	if rec := c.do("POST", "/api/tokens", map[string]any{"name": " "}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty name: status = %d", rec.Code)
	}
}

func TestAPITokenFailuresCountAndAValidTokenHasItsOwnRate(t *testing.T) {
	s := newService(t)
	now := clockAt(s)
	setup(t, s)
	c := tokenAPI(t, s)
	loginAs(t, c, "admin", goodPassword)
	_, secret := makeToken(t, c, map[string]any{"name": "busy"})
	script := &client{t: t, handler: c.handler}

	// A burst, then 429 with Retry-After; time gives the budget back.
	limited := 0
	for range TokenBurst + 20 {
		if rec := script.do("GET", "/api/agents", nil, bearer(secret)); rec.Code == http.StatusTooManyRequests {
			limited++
			if rec.Header().Get("Retry-After") == "" {
				t.Error("429 without Retry-After")
			}
		}
	}
	if limited < 15 {
		t.Errorf("limited = %d after a burst of %d", limited, TokenBurst+20)
	}
	*now = now.Add(2 * time.Second)
	if rec := script.do("GET", "/api/agents", nil, bearer(secret)); rec.Code != http.StatusOK {
		t.Errorf("after waiting: status = %d", rec.Code)
	}

	// Wrong tokens count toward the limit of failed logins, per address.
	for i := range maxFailures {
		if rec := script.do("GET", "/api/agents", nil, bearer("rgt_wrong"+itoa(int64(i)))); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong token %d: status = %d", i, rec.Code)
		}
	}
	for range 3 {
		if rec := script.do("GET", "/api/agents", nil, bearer(secret)); rec.Code != http.StatusTooManyRequests {
			t.Errorf("a right token from a limited address: status = %d", rec.Code)
		}
	}
	// One audit line for the address, not one per request.
	entries, _ := s.Audit(t.Context(), 100)
	n := 0
	for _, e := range entries {
		if e.Event == EventAPITokenAuthFailed {
			n++
		}
	}
	if n != 1 {
		t.Errorf("api_token_auth_failed lines = %d, want 1", n)
	}
	// The same limiter protects the login of that address.
	if rec := script.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": goodPassword}); rec.Code != http.StatusTooManyRequests {
		t.Errorf("login from the limited address: status = %d", rec.Code)
	}
}

func TestAPITokenWritesAreAudited(t *testing.T) {
	s := newService(t)
	setup(t, s)
	c := tokenAPI(t, s)
	loginAs(t, c, "admin", goodPassword)
	_, secret := makeToken(t, c, map[string]any{"name": "w", "permissions": []string{"servers.read", "servers.manage"}})
	script := &client{t: t, handler: c.handler}
	script.do("GET", "/api/agents", nil, bearer(secret))
	// No JSON content type needed: a browser cannot send this header by itself.
	r := httptest.NewRequest("PATCH", "/api/agents/a1", bytes.NewReader([]byte(`{}`)))
	r.RemoteAddr = "192.0.2.1:1"
	r.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH without a content type: status = %d", rec.Code)
	}
	entries, _ := s.Audit(t.Context(), 100)
	used := 0
	for _, e := range entries {
		if e.Event == EventAPITokenUsed {
			used++
			if e.Actor != "admin" || e.Detail != "PATCH /api/agents/a1" || e.Code != CodeAPITokenRequest {
				t.Errorf("entry = %+v", e)
			}
		}
	}
	if used != 1 {
		t.Errorf("api_token_used lines = %d, want 1 (the read is not audited)", used)
	}
}
