package auth

// apitoken.go: API tokens (#406, docs/authentication.md "API tokens"). A token
// is a credential for a program: made by a user, owned by that user, and able
// to do less than they can. The secret is shown once and only its SHA-256 is
// stored. A request that carries `Authorization: Bearer rgt_...` is
// authenticated by the token alone (a cookie on it is ignored), with the
// permissions the token lists that its owner still holds, and it can never
// pass step-up or reach the account endpoints.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Everything about the token's form and limits.
const (
	// TokenPrefix starts every API token.
	TokenPrefix = "rgt_"
	// tokenSecretBytes is the length of the random part before base64url.
	tokenSecretBytes = 32
	// tokenShown is how many characters of the token are kept to tell tokens
	// apart: "rgt_" and four more.
	tokenShown = 8

	// DefaultTokenLifetime is the expiry of a token made without a choice.
	DefaultTokenLifetime = 90 * 24 * time.Hour
	maxTokenDays         = 3650
	maxTokensPerUser     = 50
	maxTokenName         = 64

	// tokenTouchEvery is how often the last use of a token is written.
	tokenTouchEvery = time.Minute
	// TokenRate and TokenBurst limit a valid token: requests a second, and the
	// burst it may spend at once.
	TokenRate  = 20
	TokenBurst = 100
	// tokenAuthFailedEvery is how often one address gets a line in the audit
	// log for being limited, so that a flood cannot fill it.
	tokenAuthFailedEvery = time.Hour
	// maxTrackedTokens bounds the in-memory tables that follow tokens.
	maxTrackedTokens = 10000
)

// Codes of the answers that only tokens meet.
const (
	// StepUpUnavailableCode is the code of the 403 answer to a token on a route
	// that asks for a recent proof of the password.
	StepUpUnavailableCode = "step_up_not_available_to_tokens"
	// NotForTokensCode is the code of the 403 answer to a token on a route that
	// is for people only (the account, the live stream).
	NotForTokensCode = "not_available_to_tokens"
	codeTokenInvalid = "invalid_token"
	codeTokenLimited = "token_rate_limited"
)

// The audit events of the tokens.
const (
	EventAPITokenCreated    = "api_token_created"
	EventAPITokenRevoked    = "api_token_revoked"
	EventAPITokenExpired    = "api_token_expired"
	EventAPITokenAuthFailed = "api_token_auth_failed"
	EventAPITokenUsed       = "api_token_used"
)

// Permissions of the tokens, in permissions.go: PermTokensCreate and
// PermTokensManage.

// ErrTokenNotFound is the answer for a token that does not exist, or that the
// caller may not see.
var ErrTokenNotFound = errors.New("token not found")

// tokenForbidden are the permissions no token can hold: whoever has them can
// widen their own reach, and creating a token needs a password anyway.
var tokenForbidden = []string{PermUsersManage, PermRolesManage, PermTokensManage, PermTokensCreate}

// APIToken is a token as the API shows it: never with its secret, except in
// the answer that creates it.
type APIToken struct {
	ID          int64      `json:"id"`
	Owner       string     `json:"owner"`
	Name        string     `json:"name"`
	Prefix      string     `json:"prefix"`
	Permissions []string   `json:"permissions"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	LastUsedIP  string     `json:"last_used_ip,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at"`
}

// tokenInfo is what a request authenticated by a token carries.
type tokenInfo struct {
	id     int64
	prefix string
}

type tokenKey struct{}

func tokenFrom(ctx context.Context) (tokenInfo, bool) {
	t, ok := ctx.Value(tokenKey{}).(tokenInfo)
	return t, ok
}

// IsToken reports whether the request was authenticated by an API token.
func IsToken(ctx context.Context) bool {
	_, ok := tokenFrom(ctx)
	return ok
}

// RefuseTokens answers 403 to a request made with an API token. Use it on the
// routes that are for people only (the live stream).
func RefuseTokens(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsToken(r.Context()) {
			writeErrorCode(w, http.StatusForbidden, "this is not available to API tokens", NotForTokensCode)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- storage ----

type tokenRow struct {
	APIToken
	userID   int64
	disabled bool
	role     Role
	twoFac   bool
	created  time.Time
}

const tokenColumns = `t.id, t.user_id, u.username, t.name, t.prefix, t.permissions, t.created_at, t.expires_at, t.last_used_at, t.last_used_ip, t.revoked_at`

func scanToken(sc interface{ Scan(...any) error }) (APIToken, int64, error) {
	var (
		t                          APIToken
		uid                        int64
		perms, created             string
		expires, lastUsed, revoked sql.NullString
	)
	if err := sc.Scan(&t.ID, &uid, &t.Owner, &t.Name, &t.Prefix, &perms, &created, &expires, &lastUsed, &t.LastUsedIP, &revoked); err != nil {
		return APIToken{}, 0, err
	}
	t.Permissions = splitPerms(perms)
	t.CreatedAt, _ = time.Parse(timeLayout, created)
	parse := func(n sql.NullString) *time.Time {
		if !n.Valid {
			return nil
		}
		v, err := time.Parse(timeLayout, n.String)
		if err != nil {
			return nil
		}
		return &v
	}
	t.ExpiresAt, t.LastUsedAt, t.RevokedAt = parse(expires), parse(lastUsed), parse(revoked)
	return t, uid, nil
}

func splitPerms(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

func (s store) createToken(ctx context.Context, userID int64, name, prefix, hash string, perms []string, now time.Time, expires *time.Time) (int64, error) {
	var exp any
	if expires != nil {
		exp = expires.UTC().Format(timeLayout)
	}
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO api_tokens (user_id, name, prefix, token_hash, permissions, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		userID, name, prefix, hash, strings.Join(perms, ","), now.UTC().Format(timeLayout), exp).Scan(&id)
	return id, err
}

// countTokens is how many tokens of the user are still usable.
func (s store) countTokens(ctx context.Context, userID int64, now time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`,
		userID, now.UTC().Format(timeLayout)).Scan(&n)
	return n, err
}

