// Package storage opens the hub database and applies its migrations (embedded,
// applied in order and recorded). It supports SQLite, the default, through a
// pure Go driver, and PostgreSQL through pgx; the choice is made by the
// configuration. This is the one package that knows the differences between
// the two: the other packages receive a *sql.DB, write their queries with ?
// placeholders and own their own tables. For the hub it also makes sure one
// hub runs on a PostgreSQL database (OpenHub), and for tools beside the hub
// it opens a database without migrating it (OpenExisting). It does not move
// data from one backend to the other (#124), and it does not lock the data
// directory (the hub does).
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure Go driver, keeps the binary static (no cgo)
)

// FileName is the name of the SQLite database file inside the data directory.
const FileName = "regulus.db"

// How long Open keeps trying to reach PostgreSQL before it gives up. The hub
// then exits and the service manager starts it again, so a database that comes
// up a little after the hub (a reboot) does not need anybody.
const connectWait = 60 * time.Second

// Open opens the database named by cfg and applies all pending migrations. For
// SQLite the file is created if needed. The hub itself uses OpenHub.
func Open(ctx context.Context, cfg Config) (*sql.DB, error) {
	db, _, err := open(ctx, cfg, false)
	return db, err
}

// HubDB is the database as the running hub holds it.
type HubDB struct {
	// Write is the pool for everything that changes the database, and for
	// reads that must see the caller's own transaction.
	Write *sql.DB
	// Read is the pool for reads that need not see a transaction of the
	// caller's own. On SQLite it is a separate pool of read-only connections
	// (WAL lets them read while the one writer writes), so a request that
	// only checks a session does not queue behind a write or a slow read; on
	// PostgreSQL, whose pool already has several connections, it is Write.
	Read *sql.DB

	active *activeLock
}

// Close releases the lock of the active hub and closes the pools.
func (d *HubDB) Close() error {
	d.active.release()
	var err error
	if d.Read != d.Write {
		err = d.Read.Close()
	}
	return errors.Join(err, d.Write.Close())
}

// OpenHub opens the database for the hub: like Open, and on PostgreSQL it
// first makes this process the one active hub on the database (an advisory
// lock, see lockActiveHub), before any migration, so that a second hub, maybe
// a newer one, does not migrate the schema under the first. On SQLite that
// guarantee comes from the lock the hub holds on the data directory.
func OpenHub(ctx context.Context, cfg Config) (*HubDB, error) {
	db, lock, err := open(ctx, cfg, true)
	if err != nil {
		return nil, err
	}
	read := db
	if !cfg.IsPostgres() && cfg.ReadConns >= 0 {
		if read, err = openSQLiteReader(cfg); err != nil {
			lock.release()
			_ = db.Close()
			return nil, err
		}
	}
	return &HubDB{Write: db, Read: read, active: lock}, nil
}

func open(ctx context.Context, cfg Config, active bool) (*sql.DB, *activeLock, error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, nil, fmt.Errorf("create data directory: %w", err)
	}
	db, err := openPool(cfg)
	if err != nil {
		return nil, nil, err
	}
	if err := waitFor(ctx, db, cfg); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	if cfg.IsPostgres() {
		if _, err := verifySchema(ctx, db, cfg); err != nil {
			_ = db.Close()
			return nil, nil, err
		}
	}
	var lock *activeLock
	if active {
		if lock, err = lockActiveHub(ctx, db, cfg); err != nil {
			_ = db.Close()
			return nil, nil, err
		}
	}
	if err := Migrate(ctx, db, cfg.Type); err != nil {
		lock.release()
		_ = db.Close()
		return nil, nil, err
	}
	return db, lock, nil
}

// OpenSQLite opens the SQLite database in dataDir. It is shorthand for the
// default configuration.
func OpenSQLite(ctx context.Context, dataDir string) (*sql.DB, error) {
	return Open(ctx, Config{Type: TypeSQLite, DataDir: dataDir})
}

func openPool(cfg Config) (*sql.DB, error) {
	if cfg.IsPostgres() {
		return openPostgres(cfg)
	}
	db, err := sql.Open("sqlite", dsn(filepath.Join(cfg.DataDir, FileName)))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One connection writes, which avoids SQLITE_BUSY between writers; its
	// transactions start with BEGIN IMMEDIATE (_txlock), so one never has to
	// upgrade a read lock to a write lock half way. Reads that need not see a
	// transaction of their own go to the read pool of OpenHub, which WAL lets
	// run beside the writer.
	db.SetMaxOpenConns(1)
	return db, nil
}

// DefaultReadConns is the size of the SQLite read pool of OpenHub.
const DefaultReadConns = 4

// openSQLiteReader opens the read pool of a SQLite database that Open has
// already created and migrated. Its connections are query_only: a write sent
// to them by mistake fails instead of competing with the writer.
func openSQLiteReader(cfg Config) (*sql.DB, error) {
	path := filepath.Join(cfg.DataDir, FileName)
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "query_only(1)")
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Opaque: filepath.ToSlash(path), RawQuery: q.Encode()}).String())
	if err != nil {
		return nil, fmt.Errorf("open database for reading: %w", err)
	}
	n := cfg.ReadConns
	if n == 0 {
		n = DefaultReadConns
	}
	db.SetMaxOpenConns(n)
	db.SetMaxIdleConns(n)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database for reading: %w", err)
	}
	return db, nil
}

// waitFor pings the database. SQLite answers at once; PostgreSQL is retried
// for a while, because it may still be starting.
func waitFor(ctx context.Context, db *sql.DB, cfg Config) error {
	deadline := time.Now().Add(connectWait)
	delay := time.Second
	for {
		err := db.PingContext(ctx)
		if err == nil {
			return nil
		}
		if !cfg.IsPostgres() || ctx.Err() != nil || time.Now().Add(delay).After(deadline) {
			return fmt.Errorf("connect to database: %s", Explain(err))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("connect to database: %w", ctx.Err())
		case <-time.After(delay):
		}
		if delay < 8*time.Second {
			delay *= 2
		}
	}
}

func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Set("_txlock", "immediate")
	return (&url.URL{Scheme: "file", Opaque: filepath.ToSlash(path), RawQuery: q.Encode()}).String()
}

// OpenRaw opens a pool without pinging it and without migrating. Tests use it
// to prepare or clean a database.
func OpenRaw(cfg Config) (*sql.DB, error) {
	return openPool(cfg)
}
