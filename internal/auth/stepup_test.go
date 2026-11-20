package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// stepUpRoutes are the account endpoints that need a recent proof of the
// password (docs/authentication.md), with a body each would accept.
var stepUpRoutes = []struct {
	method, path string
	body         any
}{
	{"POST", "/api/users", map[string]string{"username": "newbie", "password": otherPassword, "role": "viewer"}},
	{"PATCH", "/api/users/gina", map[string]any{"role": "operator"}},
	{"POST", "/api/users/gina/reset", struct{}{}},
	{"DELETE", "/api/users/gina/two-factor", struct{}{}},
	{"DELETE", "/api/users/gina", struct{}{}},
	{"PUT", "/api/settings/security", map[string]any{"two_factor_policy": "admins"}},
	{"POST", "/api/roles", map[string]any{"name": "helpdesk", "permissions": []string{"servers.read"}}},
	{"PATCH", "/api/roles/helpdesk", map[string]any{"description": "x"}},
	{"DELETE", "/api/roles/helpdesk", struct{}{}},
}

// clockAt makes the service's clock settable.
func clockAt(s *Service) *time.Time {
	now := time.Now()
	s.now = func() time.Time { return now }
	return &now
}

func codeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Code
}

func TestStepUpIsAskedForOnceTheLoginIsOld(t *testing.T) {
	s := newService(t)
	now := clockAt(s)
	admin, _ := setup(t, s)
	addUser(t, s, admin, "gina", RoleViewer)
	c := newClient(t, s)
	loginAs(t, c, "admin", goodPassword)

	// A login is a proof: right after it nothing is asked.
	if rec := c.do("PATCH", "/api/users/gina", map[string]any{"role": "operator"}); rec.Code != http.StatusOK {
		t.Fatalf("right after the login: status = %d, body = %s", rec.Code, rec.Body)
	}

	*now = now.Add(StepUpWindow + time.Second)
	for _, r := range stepUpRoutes {
		rec := c.do(r.method, r.path, r.body)
		if rec.Code != http.StatusForbidden || codeOf(t, rec) != ReauthRequiredCode {
			t.Errorf("%s %s without a recent proof: status = %d, body = %s", r.method, r.path, rec.Code, rec.Body)
		}
	}
	// Reading and the own account are not affected.
	if rec := c.do("GET", "/api/users", nil); rec.Code != http.StatusOK {
		t.Errorf("list users: status = %d", rec.Code)
	}
	if rec := c.do("GET", "/api/account/sessions", nil); rec.Code != http.StatusOK {
		t.Errorf("own sessions: status = %d", rec.Code)
	}

	if rec := c.do("POST", "/api/account/reauth", map[string]string{"password": "wrong password!"}); rec.Code != http.StatusForbidden || codeOf(t, rec) != "wrong_password" {
		t.Errorf("wrong password: status = %d, body = %s", rec.Code, rec.Body)
	}
	rec := c.do("POST", "/api/account/reauth", map[string]string{"password": goodPassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("reauth: status = %d, body = %s", rec.Code, rec.Body)
	}
	var got struct {
		Until time.Time `json:"reauth_until"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Until.Sub(now.Add(StepUpWindow)).Abs() > time.Second {
		t.Errorf("reauth_until = %v, want %v (%v)", got.Until, now.Add(StepUpWindow), err)
	}
	if rec := c.do("POST", "/api/users", stepUpRoutes[0].body); rec.Code != http.StatusCreated {
		t.Errorf("after the proof: status = %d, body = %s", rec.Code, rec.Body)
	}

	// The proof belongs to the session, not to the user.
	other := newClient(t, s)
	other.handler = c.handler
	loginAs(t, other, "admin", goodPassword)
	*now = now.Add(StepUpWindow + time.Second)
	if rec := other.do("PATCH", "/api/users/gina", map[string]any{"role": "viewer"}); codeOf(t, rec) != ReauthRequiredCode {
		t.Errorf("an old second session: status = %d", rec.Code)
	}

	entries, err := s.Audit(context.Background(), 50)
	if err != nil || !hasEvent(entries, EventReauth, "admin", "admin") {
		t.Errorf("the re-authentication is not in the audit log: %v", err)
	}
}

func TestStepUpComesAfterThePermission(t *testing.T) {
	s := newService(t)
	now := clockAt(s)
	admin, _ := setup(t, s)
	addUser(t, s, admin, "viewer1", RoleViewer)
	c := newClient(t, s)
	loginAs(t, c, "viewer1", otherPassword)
	*now = now.Add(StepUpWindow + time.Second)
	rec := c.do("POST", "/api/users", stepUpRoutes[0].body)
	if rec.Code != http.StatusForbidden || codeOf(t, rec) == ReauthRequiredCode {
		t.Errorf("a viewer is asked for the password instead of refused: %d %s", rec.Code, rec.Body)
	}
}

func TestStepUpNeedsTheCodeWithTwoFactor(t *testing.T) {
	s := newService(t)
	now := clockAt(s)
	e := enroll(t, s)
	c := newClient(t, s)
	c.cookie = &http.Cookie{Name: CookieName, Value: e.session}
	*now = now.Add(StepUpWindow + time.Second)

	if rec := c.do("POST", "/api/account/reauth", map[string]string{"password": goodPassword}); rec.Code != http.StatusForbidden || codeOf(t, rec) != CodeRequiredCode {
		t.Errorf("password without a code: status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := c.do("PUT", "/api/settings/security", map[string]any{"two_factor_policy": "off"}); codeOf(t, rec) != ReauthRequiredCode {
		t.Errorf("a failed proof counted: %d %s", rec.Code, rec.Body)
	}
	if rec := c.do("POST", "/api/account/reauth", map[string]string{"password": "wrong password!", "code": e.code(s)}); rec.Code != http.StatusForbidden {
		t.Errorf("wrong password with a good code: status = %d", rec.Code)
	}
	if rec := c.do("POST", "/api/account/reauth", map[string]string{"password": goodPassword, "code": e.code(s)}); rec.Code != http.StatusOK {
		t.Fatalf("password and code: status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := c.do("PUT", "/api/settings/security", map[string]any{"two_factor_policy": "off"}); rec.Code != http.StatusOK {
		t.Errorf("after the proof: status = %d, body = %s", rec.Code, rec.Body)
	}

	// A recovery code works too, and is used up.
	*now = now.Add(StepUpWindow + time.Second)
	if rec := c.do("POST", "/api/account/reauth", map[string]string{"password": goodPassword, "code": e.recovery[0]}); rec.Code != http.StatusOK {
		t.Errorf("recovery code: status = %d", rec.Code)
	}
	*now = now.Add(StepUpWindow + time.Second)
	if rec := c.do("POST", "/api/account/reauth", map[string]string{"password": goodPassword, "code": e.recovery[0]}); rec.Code != http.StatusForbidden {
		t.Errorf("a recovery code worked twice: status = %d", rec.Code)
	}
}

func TestReauthenticateIsRateLimited(t *testing.T) {
	s := newService(t)
	u, token := setup(t, s)
	ctx := context.Background()
	for range maxFailures {
		if _, err := s.Reauthenticate(ctx, u, hashToken(token), "wrong password!", ""); !errors.Is(err, ErrWrongPassword) {
			t.Fatalf("wrong password: %v", err)
		}
	}
	if _, err := s.Reauthenticate(ctx, u, hashToken(token), goodPassword, ""); !errors.Is(err, ErrRateLimited) {
		t.Errorf("after %d failures: %v, want ErrRateLimited", maxFailures, err)
	}
}
