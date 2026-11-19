// Package fleet manages the remote agents of the hub: the single-use tokens
// that let a new agent enroll (made, listed and cancelled here), the registry of the agents that did, and the
// revocation of an agent. The certificates themselves come from package pki.
package fleet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/CodexCircle/Regulus/internal/pki"
)

const (
	// TokenTTL is how long an enrollment token can be used when the caller
	// does not choose.
	TokenTTL = time.Hour
	// MinTokenTTL and MaxTokenTTL bound the lifetime a caller can ask for.
	MinTokenTTL = time.Minute
	MaxTokenTTL = 7 * 24 * time.Hour

	maxNameLen  = 64
	maxFailures = 10
	maxTracked  = 10000
	failWindow  = 15 * time.Minute
)

// Audit events recorded by the fleet.
const (
	EventTokenCreated  = "enroll_token_created"
	EventAgentEnrolled = "agent_enrolled"
	EventEnrollFailed  = "agent_enroll_failed"
	EventTokenRevoked  = "enroll_token_revoked"
	EventAgentRevoked  = "agent_revoked"
	EventAgentRenamed  = "agent_renamed"
)

// Errors returned by the service.
var (
	// ErrInvalidToken is the one answer for a token that is unknown, expired or
	// already used.
	ErrInvalidToken = errors.New("this enrollment token is invalid or has expired")
	ErrRateLimited  = errors.New("too many failed attempts, try again later")
	ErrNotFound     = errors.New("agent not found")
	// ErrTokenNotFound is returned when no pending enrollment token has the id.
	ErrTokenNotFound = errors.New("enrollment token not found")
	// ErrRevoked is returned for an agent that was revoked, or whose
	// certificate is not the one the hub issued last.
	ErrRevoked = errors.New("this agent is revoked")
)

// Auditor records events; *auth.Service implements it.
type Auditor interface {
	Record(ctx context.Context, actor, event, target, detail string)
}

// Agent is an enrolled agent.
type Agent struct {
	ID         string
	Name       string
	Hostname   string
	Version    string
	OSVersion  string
	CreatedAt  time.Time
	EnrolledAt string    // the address it enrolled from
	LastSeen   time.Time // zero if it never connected
	Revoked    bool
	RevokedAt  time.Time
}

// Service is the registry of remote agents.
type Service struct {
	db    *sql.DB
	ca    *pki.CA
	audit Auditor
	now   func() time.Time

	mu       sync.Mutex
	failures map[string]*failure
}

type failure struct {
	count int
	since time.Time
}

// New returns the service. audit may be nil.
func New(db *sql.DB, ca *pki.CA, audit Auditor) *Service {
	return &Service{db: db, ca: ca, audit: audit, now: time.Now, failures: map[string]*failure{}}
}

// CA returns the certificate authority, for the listener.
func (s *Service) CA() *pki.CA { return s.ca }

const timeLayout = "2006-01-02T15:04:05.000000000Z"

func ts(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTS(v string) time.Time {
	t, _ := time.Parse(timeLayout, v)
	return t
}

func (s *Service) record(ctx context.Context, actor, event, target, detail string) {
	if s.audit != nil {
		s.audit.Record(ctx, actor, event, target, detail)
	}
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NameError is a problem with the name chosen for a server.
type NameError struct{ msg string }

func (e *NameError) Error() string { return e.msg }

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) > maxNameLen {
		return "", &NameError{fmt.Sprintf("the name can have at most %d characters", maxNameLen)}
	}
	for _, r := range name {
		if r < ' ' || r == 0x7f {
			return "", &NameError{"the name cannot contain control characters"}
		}
	}
	return name, nil
}

// TTLError is a problem with the lifetime asked for an enrollment token.
type TTLError struct{ msg string }

func (e *TTLError) Error() string { return e.msg }

