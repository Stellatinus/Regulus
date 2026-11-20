package storage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/storage"
	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

// OpenExisting is what reset-admin uses: it must never migrate, and it must
// refuse a schema that is not exactly this binary's.
func TestOpenExistingNeverMigrates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := storagetest.Config(t, dir)

	if !cfg.IsPostgres() {
		if _, err := storage.OpenExisting(ctx, cfg); !errors.Is(err, storage.ErrNoDatabase) {
			t.Fatalf("missing database: error = %v, want ErrNoDatabase", err)
		}
		if _, err := os.Stat(filepath.Join(dir, storage.FileName)); err == nil {
			t.Fatal("OpenExisting created a database file")
		}
	} else if _, err := storage.OpenExisting(ctx, cfg); !errors.Is(err, storage.ErrNoDatabase) {
		t.Fatalf("empty schema: error = %v, want ErrNoDatabase", err)
	}

	db, err := storage.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	got, err := storage.OpenExisting(ctx, cfg)
	if err != nil {
		t.Fatalf("a database at this version: %v", err)
	}
	_ = got.Close()

	// One migration short: what a database looks like after a package upgrade,
	// before the hub restarts.
	var last int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, last); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.OpenExisting(ctx, cfg); !errors.Is(err, storage.ErrSchemaOlder) {
		t.Fatalf("older schema: error = %v, want ErrSchemaOlder", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, last).Scan(&n); err != nil || n != 0 {
		t.Fatalf("migration %d was applied again (count %d, err %v): OpenExisting must not migrate", last, n, err)
	}

	if _, err := db.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, 'x'), (9999, 'from_the_future')`, last); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.OpenExisting(ctx, cfg); !errors.Is(err, storage.ErrSchemaNewer) {
		t.Fatalf("newer schema: error = %v, want ErrSchemaNewer", err)
	}
}

// On PostgreSQL two hubs could share one database from two machines; the
// second must not start (and must not migrate).
func TestOpenHubOnPostgresAdmitsOneHub(t *testing.T) {
	if !storagetest.Postgres() {
		t.Skip("PostgreSQL only: on SQLite the hub locks its data directory")
	}
	ctx := context.Background()
	cfg := storagetest.Config(t, t.TempDir())
	first, err := storage.OpenHub(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := storage.OpenHub(ctx, cfg); !errors.Is(err, storage.ErrHubActive) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second hub: error = %v, want ErrHubActive", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := storage.OpenHub(ctx, cfg)
	if err != nil {
		t.Fatalf("after the first hub closed: %v", err)
	}
	_ = again.Close()
}

// terminateActiveHubSession ends the PostgreSQL session that holds the
// active-hub lock of cfg's schema, as pg_terminate_backend or a server restart
// would, and returns once the lock is free.
func terminateActiveHubSession(t *testing.T, db *storage.HubDB) {
	t.Helper()
	ctx := context.Background()
	var n int
	if err := db.Write.QueryRowContext(ctx, `SELECT count(pg_terminate_backend(pid)) FROM pg_locks
		WHERE locktype = 'advisory' AND objsubid = 1 AND granted AND pid <> pg_backend_pid()
		AND ((classid::bigint << 32) | objid::bigint) = hashtext('regulus-active-hub:' || current_schema())::bigint`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("ending the session of the lock: %d sessions, err %v", n, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var held bool
		if err := db.Write.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype = 'advisory' AND objsubid = 1
			AND ((classid::bigint << 32) | objid::bigint) = hashtext('regulus-active-hub:' || current_schema())::bigint)`).Scan(&held); err != nil {
			t.Fatal(err)
		}
		if !held {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the lock was not released after its session ended")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestActiveHubLockIsTakenAgainAfterTheSessionIsLost is #344: PostgreSQL ends
// the session that holds the lock; the hub notices, takes the lock again, and
// a second hub is still refused.
func TestActiveHubLockIsTakenAgainAfterTheSessionIsLost(t *testing.T) {
	if !storagetest.Postgres() {
		t.Skip("PostgreSQL only: the active-hub lock does not exist on SQLite")
	}
	ctx := context.Background()
	cfg := storagetest.Config(t, t.TempDir())
	hub, err := storage.OpenHub(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hub.Close() }()

	if retaken, err := hub.EnsureActive(ctx); err != nil || retaken {
		t.Fatalf("healthy: retaken = %v, err = %v; want nothing to do", retaken, err)
	}

	terminateActiveHubSession(t, hub)
	// Without the check, a second hub could now start.
	if retaken, err := hub.EnsureActive(ctx); err != nil || !retaken {
		t.Fatalf("after the session ended: retaken = %v, err = %v; want the lock taken again", retaken, err)
	}
	if second, err := storage.OpenHub(ctx, cfg); !errors.Is(err, storage.ErrHubActive) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second hub after the lock was retaken: error = %v, want ErrHubActive", err)
	}
	if retaken, err := hub.EnsureActive(ctx); err != nil || retaken {
		t.Fatalf("again: retaken = %v, err = %v; want nothing to do", retaken, err)
	}
}

// If another hub took the lock in the gap, this one must learn it and not
// pretend to be the active one.
func TestActiveHubLockTakenByAnotherHubIsReported(t *testing.T) {
	if !storagetest.Postgres() {
		t.Skip("PostgreSQL only: the active-hub lock does not exist on SQLite")
	}
	ctx := context.Background()
	cfg := storagetest.Config(t, t.TempDir())
	first, err := storage.OpenHub(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	terminateActiveHubSession(t, first)
	second, err := storage.OpenHub(ctx, cfg)
	if err != nil {
		t.Fatalf("the second hub should start once the lock is free: %v", err)
	}
	defer func() { _ = second.Close() }()

	if _, err := first.EnsureActive(ctx); !errors.Is(err, storage.ErrHubActive) {
		t.Fatalf("first hub: error = %v, want ErrHubActive", err)
	}
	if retaken, err := second.EnsureActive(ctx); err != nil || retaken {
		t.Fatalf("second hub: retaken = %v, err = %v; it holds the lock and has nothing to do", retaken, err)
	}
}