// listTokens returns the tokens of one user, or of everybody when userID is 0,
// newest first.
func (s store) listTokens(ctx context.Context, userID int64) ([]APIToken, error) {
	q := `SELECT ` + tokenColumns + ` FROM api_tokens t JOIN users u ON u.id = t.user_id`
	var args []any
	if userID != 0 {
		q += ` WHERE t.user_id = ?`
		args = append(args, userID)
	}
	rows, err := s.read().QueryContext(ctx, q+` ORDER BY t.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []APIToken{}
	for rows.Next() {
		t, _, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s store) tokenByID(ctx context.Context, id int64) (APIToken, int64, error) {
	t, uid, err := scanToken(s.db.QueryRowContext(ctx,
		`SELECT `+tokenColumns+` FROM api_tokens t JOIN users u ON u.id = t.user_id WHERE t.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, 0, ErrTokenNotFound
	}
	return t, uid, err
}

// tokenByHash finds a token by the hash of its secret, with its owner.
func (s store) tokenByHash(ctx context.Context, hash string) (tokenRow, error) {
	var r tokenRow
	var (
		perms, created, ucreated   string
		expires, lastUsed, revoked sql.NullString
	)
	err := s.read().QueryRowContext(ctx,
		`SELECT t.id, t.user_id, t.name, t.prefix, t.permissions, t.created_at, t.expires_at, t.last_used_at, t.revoked_at,
		        users.username, users.role, users.created_at, users.disabled, `+hasTwoFactor+`
		 FROM api_tokens t JOIN users ON users.id = t.user_id WHERE t.token_hash = ?`, hash).
		Scan(&r.ID, &r.userID, &r.Name, &r.Prefix, &perms, &created, &expires, &lastUsed, &revoked,
			&r.Owner, &r.role, &ucreated, &r.disabled, &r.twoFac)
	if errors.Is(err, sql.ErrNoRows) {
		return tokenRow{}, ErrUnauthenticated
	}
	if err != nil {
		return tokenRow{}, err
	}
	r.Permissions = splitPerms(perms)
	r.CreatedAt, _ = time.Parse(timeLayout, created)
	parse := func(n sql.NullString) *time.Time {
		if !n.Valid {
			return nil
		}
		v, err := time.Parse(timeLayout, n.String)
		if err != nil {
			return nil
		}
		return &v
	}
	r.ExpiresAt, r.LastUsedAt, r.RevokedAt = parse(expires), parse(lastUsed), parse(revoked)
	r.created, _ = time.Parse(timeLayout, ucreated)
	return r, nil
}

