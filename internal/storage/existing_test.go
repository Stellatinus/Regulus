package storage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