// CreateToken makes a single-use enrollment token, valid for ttl (TokenTTL when
// ttl is zero, otherwise between MinTokenTTL and MaxTokenTTL). The name, if
// given, becomes the name of the server that enrolls with it. The token is
// returned once; only its hash is kept.
func (s *Service) CreateToken(ctx context.Context, by, name string, ttl time.Duration) (token string, expires time.Time, err error) {
	name, err = cleanName(name)
	if err != nil {
		return "", time.Time{}, err
	}
	if ttl == 0 {
		ttl = TokenTTL
	}
	if ttl < MinTokenTTL || ttl > MaxTokenTTL {
		return "", time.Time{}, &TTLError{fmt.Sprintf("the lifetime of a token must be between %v and %v", MinTokenTTL, MaxTokenTTL)}
	}
	token, err = randomToken(24)
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	expires = now.Add(ttl)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM enrollment_tokens WHERE expires_at <= ?`, ts(now)); err != nil {
		return "", time.Time{}, err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO enrollment_tokens (token_hash, name, created_at, expires_at, created_by) VALUES (?, ?, ?, ?, ?)`,
		hashToken(token), name, ts(now), ts(expires), by); err != nil {
		return "", time.Time{}, err
	}
	s.record(ctx, by, EventTokenCreated, name, "valid for "+ttl.String())
	return token, expires, nil
}

// idLen is how much of a token's hash names it in the API: enough to tell
// tokens apart, and no use for enrolling (the hash cannot be turned back into
// the token).
const idLen = 16

// PendingToken is an enrollment token that has not been used or expired. It
// never carries the secret: that was shown once, when it was made.
type PendingToken struct {
	ID        string
	Name      string
	CreatedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// PendingTokens lists the enrollment tokens that can still be used, oldest
// first.
func (s *Service) PendingTokens(ctx context.Context) ([]PendingToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT token_hash, name, created_by, created_at, expires_at FROM enrollment_tokens WHERE expires_at > ? ORDER BY created_at, token_hash`,
		ts(s.now()))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	list := []PendingToken{}
	for rows.Next() {
		var hash, created, expires string
		t := PendingToken{}
		if err := rows.Scan(&hash, &t.Name, &t.CreatedBy, &created, &expires); err != nil {
			return nil, err
		}
		t.ID, t.CreatedAt, t.ExpiresAt = hash[:idLen], parseTS(created), parseTS(expires)
		list = append(list, t)
	}
	return list, rows.Err()
}

// RevokeToken cancels a pending enrollment token, for one that was pasted
// where it should not have been or is no longer needed. Enrolling with it
// fails from then on.
func (s *Service) RevokeToken(ctx context.Context, by, id string) error {
	if len(id) != idLen || strings.Trim(id, "0123456789abcdef") != "" {
		return ErrTokenNotFound
	}
	var name string
	err := s.db.QueryRowContext(ctx,
		`DELETE FROM enrollment_tokens WHERE substr(token_hash, 1, ?) = ? AND expires_at > ? RETURNING name`,
		idLen, id, ts(s.now())).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTokenNotFound
	}
	if err != nil {
		return err
	}
	s.record(ctx, by, EventTokenRevoked, name, "token "+id)
	return nil
}

// Enrolled is the result of a successful enrollment.
type Enrolled struct {
	AgentID          string
	CertificateDER   []byte
	CACertificateDER []byte
}

// Enroll registers a new agent: it signs the agent's certificate request,
// then, in one transaction, uses the token up and records the agent. A request
// that fails (a malformed certificate request, a signing or database error)
// leaves the token usable, so the admin does not have to make another. remote
// is the address of the agent, for the limit on failed attempts and the audit
// log.
func (s *Service) Enroll(ctx context.Context, remote, token string, csrDER []byte, hostname string) (Enrolled, error) {
	if s.limited(remote) {
		return Enrolled{}, ErrRateLimited
	}
	hostname = strings.TrimSpace(hostname)
	if len(hostname) > 255 {
		hostname = hostname[:255]
	}
	invalid := func() (Enrolled, error) {
		s.fail(remote)
		s.record(ctx, "", EventEnrollFailed, "", "invalid token")
		return Enrolled{}, ErrInvalidToken
	}

	now := s.now()
	hash := hashToken(token)
	var name string
	err := s.db.QueryRowContext(ctx,
		`SELECT name FROM enrollment_tokens WHERE token_hash = ? AND expires_at > ?`, hash, ts(now)).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return invalid()
	}
	if err != nil {
		return Enrolled{}, err
	}

	id, err := newAgentID()
	if err != nil {
		return Enrolled{}, err
	}
	certDER, serial, err := s.ca.SignClient(csrDER, id, now)
	if err != nil {
		return Enrolled{}, &RequestError{err}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Enrolled{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// Using the token up is what makes it single-use: of two requests that
	// passed the check above with the same token, only one deletes the row.
	res, err := tx.ExecContext(ctx, `DELETE FROM enrollment_tokens WHERE token_hash = ? AND expires_at > ?`, hash, ts(now))
	if err != nil {
		return Enrolled{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Enrolled{}, err
	} else if n != 1 {
		return invalid()
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO agents (id, name, hostname, created_at, enrolled_from, cert_serial) VALUES (?, ?, ?, ?, ?, ?)`,
		id, name, hostname, ts(now), remote, serial); err != nil {
		return Enrolled{}, err
	}
	if err := tx.Commit(); err != nil {
		return Enrolled{}, err
	}
	s.clear(remote)
	s.record(ctx, "", EventAgentEnrolled, id, nameOr(name, hostname))
	return Enrolled{AgentID: id, CertificateDER: certDER, CACertificateDER: s.ca.CertDER()}, nil
}

