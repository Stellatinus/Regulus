package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// CookieName is the name of the session cookie.
const CookieName = "regulus_session"

const maxBodyBytes = 4096

type ctxKey struct{}

// UserFrom returns the authenticated user of a request that passed Require.
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

// Register adds the authentication endpoints to mux:
//
//	GET  /api/auth/me      who is logged in, and whether the first-run setup is pending
//	POST /api/auth/setup   create the first admin (needs the one-time setup token)
//	POST /api/auth/login   start a session, or answer that a second factor is needed
//	POST /api/auth/login/two-factor  finish such a login with a code
//	POST /api/auth/logout  end the session
//	POST /api/auth/reset   choose a new password with a one-time reset token
//
// The cookie is HttpOnly and SameSite=Lax, and POST requests must be JSON from
// the same origin, which together rule out cross-site request forgery.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/me", s.handleMe)
	mux.Handle("POST /api/auth/setup", RequireJSON(http.HandlerFunc(s.handleSetup)))
	mux.Handle("POST /api/auth/login", RequireJSON(http.HandlerFunc(s.handleLogin)))
	mux.Handle("POST /api/auth/login/two-factor", RequireJSON(http.HandlerFunc(s.handleLoginTwoFactor)))
	mux.Handle("POST /api/auth/logout", RequireJSON(http.HandlerFunc(s.handleLogout)))
	mux.Handle("POST /api/auth/reset", RequireJSON(http.HandlerFunc(s.handleResetPassword)))
}

// Routes are the patterns Register adds to the mux.
var Routes = []string{
	"GET /api/auth/me",
	"POST /api/auth/setup",
	"POST /api/auth/login",
	"POST /api/auth/login/two-factor",
	"POST /api/auth/logout",
	"POST /api/auth/reset",
}

// Require lets a request through only if it carries a valid session cookie;
// otherwise it answers 401. The user is available with UserFrom.
func (s *Service) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A bearer token is the only identity of the request: a cookie on it
		// is ignored.
		if secret, ok := bearerOf(r); ok {
			s.requireToken(w, r, secret, next)
			return
		}
		user, sess, err := s.sessionOf(r)
		if err != nil {
			writeFail(w, http.StatusUnauthorized, ErrUnauthenticated)
			return
		}
		if s.MustEnroll(r.Context(), user) && !strings.HasPrefix(r.URL.Path, twoFactorPath) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "the hub requires two-factor authentication: set it up before doing anything else",
				"code":  "two_factor_setup_required",
			})
			return
		}
		ctx := context.WithValue(s.withRequestClient(r), ctxKey{}, user)
		ctx = context.WithValue(ctx, sessionKey{}, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withRequestClient returns the request context with the client recorded.
func (s *Service) withRequestClient(r *http.Request) context.Context {
	return WithClient(r.Context(), Client{Remote: s.remoteIP(r), UserAgent: clip(r.UserAgent(), 200)})
}

func (s *Service) userOf(r *http.Request) (User, error) {
	u, _, err := s.sessionOf(r)
	return u, err
}

// sessionOf returns the user and the session of the request's cookie.
func (s *Service) sessionOf(r *http.Request) (User, session, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return User{}, session{}, ErrUnauthenticated
	}
	return s.authenticate(r.Context(), c.Value)
}

type credentials struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	SetupToken string `json:"setup_token"`
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	resp := struct {
		SetupRequired bool  `json:"setup_required"`
		User          *User `json:"user"`
		// TwoFactorSetupRequired is true when the hub requires two-factor
		// authentication from this user, who has not set it up yet.
		TwoFactorSetupRequired bool `json:"two_factor_setup_required"`
	}{SetupRequired: s.SetupRequired()}
	if u, err := s.userOf(r); err == nil {
		resp.User = &u
		resp.TwoFactorSetupRequired = s.MustEnroll(r.Context(), u)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleSetup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	user, token, expires, err := s.Setup(s.withRequestClient(r), c.SetupToken, c.Username, c.Password)
	s.finishLogin(w, r, user, token, expires, err)
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	user, token, expires, err := s.Login(s.withRequestClient(r), s.remoteIP(r), c.Username, c.Password)
	var need *TwoFactorRequiredError
	if errors.As(err, &need) {
		writeJSON(w, http.StatusOK, map[string]any{
			"two_factor_required": true,
			"pending_token":       need.Pending,
			"expires_at":          need.Expires,
		})
		return
	}
	s.finishLogin(w, r, user, token, expires, err)
}