func (s store) touchToken(ctx context.Context, id int64, now time.Time, ip string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ?, last_used_ip = ? WHERE id = ?`, now.UTC().Format(timeLayout), ip, id)
}

func (s store) revokeToken(ctx context.Context, id int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now.UTC().Format(timeLayout), id)
	return err
}

// ---- the service ----

// tokenState is what the service keeps in memory about tokens: the buckets of
// the rate limit, the last write of the last use, the tokens already reported
// as expired and the addresses already reported as limited.
type tokenState struct {
	buckets map[int64]*bucket
	touched map[int64]time.Time
	expired map[int64]bool
	limited map[string]time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

func (s *Service) tokenMem() *tokenState {
	if s.tokens == nil {
		s.tokens = &tokenState{buckets: map[int64]*bucket{}, touched: map[int64]time.Time{}, expired: map[int64]bool{}, limited: map[string]time.Time{}}
	}
	if len(s.tokens.buckets) > maxTrackedTokens {
		s.tokens.buckets = map[int64]*bucket{}
	}
	if len(s.tokens.expired) > maxTrackedTokens {
		s.tokens.expired = map[int64]bool{}
	}
	if len(s.tokens.touched) > maxTrackedTokens {
		s.tokens.touched = map[int64]time.Time{}
	}
	if len(s.tokens.limited) > maxTrackedTokens {
		s.tokens.limited = map[string]time.Time{}
	}
	return s.tokens
}

// allowToken spends one request of the token's own budget.
func (s *Service) allowToken(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.tokenMem()
	now := s.now()
	b := m.buckets[id]
	if b == nil {
		b = &bucket{tokens: TokenBurst, at: now}
		m.buckets[id] = b
	}
	if d := now.Sub(b.at).Seconds(); d > 0 {
		b.tokens = min(TokenBurst, b.tokens+d*TokenRate)
	}
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// generateToken makes a secret and the hash and prefix that are stored.
func generateToken() (secret, hash, prefix string, err error) {
	b := make([]byte, tokenSecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", "", fmt.Errorf("generate token: %w", err)
	}
	secret = TokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return secret, hashToken(secret), secret[:tokenShown], nil
}

// TokenRequest is what a user asks for when making a token.
type TokenRequest struct {
	Name string `json:"name"`
	// Permissions the token gets; empty means read only (everything the owner
	// holds that ends in ".read").
	Permissions []string `json:"permissions"`
	// ExpiresInDays is the lifetime; zero means DefaultTokenLifetime.
	ExpiresInDays int `json:"expires_in_days"`
	// NoExpiry asks for a token that never expires; only a user with
	// tokens.manage may.
	NoExpiry bool `json:"no_expiry"`
}

// CreateToken makes a token for owner. It returns the token and its secret,
// which is not kept and never shown again.
func (s *Service) CreateToken(ctx context.Context, owner User, req TokenRequest) (APIToken, string, error) {
	name := strings.TrimSpace(req.Name)
	if n := len([]rune(name)); n < 1 || n > maxTokenName {
		return APIToken{}, "", inputErr("token_name_invalid", fmt.Sprintf("the token name must be 1 to %d characters", maxTokenName), "max", maxTokenName)
	}
	perms := slices.Clone(req.Permissions)
	if len(perms) == 0 {
		for _, p := range owner.Permissions {
			if strings.HasSuffix(p, ".read") {
				perms = append(perms, p)
			}
		}
		if len(perms) == 0 {
			return APIToken{}, "", inputErr("token_permissions_none", "you hold no read-only permission to give the token: choose permissions")
		}
	}
	slices.Sort(perms)
	perms = slices.Compact(perms)
	for _, p := range perms {
		switch {
		case !validPermission(p):
			return APIToken{}, "", inputErr("permission_unknown", fmt.Sprintf("unknown permission %q", p), "permission", p)
		case slices.Contains(tokenForbidden, p):
			return APIToken{}, "", inputErr("token_permission_forbidden", fmt.Sprintf("a token can never hold %s", p), "permission", p)
		case !owner.Can(p):
			return APIToken{}, "", inputErr("token_permission_not_held", fmt.Sprintf("you do not hold %s, so you cannot give it to a token", p), "permission", p)
		}
	}
	now := s.now()
	var expires *time.Time
	switch {
	case req.NoExpiry:
		if !owner.Can(PermTokensManage) {
			return APIToken{}, "", inputErr("token_no_expiry_forbidden", "a token that never expires needs the permission to manage tokens")
		}
	case req.ExpiresInDays < 0 || req.ExpiresInDays > maxTokenDays:
		return APIToken{}, "", inputErr("token_expiry_invalid", fmt.Sprintf("the lifetime must be 1 to %d days", maxTokenDays), "max", maxTokenDays)
	default:
		life := DefaultTokenLifetime
		if req.ExpiresInDays > 0 {
			life = time.Duration(req.ExpiresInDays) * 24 * time.Hour
		}
		e := now.Add(life)
		expires = &e
	}
	if n, err := s.store.countTokens(ctx, owner.ID, now); err != nil {
		return APIToken{}, "", err
	} else if n >= maxTokensPerUser {
		return APIToken{}, "", inputErr("token_limit", fmt.Sprintf("an account can have %d tokens: revoke one first", maxTokensPerUser), "max", maxTokensPerUser)
	}
	secret, hash, prefix, err := generateToken()
	if err != nil {
		return APIToken{}, "", err
	}
	id, err := s.store.createToken(ctx, owner.ID, name, prefix, hash, perms, now, expires)
	if err != nil {
		return APIToken{}, "", err
	}
	s.audit(WithCode(ctx, CodeAPIToken, "name", name, "permissions", strings.Join(perms, ",")), owner.Username, EventAPITokenCreated, prefix, name)
	return APIToken{ID: id, Owner: owner.Username, Name: name, Prefix: prefix, Permissions: perms, CreatedAt: now.UTC(), ExpiresAt: expires}, secret, nil
}

// ListTokens returns the tokens of the user, or of everybody (all), which needs
// tokens.manage; the HTTP layer checks it.
func (s *Service) ListTokens(ctx context.Context, owner User, all bool) ([]APIToken, error) {
	if all {
		return s.store.listTokens(ctx, 0)
	}
	return s.store.listTokens(ctx, owner.ID)
}

// RevokeToken ends a token. The owner may revoke their own; tokens.manage may
// revoke anyone's. A token that is not the caller's to see is "not found".
func (s *Service) RevokeToken(ctx context.Context, by User, id int64) error {
	t, uid, err := s.store.tokenByID(ctx, id)
	if err != nil {
		return err
	}
	if uid != by.ID && !by.Can(PermTokensManage) {
		return ErrTokenNotFound
	}
	if t.RevokedAt != nil {
		return nil
	}
	if err := s.store.revokeToken(ctx, id, s.now()); err != nil {
		return err
	}
	s.audit(WithCode(ctx, CodeAPITokenName, "name", t.Name), by.Username, EventAPITokenRevoked, t.Prefix, t.Name)
	return nil
}

// bearerOf returns the token of an Authorization header, and whether the
// header is a bearer one.
func bearerOf(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	scheme, token, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(token), true
}

// tokenFailure is why a bearer request was not accepted.
type tokenFailure struct {
	status int
	code   string
	msg    string
	retry  int // Retry-After, seconds
}

// authenticateToken checks the token of a request. It counts the failures
// toward the same limiter as failed logins, per client address. A good token
// returns its owner with the permissions it may use now: the token's list,
// narrowed to what the owner holds at this moment.
func (s *Service) authenticateToken(ctx context.Context, secret, remote string) (User, tokenInfo, *tokenFailure) {
	att, ok := s.begin(s.ipKey(remote))
	if !ok {
		s.reportLimited(ctx, remote)
		return User{}, tokenInfo{}, &tokenFailure{http.StatusTooManyRequests, "rate_limited", ErrRateLimited.Error(), 900}
	}
	defer att.abandon()
	invalid := &tokenFailure{http.StatusUnauthorized, codeTokenInvalid, "invalid API token", 0}
	if !strings.HasPrefix(secret, TokenPrefix) || len(secret) > 128 {
		att.failed()
		return User{}, tokenInfo{}, invalid
	}
	row, err := s.store.tokenByHash(ctx, hashToken(secret))
	switch {
	case errors.Is(err, ErrUnauthenticated):
		att.failed()
		return User{}, tokenInfo{}, invalid
	case err != nil:
		s.log.Error("checking an API token", "err", err)
		return User{}, tokenInfo{}, &tokenFailure{http.StatusInternalServerError, codeInternal, "internal error", 0}
	}
	now := s.now()
	if row.RevokedAt != nil || row.disabled {
		att.failed()
		return User{}, tokenInfo{}, invalid
	}
	if row.ExpiresAt != nil && !now.Before(*row.ExpiresAt) {
		att.failed()
		s.mu.Lock()
		first := !s.tokenMem().expired[row.ID]
		s.tokens.expired[row.ID] = true
		s.mu.Unlock()
		if first {
			s.audit(WithCode(ctx, CodeAPITokenName, "name", row.Name), row.Owner, EventAPITokenExpired, row.Prefix, row.Name)
		}
		return User{}, tokenInfo{}, invalid
	}
	att.succeeded()
	if !s.allowToken(row.ID) {
		return User{}, tokenInfo{}, &tokenFailure{http.StatusTooManyRequests, codeTokenLimited, "too many requests with this token", 1}
	}
	user := User{ID: row.userID, Username: row.Owner, Role: row.role, CreatedAt: row.created, TwoFactor: row.twoFac}
	owner := s.permsOfUser(ctx, user)
	for _, p := range row.Permissions {
		if owner.has(p) && !slices.Contains(tokenForbidden, p) {
			user.Permissions = append(user.Permissions, p)
		}
	}
	if user.Permissions == nil {
		user.Permissions = []string{}
	}
	s.mu.Lock()
	m := s.tokenMem()
	touch := now.Sub(m.touched[row.ID]) >= tokenTouchEvery
	if touch {
		m.touched[row.ID] = now
	}
	s.mu.Unlock()
	if touch {
		s.store.touchToken(ctx, row.ID, now, clientFrom(ctx).Remote)
	}
	return user, tokenInfo{id: row.ID, prefix: row.Prefix}, nil
}

// reportLimited writes one audit line for an address that is being limited,
// at most one an hour, so a flood cannot fill the log.
func (s *Service) reportLimited(ctx context.Context, remote string) {
	key := addressKey(remote)
	s.mu.Lock()
	m := s.tokenMem()
	now := s.now()
	if at, ok := m.limited[key]; ok && now.Sub(at) < tokenAuthFailedEvery {
		s.mu.Unlock()
		return
	}
	m.limited[key] = now
	s.mu.Unlock()
	s.audit(WithCode(ctx, CodeAPITokenLimited), "", EventAPITokenAuthFailed, "", "too many failed token checks from this address")
}

// requireToken is the bearer branch of Require.
func (s *Service) requireToken(w http.ResponseWriter, r *http.Request, secret string, next http.Handler) {
	ctx := s.withRequestClient(r)
	user, info, fail := s.authenticateToken(ctx, secret, clientFrom(ctx).Remote)
	if fail != nil {
		if fail.retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(fail.retry))
		}
		if fail.status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		}
		writeErrorCode(w, fail.status, fail.msg, fail.code)
		return
	}
	if s.MustEnroll(ctx, user) || strings.HasPrefix(r.URL.Path, "/api/account/") {
		writeErrorCode(w, http.StatusForbidden, "this is not available to API tokens", NotForTokensCode)
		return
	}
	ctx = context.WithValue(ctx, ctxKey{}, user)
	ctx = context.WithValue(ctx, tokenKey{}, info)
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		s.audit(WithCode(ctx, CodeAPITokenRequest, "method", r.Method, "path", r.URL.Path), user.Username, EventAPITokenUsed, info.prefix, r.Method+" "+r.URL.Path)
	}
	next.ServeHTTP(w, r.WithContext(ctx))
}

// ---- HTTP ----

// tokenRoutes are the endpoints of the tokens. They are for people: a token
// holds none of the permissions they need.
func tokenRoutes(s *Service) []Route {
	own := []string{PermTokensCreate, PermTokensManage}
	return []Route{
		{"GET /api/tokens", RequireAnyPermission(own, http.HandlerFunc(s.handleListTokens))},
		{"POST /api/tokens", RequirePermission(PermTokensCreate, s.RequireStepUp(RequireJSON(http.HandlerFunc(s.handleCreateToken))))},
		{"DELETE /api/tokens/{id}", RequireAnyPermission(own, RequireJSON(http.HandlerFunc(s.handleRevokeToken)))},
	}
}

func (s *Service) handleListTokens(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	all := r.URL.Query().Get("all") == "true"
	if all && !user.Can(PermTokensManage) {
		writeFail(w, http.StatusForbidden, ErrForbidden)
		return
	}
	if !all && !user.Can(PermTokensCreate) {
		// tokens.manage alone: the tokens of everybody, not "my own".
		all = true
	}
	tokens, err := s.ListTokens(r.Context(), user, all)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (s *Service) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req TokenRequest
	if !readJSON(w, r, &req) {
		return
	}
	user, _ := UserFrom(r.Context())
	tok, secret, err := s.CreateToken(r.Context(), user, req)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		APIToken
		Token string `json:"token"`
	}{tok, secret})
}

func (s *Service) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, ErrTokenNotFound)
		return
	}
	user, _ := UserFrom(r.Context())
	if err := s.RevokeToken(r.Context(), user, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
