package auth

// stepup.go: step-up re-authentication (#279). The actions that would turn a
// stolen session into lasting control (a new admin, a changed role or
// permission, an enrollment token, an update installed as root...) need a
// recent proof of the password, and of the second factor when the user has
// one. The proof is kept on the session for StepUpWindow; a login counts as
// one. Which routes need it is decided where they are registered: here for
// the accounts, in package hub for the fleet and the updates.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// StepUpWindow is how long a proof of the password lets a session do the
// actions that need one.
const StepUpWindow = 5 * time.Minute

// ReauthRequiredCode is the code of the 403 answer to a request that needs a
// recent proof: the dashboard asks for the password (and code) and retries.
const ReauthRequiredCode = "reauth_required"

// CodeRequiredCode is the code of the 403 answer to a re-authentication
// without a code by a user who has two-factor on: the dashboard then asks for
// one. Nothing is checked or counted for such a request.
const CodeRequiredCode = "two_factor_code_required"

// EventReauth is the audit event of a successful re-authentication. A wrong
// password is recorded as EventPasswordFailed with the detail
// "re-authentication".
const EventReauth = "reauthenticated"

// ErrReauthRequired is the answer to a sensitive action without a recent proof.
var ErrReauthRequired = errors.New("confirm your password to do this")

// sessionKey carries the session of an authenticated request.
type sessionKey struct{}

// session is what Require learns about the session of a request.
type session struct {
	tokenHash string
	reauthAt  time.Time // zero: never proved since this column existed
}

func sessionFrom(ctx context.Context) (session, bool) {
	s, ok := ctx.Value(sessionKey{}).(session)
	return s, ok
}

// recent reports whether the session proved the password within StepUpWindow.
func (s *Service) recent(sess session) bool {
	if sess.reauthAt.IsZero() {
		return false
	}
	age := s.now().Sub(sess.reauthAt)
	return age >= -time.Minute && age <= StepUpWindow // a little clock skew between hubs is fine
}

// RequireStepUp lets a request through only if its session proved the password
// (and the second factor) within StepUpWindow; otherwise it answers 403 with
// the code reauth_required. Use it inside Require, after the permission check,
// so that a user without the permission is refused rather than asked.
func (s *Service) RequireStepUp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := sessionFrom(r.Context())
		if !ok || !s.recent(sess) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": ErrReauthRequired.Error(),
				"code":  ReauthRequiredCode,
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Reauthenticate checks the password of the logged-in user, and a code (TOTP
// or recovery) when two-factor is on, and records the proof on the session of
// tokenHash. It returns until when the proof lasts. Wrong passwords and codes
// count toward the same limit as the other checks of a logged-in user, so a
// stolen session cannot guess the password here either.
func (s *Service) Reauthenticate(ctx context.Context, u User, tokenHash, password, code string) (time.Time, error) {
	if err := s.checkPasswordFor(ctx, u, password, "re-authentication"); err != nil {
		return time.Time{}, err
	}
	if u.TwoFactor {
		if err := s.verifyRecoverable(ctx, u, code); err != nil {
			return time.Time{}, err
		}
	}
	s.clearFailures(s.twoFactorKey(u.ID))
	now := s.now()
	if err := s.store.markReauth(ctx, tokenHash, now); err != nil {
		return time.Time{}, err
	}
	s.audit(ctx, u.Username, EventReauth, u.Username, "")
	return now.Add(StepUpWindow), nil
}

// handleReauth is POST /api/account/reauth.
func (s *Service) handleReauth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	user, _ := UserFrom(r.Context())
	if user.TwoFactor && strings.TrimSpace(req.Code) == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "enter the code of your authenticator app, or a recovery code",
			"code":  CodeRequiredCode,
		})
		return
	}
	sess, _ := sessionFrom(r.Context())
	until, err := s.Reauthenticate(r.Context(), user, sess.tokenHash, req.Password, req.Code)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reauth_until": until})
}