// loginResponse is the user, plus a note when a recovery code was used.
type loginResponse struct {
	User
	RecoveryCodeUsed  bool `json:"recovery_code_used,omitempty"`
	RecoveryCodesLeft int  `json:"recovery_codes_left,omitempty"`
}

func (s *Service) handleLoginTwoFactor(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pending string `json:"pending_token"`
		Code    string `json:"code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	res, err := s.CompleteLogin(s.withRequestClient(r), s.remoteIP(r), req.Pending, req.Code)
	switch {
	case err == nil:
		s.setSessionCookie(w, r, res.Token, res.Expires)
		writeJSON(w, http.StatusOK, loginResponse{User: res.User, RecoveryCodeUsed: res.RecoveryUsed, RecoveryCodesLeft: res.RecoveryLeft})
	case errors.Is(err, ErrInvalidCode):
		writeErrorCode(w, http.StatusUnauthorized, err.Error(), "invalid_code")
	case errors.Is(err, ErrLoginExpired):
		// The dashboard tells the two apart by the code, not by the text,
		// which is English and may change.
		writeErrorCode(w, http.StatusUnauthorized, err.Error(), "login_expired")
	default:
		s.fail(w, err)
	}
}

func (s *Service) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) finishLogin(w http.ResponseWriter, r *http.Request, user User, token string, expires time.Time, err error) {
	switch {
	case err == nil:
		s.setSessionCookie(w, r, token, expires)
		writeJSON(w, http.StatusOK, user)
	case errors.Is(err, ErrInvalidCredentials):
		writeFail(w, http.StatusUnauthorized, err)
	case errors.Is(err, ErrRateLimited):
		w.Header().Set("Retry-After", "900")
		writeFail(w, http.StatusTooManyRequests, err)
	case errors.Is(err, ErrBusy):
		w.Header().Set("Retry-After", "5")
		writeFail(w, http.StatusServiceUnavailable, err)
	case errors.Is(err, ErrBadSetupToken):
		writeFail(w, http.StatusForbidden, err)
	case errors.Is(err, ErrSetupDone):
		writeFail(w, http.StatusConflict, err)
	case isInputError(err):
		writeFail(w, http.StatusBadRequest, err)
	default:
		s.log.Error("authentication failed", "err", err)
		writeErrorCode(w, http.StatusInternalServerError, "internal error", codeInternal)
	}
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		if err := s.Logout(r.Context(), c.Value); err != nil {
			s.log.Error("logout failed", "err", err)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// RequireJSON rejects requests that change state and that a browser could send
// from another site: the body must be JSON (a content type forms cannot send
// without a CORS preflight) and an Origin header, when present, must be the
// hub's own. Wrap every POST, PUT and DELETE handler with it.
func RequireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The rule protects cookies from other sites; a browser never adds a
		// bearer header by itself, so a token needs neither.
		if IsToken(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		ct := r.Header.Get("Content-Type")
		if mt, _, _ := strings.Cut(ct, ";"); strings.TrimSpace(strings.ToLower(mt)) != "application/json" {
			writeErrorCode(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json", codeUnsupportedMedia)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				writeErrorCode(w, http.StatusForbidden, "cross-origin request refused", codeCrossOrigin)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isInputError reports whether err comes from validateCredentials.
func isInputError(err error) bool {
	var e *inputError
	return errors.As(err, &e)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "invalid JSON body", codeInvalidJSON)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// errorCodes gives the errors of this package a code a client can translate
// without reading the English text (#330). The text stays in the answer for
// scripts and for clients that do not know the code; a new error that people
// meet in the dashboard gets a line here and a text in the catalogs
// (dashboard/src/i18n/messages/core.*.ts, "hub.error.<code>").
var errorCodes = []struct {
	err  error
	code string
}{
	{ErrUserNotFound, "user_not_found"},
	{ErrSessionNotFound, "session_not_found"},
	{ErrRoleNotFound, "role_not_found"},
	{ErrUserExists, "user_exists"},
	{ErrLastAdmin, "last_admin"},
	{ErrRoleExists, "role_exists"},
	{ErrBuiltinRole, "builtin_role"},
	{ErrRoleInUse, "role_in_use"},
	{ErrWrongPassword, "wrong_password"},
	{ErrForbidden, "forbidden"},
	{ErrUnauthenticated, "unauthenticated"},
	{ErrRateLimited, "rate_limited"},
	{ErrBusy, "busy"},
	{ErrInvalidReset, "invalid_reset"},
	{ErrInvalidCode, "invalid_code"},
	{ErrTwoFactorOn, "two_factor_on"},
	{ErrTwoFactorOff, "two_factor_off"},
	{ErrTwoFactorNotStarted, "two_factor_not_started"},
	{ErrTwoFactorMandatory, "two_factor_mandatory"},
	{ErrTwoFactorUnavailable, "two_factor_unavailable"},
	{ErrInvalidCredentials, "invalid_credentials"},
	{ErrBadSetupToken, "bad_setup_token"},
	{ErrSetupDone, "setup_done"},
	{ErrTokenNotFound, "token_not_found"},
}

// Codes of answers that no sentinel error carries (#354).
const (
	codeInternal         = "internal_error"
	codeInvalidJSON      = "invalid_json"
	codeInvalidLimit     = "invalid_limit"
	codeUnsupportedMedia = "unsupported_media_type"
	codeCrossOrigin      = "cross_origin"
)

// ErrorCodes lists every code this package writes (the table above, the
// constants, the input codes, and the ones written in place: login_expired and
// reauth_required), for the test that checks the
// dashboard has a text for each (#354).
func ErrorCodes() []string {
	codes := []string{
		codeInternal, codeInvalidJSON, codeInvalidLimit, codeUnsupportedMedia, codeCrossOrigin,
		"login_expired", "reauth_required",
		StepUpUnavailableCode, NotForTokensCode, codeTokenInvalid, codeTokenLimited, "rate_limited",
	}
	codes = append(codes, inputCodes...)
	for _, c := range errorCodes {
		codes = append(codes, c.code)
	}
	return codes
}

// inputCodes are the codes of inputErr (service.go), one per validation.
var inputCodes = []string{
	"username_invalid", "password_too_short", "password_too_long", "new_password_length", "account_disabled",
	"role_name_invalid", "role_name_builtin", "role_unknown", "permission_unknown", "role_description_length", "policy_invalid",
	"token_name_invalid", "token_permissions_none", "token_permission_forbidden", "token_permission_not_held",
	"token_no_expiry_forbidden", "token_expiry_invalid", "token_limit",
}

// errorCodeOf is the code of err, or "" for an error without one.
func errorCodeOf(err error) string {
	for _, c := range errorCodes {
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	return ""
}

// writeFail answers with err's text and, when it has one, its code.
func writeFail(w http.ResponseWriter, status int, err error) {
	var in *inputError
	if errors.As(err, &in) && in.code != "" {
		body := map[string]any{"error": err.Error(), "code": in.code}
		if len(in.args) > 0 {
			body["args"] = in.args
		}
		writeJSON(w, status, body)
		return
	}
	if code := errorCodeOf(err); code != "" {
		writeErrorCode(w, status, err.Error(), code)
		return
	}
	writeError(w, status, err.Error())
}

// writeErrorCode is writeError with a machine-readable code, for answers a
// client must tell apart without reading the English text.
func writeErrorCode(w http.ResponseWriter, status int, msg, code string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// peerIP is the address of the other end of the connection.
func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// trusted reports whether addr is one of the configured reverse proxies.
func (s *Service) trusted(addr string) bool {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range s.proxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// remoteIP is the address of the client: the peer of the connection, unless
// the peer is a trusted proxy, in which case it is the rightmost entry of
// X-Forwarded-For that is not itself a trusted proxy. The entries to the left
// of that one are written by the client and are never used. When every entry
// is a trusted proxy, the leftmost is the client (an internal one); when an
// entry is not an address, the walk stops at the hop that forwarded it.
func (s *Service) remoteIP(r *http.Request) string {
	peer := peerIP(r)
	if !s.trusted(peer) {
		return peer
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, h := range strings.Split(v, ",") {
			hops = append(hops, strings.TrimSpace(h))
		}
	}
	hop := peer
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(hops[i])
		if err != nil {
			return hop
		}
		hop = a.Unmap().String()
		if !s.trusted(hop) {
			return hop
		}
	}
	return hop
}

// isHTTPS reports whether the client reached the hub over TLS, directly or
// through a trusted reverse proxy that says so in X-Forwarded-Proto (the proxy
// is the rightmost value). From any other peer the header is ignored.
func (s *Service) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !s.trusted(peerIP(r)) {
		return false
	}
	vals := r.Header.Values("X-Forwarded-Proto")
	if len(vals) == 0 {
		return false
	}
	last := vals[len(vals)-1]
	if i := strings.LastIndex(last, ","); i >= 0 {
		last = last[i+1:]
	}
	return strings.EqualFold(strings.TrimSpace(last), "https")
}