// RequestError is a problem with the certificate request of an agent.
type RequestError struct{ Err error }

func (e *RequestError) Error() string { return "invalid certificate request: " + e.Err.Error() }
func (e *RequestError) Unwrap() error { return e.Err }

func nameOr(name, fallback string) string {
	if name != "" {
		return name
	}
	return fallback
}

// Authorize checks that the agent may connect with the certificate that has
// this serial number: the agent exists, is not revoked, and the certificate is
// the one the hub issued for it.
func (s *Service) Authorize(ctx context.Context, id, serial string) error {
	var (
		want    string
		revoked sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `SELECT cert_serial, revoked_at FROM agents WHERE id = ?`, id).Scan(&want, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if revoked.Valid || want != serial {
		return ErrRevoked
	}
	return nil
}

// Revoke makes the agent unable to connect again. It does not close a
// connection that is open: the hub does that.
func (s *Service) Revoke(ctx context.Context, by, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agents SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, ts(s.now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var one int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM agents WHERE id = ?`, id).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err // nil: it was revoked already
	}
	s.record(ctx, by, EventAgentRevoked, id, "")
	return nil
}

// Agents lists the enrolled agents, oldest first.
func (s *Service) Agents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, hostname, version, os_version, created_at, enrolled_from, last_seen_at, revoked_at FROM agents ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	list := []Agent{}
	for rows.Next() {
		var (
			a       Agent
			created string
			seen    sql.NullString
			revoked sql.NullString
		)
		if err := rows.Scan(&a.ID, &a.Name, &a.Hostname, &a.Version, &a.OSVersion, &created, &a.EnrolledAt, &seen, &revoked); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTS(created)
		if seen.Valid {
			a.LastSeen = parseTS(seen.String)
		}
		if revoked.Valid {
			a.Revoked, a.RevokedAt = true, parseTS(revoked.String)
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// Seen records that the agent is connected, or was just now, with what it
// reported about itself. An id that is not an enrolled agent (the built-in
// agent) is ignored.
func (s *Service) Seen(ctx context.Context, id, version, hostname, osVersion string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET last_seen_at = ?, version = ?, hostname = CASE WHEN ? <> '' THEN ? ELSE hostname END,
		 os_version = CASE WHEN ? <> '' THEN ? ELSE os_version END WHERE id = ?`,
		ts(at), version, hostname, hostname, osVersion, osVersion, id)
	return err
}

// Rename sets the name shown for an agent; an empty name goes back to the
// host name.
func (s *Service) Rename(ctx context.Context, by, id, name string) error {
	name, err := cleanName(name)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.record(ctx, by, EventAgentRenamed, id, name)
	return nil
}

func (s *Service) limited(remote string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.failures[remote]
	if f == nil {
		return false
	}
	if s.now().Sub(f.since) > failWindow {
		delete(s.failures, remote)
		return false
	}
	return f.count >= maxFailures
}

func (s *Service) fail(remote string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if len(s.failures) > maxTracked { // addresses come from outside, so keep the map bounded
		for k, f := range s.failures {
			if now.Sub(f.since) > failWindow {
				delete(s.failures, k)
			}
		}
	}
	f := s.failures[remote]
	if f == nil || now.Sub(f.since) > failWindow {
		f = &failure{since: now}
		s.failures[remote] = f
	}
	f.count++
}

func (s *Service) clear(remote string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, remote)
}

func newAgentID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "agent-" + hex.EncodeToString(b), nil
}
