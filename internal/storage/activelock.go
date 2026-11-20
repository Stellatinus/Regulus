package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrHubActive: another hub already uses the database.
var ErrHubActive = errors.New("another hub is already running on this database")

// lockActiveHub makes the caller the one hub that runs on the database. On
// PostgreSQL, where hubs on two machines could share a database, it takes a
// session advisory lock named after the schema, on a connection it keeps
// until release is called; a second hub gets ErrHubActive at once instead of
// running a second set of timers (automatic rollouts, update checks) on the
// same data. The lock lives as long as that connection: if PostgreSQL ends the
// session (a restart of the server, a network cut long enough to drop it), the
// lock is gone until the hub restarts. On SQLite it does nothing: the data
// directory is the database, and the hub locks that (see hub.New).
func lockActiveHub(ctx context.Context, db *sql.DB, cfg Config) (release func(), err error) {
	if !cfg.IsPostgres() {
		return func() {}, nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock the database for this hub: %s", Explain(err))
	}
	var got bool
	// current_schema() keeps hubs that use different schemas of one database
	// (database.schema) apart, and the tests too.
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext('regulus-active-hub:' || current_schema()))`).Scan(&got); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("lock the database for this hub: %s", Explain(err))
	}
	if !got {
		_ = conn.Close()
		return nil, fmt.Errorf("%w (%s): stop the other hub first; if it was stopped a moment ago, its database session may still be open, and the service manager retries", ErrHubActive, cfg.Describe())
	}
	return func() {
		// Closing the connection would release the lock too, but the pool may
		// keep it open; unlock explicitly, then hand the connection back.
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(uctx, `SELECT pg_advisory_unlock(hashtext('regulus-active-hub:' || current_schema()))`)
		_ = conn.Close()
	}, nil
}
