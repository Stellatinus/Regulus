package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// Each backend has its own directory: the two SQL dialects differ in types,
// defaults and how a table is rebuilt. Versions and names match, and a test
// compares the schemas they produce.
//
//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrationFiles embed.FS

type migration struct {
	version int
	name    string
	sql     string
	// readerMin is the lowest migration version a binary must know to keep
	// working on a database that has this migration applied (#247). It is the
	// migration's own version unless the file declares otherwise with
	// readerMinDirective: that is the strict default, and a release that
	// only expands the schema lowers it.
	readerMin int
}

// readerMinDirective, on a line of its own in the leading comments of a
// migration file, declares an expand-only migration: `-- regulus:reader-min 15`
// says a binary that knows migrations up to 15 still works on a database that
// has this one. See docs/updates.md ("Rollback and the schema range").
const readerMinDirective = "-- regulus:reader-min "

// Migrate applies, in order and each in its own transaction, the embedded
// migrations that are not yet recorded in schema_migrations. It refuses to
// run on a database that has a migration this binary does not know and that
// needs a newer binary (see readerMin); a database whose unknown migrations
// are all expand-only is accepted, so a rolled-back release still starts.
//
// typ is TypeSQLite (also when empty) or TypePostgres. On PostgreSQL the whole
// run holds an advisory lock, so two hubs that start together on one database
// do not migrate it at the same time: the second waits, then finds nothing to do.
func Migrate(ctx context.Context, db *sql.DB, typ string) error {
	d := dialects[TypeSQLite]
	if typ == TypePostgres {
		d = dialects[TypePostgres]
	}
	migrations, err := loadMigrations(migrationFiles, d.dir)
	if err != nil {
		return err
	}
	return apply(ctx, db, d, migrations)
}

// dialect holds what differs between the backends in the migration runner.
type dialect struct {
	dir              string
	createMigrations string
	postgres         bool
}

var dialects = map[string]dialect{
	TypeSQLite: {
		dir: "migrations/sqlite",
		createMigrations: `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
	) STRICT`,
	},
	TypePostgres: {
		dir:      "migrations/postgres",
		postgres: true,
		createMigrations: `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    BIGINT PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	)`,
	},
}

func loadMigrations(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var list []migration
	for _, e := range entries {
		file := e.Name()
		prefix, rest, ok := strings.Cut(strings.TrimSuffix(file, ".sql"), "_")
		version, convErr := strconv.Atoi(prefix)
		if !ok || convErr != nil || version <= 0 {
			return nil, fmt.Errorf("migration %q: name must look like 0001_description.sql", file)
		}
		body, err := fs.ReadFile(fsys, dir+"/"+file)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", file, err)
		}
		readerMin, err := parseReaderMin(file, string(body), version)
		if err != nil {
			return nil, err
		}
		list = append(list, migration{version: version, name: rest, sql: string(body), readerMin: readerMin})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].version < list[j].version })
	for i := 1; i < len(list); i++ {
		if list[i].version == list[i-1].version {
			return nil, fmt.Errorf("duplicate migration version %d", list[i].version)
		}
	}
	return list, nil
}

// parseReaderMin reads readerMinDirective from the leading comment lines of a
// migration. The value must be a version below the migration's own: a larger
// one would claim the migration needs a newer binary than itself.
func parseReaderMin(file, body string, version int) (int, error) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "--") {
			break
		}
		if v, ok := strings.CutPrefix(line, readerMinDirective); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n <= 0 || n >= version {
				return 0, fmt.Errorf("migration %q: %s must be a version between 1 and %d", file, strings.TrimSpace(readerMinDirective), version-1)
			}
			return n, nil
		}
	}
	return version, nil
}

func apply(ctx context.Context, db *sql.DB, d dialect, migrations []migration) error {
	if d.postgres {
		unlock, err := lockPostgres(ctx, db)
		if err != nil {
			return err
		}
		defer unlock()
	}
	if _, err := db.ExecContext(ctx, d.createMigrations); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	if err := ensureReaderMin(ctx, db, d); err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, db, d)
	if err != nil {
		return err
	}
	if err := refuseUnknown(applied, migrations); err != nil {
		return err
	}

	for _, m := range migrations {
		if _, done := applied[m.version]; done {
			continue
		}
		if err := applyOne(ctx, db, d, m); err != nil {
			return fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
		}
	}
	return nil
}

