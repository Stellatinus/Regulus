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
		user, err := s.userOf(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, ErrUnauthenticated.Error())
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
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withRequestClient returns the request context with the client recorded.
func (s *Service) withRequestClient(r *http.Request) context.Context {
	return WithClient(r.Context(), Client{Remote: s.remoteIP(r), UserAgent: clip(r.UserAgent(), 200)})
}

func (s *Service) userOf(r *http.Request) (User, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return User{}, ErrUnauthenticated
	}
	return s.Authenticate(r.Context(), c.Value)
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
	case errors.Is(err, ErrInvalidCode), errors.Is(err, ErrLoginExpired):
		writeError(w, http.StatusUnauthorized, err.Error())
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
		writeError(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, ErrRateLimited):
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, ErrBusy):
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, ErrBadSetupToken):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrSetupDone):
		writeError(w, http.StatusConflict, err.Error())
	case isInputError(err):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.log.Error("authentication failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
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
		ct := r.Header.Get("Content-Type")
		if mt, _, _ := strings.Cut(ct, ";"); strings.TrimSpace(strings.ToLower(mt)) != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				writeError(w, http.StatusForbidden, "cross-origin request refused")
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
		writeError(w, http.StatusBadRequest, "invalid JSON body")
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
