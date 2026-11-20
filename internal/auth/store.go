package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Role is the name of a built-in or custom role (see permissions.go).
type Role string

// User is a local account.
type User struct {
	ID        int64     `json:"-"`
	Username  string    `json:"username"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	// Disabled accounts cannot log in and have no sessions.
	Disabled bool `json:"disabled"`
	// TwoFactor is true when the user confirmed a second factor (TOTP).
	TwoFactor bool `json:"two_factor"`
	// Permissions are what the role allows. Only users that come from a
	// session carry them.
	Permissions []string `json:"permissions,omitempty"`
}

// hasTwoFactor is the SQL expression for User.TwoFactor, for a query on users.
const hasTwoFactor = `EXISTS (SELECT 1 FROM user_totp t WHERE t.user_id = users.id AND t.confirmed_at IS NOT NULL)`

// Errors returned by the store and the service.
var (
	ErrSetupDone          = errors.New("setup was already completed")
	ErrBadSetupToken      = errors.New("invalid setup token")
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrRateLimited        = errors.New("too many failed attempts, try again later")
	ErrUnauthenticated    = errors.New("authentication required")
	ErrForbidden          = errors.New("you are not allowed to do this")
	ErrUserNotFound       = errors.New("user not found")
	ErrUserExists         = errors.New("a user with this name already exists")
	ErrLastAdmin          = errors.New("this would leave the hub without an enabled admin")
	ErrWrongPassword      = errors.New("the current password is incorrect")
	ErrSessionNotFound    = errors.New("session not found")
)

// timeLayout is a fixed-width UTC format, so timestamps sort correctly as text
// (time.RFC3339Nano drops trailing zeros, which breaks the order).
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// store reads and writes the tables of this package. db is the pool that
// writes; rd, if set, is a pool for reads that need not see a transaction of
// their own (storage.HubDB.Read), so that checking a session does not wait
// behind a write.
type store struct {
	db *sql.DB
	rd *sql.DB
}

// read returns the pool for plain reads.
func (s store) read() *sql.DB {
	if s.rd != nil {
		return s.rd
	}
	return s.db
}

func (s store) countUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// createFirstAdmin inserts the admin only if there is no user yet, in a single
// statement, so two concurrent setups cannot both succeed.
func (s store) createFirstAdmin(ctx context.Context, username, hash string, now time.Time) (User, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO users (username, password_hash, role, created_at)
		 SELECT CAST(? AS TEXT), CAST(? AS TEXT), 'admin', CAST(? AS TEXT) WHERE NOT EXISTS (SELECT 1 FROM users)
		 RETURNING id`,
		username, hash, now.UTC().Format(timeLayout)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrSetupDone
	}
	if err != nil {
		return User{}, fmt.Errorf("create admin: %w", err)
	}
	return User{ID: id, Username: username, Role: RoleAdmin, CreatedAt: now.UTC()}, nil
}

// userByName returns the user and the stored password hash. The error is
// sql.ErrNoRows for an unknown user.
func (s store) userByName(ctx context.Context, username string) (User, string, error) {
	var (
		u       User
		hash    string
		created string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, role, created_at, disabled, `+hasTwoFactor+`, password_hash FROM users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &u.Role, &created, &u.Disabled, &u.TwoFactor, &hash)
	if err != nil {
		return User{}, "", err
	}
	u.CreatedAt, _ = time.Parse(timeLayout, created)
	return u, hash, nil
}

func (s store) createSession(ctx context.Context, tokenHash string, userID int64, now, expires time.Time, c Client) error {
	at := now.UTC().Format(timeLayout)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, remote_addr, user_agent, last_used_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tokenHash, userID, at, expires.UTC().Format(timeLayout), c.Remote, c.UserAgent, at)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// sessionUser returns the user of a session that has not expired, and when
// the session was last used.
func (s store) sessionUser(ctx context.Context, tokenHash string, now time.Time) (User, time.Time, error) {
	var (
		u                          User
		created, expires, lastUsed string
	)
	err := s.read().QueryRowContext(ctx,
		`SELECT users.id, users.username, users.role, users.created_at, s.expires_at, s.last_used_at, `+hasTwoFactor+`
		 FROM sessions s JOIN users ON users.id = s.user_id WHERE s.token_hash = ? AND users.disabled = 0`, tokenHash).
		Scan(&u.ID, &u.Username, &u.Role, &created, &expires, &lastUsed, &u.TwoFactor)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, time.Time{}, ErrUnauthenticated
	}
	if err != nil {
		return User{}, time.Time{}, err
	}
	exp, err := time.Parse(timeLayout, expires)
	if err != nil || !now.Before(exp) {
		return User{}, time.Time{}, ErrUnauthenticated
	}
	u.CreatedAt, _ = time.Parse(timeLayout, created)
	used, _ := time.Parse(timeLayout, lastUsed)
	return u, used, nil
}

func (s store) deleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s store) deleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.UTC().Format(timeLayout))
	return err
}