// ensureReaderMin adds the reader_min column to schema_migrations when the
// table predates it. It is not a numbered migration because it belongs to the
// runner itself, which has to read it before it can judge any migration.
func ensureReaderMin(ctx context.Context, db *sql.DB, d dialect) error {
	if d.postgres {
		_, err := db.ExecContext(ctx, `ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS reader_min BIGINT NOT NULL DEFAULT 0`)
		if err != nil {
			return fmt.Errorf("add schema_migrations.reader_min: %w", err)
		}
		return nil
	}
	has, err := hasReaderMin(ctx, db, d)
	if err != nil || has {
		return err
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE schema_migrations ADD COLUMN reader_min INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("add schema_migrations.reader_min: %w", err)
	}
	return nil
}

func hasReaderMin(ctx context.Context, db *sql.DB, d dialect) (bool, error) {
	q := `SELECT COUNT(*) FROM pragma_table_info('schema_migrations') WHERE name = 'reader_min'`
	if d.postgres {
		q = `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'schema_migrations' AND column_name = 'reader_min'`
	}
	var n int
	if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return false, fmt.Errorf("read schema_migrations: %w", err)
	}
	return n > 0, nil
}

// appliedVersions reads the versions recorded in schema_migrations, each with
// the reader_min the migration was recorded with (0 for a row written before
// the column existed, or by a release that did not know it: the migration's
// own version then).
func appliedVersions(ctx context.Context, db *sql.DB, d dialect) (map[int]int, error) {
	has, err := hasReaderMin(ctx, db, d)
	if err != nil {
		return nil, err
	}
	q := `SELECT version, 0 FROM schema_migrations`
	if has {
		q = `SELECT version, reader_min FROM schema_migrations`
	}
	applied := map[int]int{}
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v, min int
		if err := rows.Scan(&v, &min); err != nil {
			return nil, fmt.Errorf("read schema_migrations: %w", err)
		}
		if min <= 0 || min > v {
			min = v
		}
		applied[v] = min
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	return applied, nil
}

// refuseUnknown fails when the database has a migration this binary does not
// know AND that migration needs a binary that knows more than this one does.
// A migration that declares reader_min at or below the newest migration this
// binary knows (an expand-only one) is tolerated: this binary, started again
// after a rollback, works on the newer schema (#247).
func refuseUnknown(applied map[int]int, migrations []migration) error {
	known := map[int]bool{}
	newest := 0
	for _, m := range migrations {
		known[m.version] = true
		newest = max(newest, m.version)
	}
	for v, readerMin := range applied {
		if !known[v] && readerMin > newest {
			return fmt.Errorf("database has migration %d that this version of Regulus does not know (it needs a version that knows migration %d, this one knows up to %d): %w", v, readerMin, newest, ErrSchemaNewer)
		}
	}
	return nil
}

// lockPostgres takes a session advisory lock for the database, on a
// connection of its own, and returns the function that releases it.
func lockPostgres(ctx context.Context, db *sql.DB) (func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock database for migration: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext('regulus-migrate'))`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("lock database for migration: %w", err)
	}
	return func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('regulus-migrate'))`)
		_ = conn.Close()
	}, nil
}

// noForeignKeys marks a migration that rebuilds a table other tables refer to.
// SQLite cannot change a CHECK constraint in place, so such a migration creates
// the new table, copies the rows, drops the old one and renames the new one;
// with foreign keys on, dropping the old table would delete the rows that
// refer to it. The pragma cannot change inside a transaction, so the migration
// runs with foreign keys off, and the references are checked before it commits.
const noForeignKeys = "-- regulus:no-foreign-keys"

func applyOne(ctx context.Context, db *sql.DB, d dialect, m migration) (err error) {
	// One connection for the pragma and the transaction.
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if !d.postgres && strings.HasPrefix(m.sql, noForeignKeys) {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return err
		}
		defer func() {
			if _, perr := conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`); perr != nil && err == nil {
				err = perr
			}
		}()
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if !d.postgres && strings.HasPrefix(m.sql, noForeignKeys) {
		var table sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT "table" FROM pragma_foreign_key_check LIMIT 1`).Scan(&table)
		if err == nil {
			return fmt.Errorf("the migration left rows that refer to missing rows in %s", table.String)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, reader_min) VALUES (?, ?, ?)`, m.version, m.name, m.readerMin); err != nil {
		return err
	}
	return tx.Commit()
}
