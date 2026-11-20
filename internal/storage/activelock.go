package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrHubActive: another hub already uses the database.
var ErrHubActive = errors.New("another hub is already running on this database")

// The key of the active-hub lock: one per schema, so that hubs that use
// different schemas of one database (database.schema) stay apart, and so do
// the tests.
const activeKeySQL = `hashtext('regulus-active-hub:' || current_schema())`

// activeLock is the lock that makes one process the hub that runs on a
// PostgreSQL database. It is a session advisory lock, so it lives as long as
// the database session of the connection that holds it: if PostgreSQL ends
// that session (a restart of the server, a pg_terminate_backend, a network cut
// long enough to drop it) the lock is gone. ensure notices that and takes it
// again (#344). The zero value, and nil, hold nothing (SQLite).
type activeLock struct {
	db   *sql.DB
	desc string // the database, for messages

	mu   sync.Mutex
	conn *sql.Conn // nil after release
	pid  int       // the PostgreSQL backend of conn, to find our own stale session
}

// lockActiveHub makes the caller the one hub that runs on the database. On
// PostgreSQL, where hubs on two machines could share a database, it takes the
// lock on a connection it keeps until release is called; a second hub gets
// ErrHubActive at once instead of running a second set of timers (automatic
// rollouts, update checks) on the same data. On SQLite it does nothing: the
// data directory is the database, and the hub locks that (see hub.New).
func lockActiveHub(ctx context.Context, db *sql.DB, cfg Config) (*activeLock, error) {
	l := &activeLock{db: db, desc: cfg.Describe()}
	if !cfg.IsPostgres() {
		return l, nil
	}
	conn, pid, got, err := l.take(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock the database for this hub: %s", Explain(err))
	}
	if !got {
		return nil, fmt.Errorf("%w (%s): stop the other hub first; if it was stopped a moment ago, its database session may still be open, and the service manager retries", ErrHubActive, l.desc)
	}
	l.conn, l.pid = conn, pid
	return l, nil
}

// take opens a connection and tries to take the lock on it. On success the
// connection is the caller's to keep; otherwise it is already closed.
func (l *activeLock) take(ctx context.Context) (conn *sql.Conn, pid int, got bool, err error) {
	conn, err = l.db.Conn(ctx)
	if err != nil {
		return nil, 0, false, err
	}
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(`+activeKeySQL+`), pg_backend_pid()`).Scan(&got, &pid); err != nil {
		_ = conn.Close()
		return nil, 0, false, err
	}
	if !got {
		_ = conn.Close()
		return nil, 0, false, nil
	}
	return conn, pid, true, nil
}

// holds reports whether the session of conn holds the lock. It reads pg_locks:
// a lock taken with a single bigint key shows its high half as classid, its
// low half as objid and objsubid 1.
func (l *activeLock) holds(ctx context.Context, conn *sql.Conn) (bool, error) {
	var held bool
	err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks
		WHERE locktype = 'advisory' AND objsubid = 1 AND granted AND pid = pg_backend_pid()
		AND ((classid::bigint << 32) | objid::bigint) = `+activeKeySQL+`::bigint)`).Scan(&held)
	return held, err
}

// ensure checks that this process still holds the lock and, if the session was
// lost, takes it again. retaken says that it had to. It returns ErrHubActive
// when another hub holds the lock now, and any other error when the database
// cannot be asked (the caller's own health check reports that). On SQLite, or
// after release, it does nothing.
func (l *activeLock) ensure(ctx context.Context) (retaken bool, err error) {
	if l == nil {
		return false, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return false, nil
	}
	if held, err := l.holds(ctx, l.conn); err == nil && held {
		return false, nil
	} else if err == nil {
		// The session is alive but the lock is gone: take it on the same one.
		var got bool
		if err := l.conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(`+activeKeySQL+`)`).Scan(&got); err == nil && got {
			return true, nil
		}
	}
	// The session is gone (or unusable). Drop its connection and start over.
	_ = l.conn.Close()
	l.conn = nil
	oldPID := l.pid

	conn, pid, got, err := l.take(ctx)
	if err != nil {
		return false, err
	}
	if !got && l.endStaleSession(ctx, oldPID) {
		conn, pid, got, err = l.take(ctx)
		if err != nil {
			return false, err
		}
	}
	if !got {
		return false, fmt.Errorf("%w (%s)", ErrHubActive, l.desc)
	}
	l.conn, l.pid = conn, pid
	return true, nil
}

// endStaleSession handles a cut network: the hub saw its connection fail, but
// PostgreSQL has not noticed yet, so the old session still holds the lock and
// would make the hub take another hub's place for itself. It ends the old
// backend, but only if that backend holds this very lock, then waits a moment
// for the lock to go. It reports whether the lock was freed.
func (l *activeLock) endStaleSession(ctx context.Context, pid int) bool {
	if pid == 0 {
		return false
	}
	var ended bool
	if err := l.db.QueryRowContext(ctx, `SELECT COALESCE(bool_or(pg_terminate_backend(pid)), false) FROM pg_locks
		WHERE locktype = 'advisory' AND objsubid = 1 AND granted AND pid = $1
		AND ((classid::bigint << 32) | objid::bigint) = `+activeKeySQL+`::bigint`, pid).Scan(&ended); err != nil || !ended {
		return false
	}
	for range 20 {
		var held bool
		if err := l.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype = 'advisory' AND pid = $1)`, pid).Scan(&held); err == nil && !held {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
	return false
}

// release gives the lock up. Closing the connection would release it too, but
// the pool may keep it open; unlock explicitly, then hand the connection back.
func (l *activeLock) release() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return
	}
	uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = l.conn.ExecContext(uctx, `SELECT pg_advisory_unlock(`+activeKeySQL+`)`)
	_ = l.conn.Close()
	l.conn = nil
}

// EnsureActive checks that this hub still holds the active-hub lock of a
// PostgreSQL database and takes it again if the database session that held it
// was lost (#344). retaken says that it had to. It returns ErrHubActive when
// another hub holds the lock now: this hub must then stop acting as the active
// one. On SQLite it always returns nil.
func (d *HubDB) EnsureActive(ctx context.Context) (retaken bool, err error) {
	return d.active.ensure(ctx)
}
