package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/storage"
	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

// TestPreviousReleaseServesOnTheCurrentSchema runs the *program* of the
// previous published release on a database that the current source has
// migrated (#383, left over from #247). TestSchemaRangeAcceptsExpandOnlyMigrations
// in internal/storage runs the previous release's migrations; this runs its
// binary, the way a rollback does:
//
//  1. the old release starts on an empty database (its own schema);
//  2. the hub built from this source starts on it, migrates it, and the first
//     account is created;
//  3. the old release starts again on the migrated database. If every
//     migration since the old release is expand-only (reader_min not above the
//     old release's schema version) it must serve: /healthz answers and the
//     account can sign in. If one is strict it must refuse with "created by a
//     newer version": the test says which migration, and checks the refusal,
//     because a rollback past a strict migration needs the backup.
//
// It runs on SQLite, and on PostgreSQL when REGULUS_TEST_DATABASE_URL names a
// database (make e2e does not set it; CI does). It skips, with the reason,
// when there is no release to download, like TestAgentOfAnOlderRelease.
func TestPreviousReleaseServesOnTheCurrentSchema(t *testing.T) {
	if os.Getenv("REGULUS_E2E") != "1" {
		t.Skip("set REGULUS_E2E=1 to run the end-to-end test (make e2e)")
	}
	old, version := olderRelease(t)
	current := binary(t)
	t.Logf("previous release %s, %s", version, old)

	t.Run("sqlite", func(t *testing.T) {
		dir := t.TempDir()
		runSchemaRoundTrip(t, old, current, dir, storage.Config{Type: storage.TypeSQLite, DataDir: filepath.Join(dir, "hub")}, nil)
	})
	t.Run("postgres", func(t *testing.T) {
		if !storagetest.Postgres() {
			t.Skip("set " + storagetest.EnvURL + " to a PostgreSQL database to run this on PostgreSQL")
		}
		dir := t.TempDir()
		cfg := storagetest.Config(t, dir) // creates the schema, drops it at the end
		var env []string
		for k, v := range storagetest.Env(t, dir) {
			env = append(env, k+"="+v)
		}
		runSchemaRoundTrip(t, old, current, dir, cfg, env)
	})
}

func runSchemaRoundTrip(t *testing.T, old, current, dir string, cfg storage.Config, env []string) {
	t.Helper()
	hubArgs := func() []string {
		return []string{"hub", "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:" + freePort(t),
			"--data-dir", filepath.Join(dir, "hub"), "--builtin-agent=false", "--metrics-interval", "1s"}
	}

	// 1. The old release on an empty database.
	h1 := startHubEnv(t, old, filepath.Join(dir, "old-1.log"), env, hubArgs()...)
	h1.stop(t)
	oldSchema := schemaVersion(t, cfg)
	t.Logf("the old release's schema version is %d", oldSchema)

	// 2. This source migrates it and creates the first account.
	h2 := startHubEnv(t, current, filepath.Join(dir, "current.log"), env, hubArgs()...)
	api := newAPI(t, h2.httpAddr)
	api.setup(h2.setupToken)
	h2.stop(t)

	var strict []int
	newest := 0
	for _, m := range migrationsAbove(t, cfg, oldSchema) {
		newest = max(newest, m.version)
		if m.readerMin > oldSchema {
			strict = append(strict, m.version)
		}
	}
	t.Logf("the current source added migrations up to %d; strict for the old release: %v", newest, strict)

	// 3. The old release again, on the migrated database.
	if len(strict) > 0 {
		p := startEnv(t, old, filepath.Join(dir, "old-2.log"), env, hubArgs()...)
		if code := p.wait(t, time.Minute); code == 0 {
			t.Fatalf("the old release started on a database with the strict migrations %v: the range does not hold", strict)
		}
		if !strings.Contains(p.log(), "newer version") {
			t.Fatalf("the old release refused the database, but not as 'created by a newer version':\n%s", p.log())
		}
		t.Logf("the old release refuses the database, as expected: migrations %v are strict (a rollback past them needs the backup)", strict)
		return
	}
	h3 := startHubEnv(t, old, filepath.Join(dir, "old-2.log"), env, hubArgs()...)
	api3 := newAPI(t, h3.httpAddr)
	api3.do("GET", "/healthz", nil, 200)
	api3.login() // the account made by the current source, read by the old program
	h3.stop(t)
}

type appliedMigration struct{ version, readerMin int }

func schemaVersion(t *testing.T, cfg storage.Config) int {
	t.Helper()
	db, err := storage.OpenRaw(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var v int
	if err := db.QueryRowContext(context.Background(), `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatalf("read the schema version: %v", err)
	}
	return v
}

// migrationsAbove lists the migrations of the database with a version above
// from, and what version of the program each needs to be read.
func migrationsAbove(t *testing.T, cfg storage.Config, from int) []appliedMigration {
	t.Helper()
	db, err := storage.OpenRaw(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	// reader_min exists once the first release with the range has run; the
	// current source adds it.
	rows, err := db.QueryContext(context.Background(), `SELECT version, reader_min FROM schema_migrations WHERE version > ? ORDER BY version`, from)
	if err != nil {
		t.Fatalf("read the migrations: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []appliedMigration
	for rows.Next() {
		var m appliedMigration
		if err := rows.Scan(&m.version, &m.readerMin); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
