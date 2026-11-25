package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The portable export and import of the hub's tables (docs/backup.md). They
// are the only code that reads the whole schema, and the only one that knows
// how SQLite and PostgreSQL list their tables, columns and foreign keys, so a
// backup made on one backend restores on the other (#124 can reuse them).
//
// What is exported: every table of the schema except the ones that do not
// belong in a copy: schema_migrations (the import makes it by applying the
// migrations), the sessions and the pending logins (nobody stays signed in
// through a restore), and the metrics history unless asked for. Tables come
// in an order in which a table follows the ones it refers to, so the import
// can insert them as they are read.

// ErrNotEmpty is the answer to importing into a database that already has the
// hub's tables.
var ErrNotEmpty = errors.New("the database already has the hub's tables")

// Table is a table of the export: its name and its columns in order.
type Table struct {
	Name    string
	Columns []string
}

// Rows is the rows of one table.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

// notExported are the tables that are never part of a copy.
var notExported = map[string]bool{"schema_migrations": true, "sessions": true, "pending_logins": true}

// metricsTables are the tables of the metrics history (#405), exported only
// when asked for.
var metricsTables = map[string]bool{"metric_buckets": true}

// LatestSchema is the number of the newest migration this binary knows for
// the backend of cfg.
func LatestSchema(cfg Config) (int, error) {
	d := dialects[backendType(cfg)]
	migrations, err := loadMigrations(migrationFiles, d.dir)
	if err != nil {
		return 0, err
	}
	latest := 0
	for _, m := range migrations {
		latest = max(latest, m.version)
	}
	return latest, nil
}

// querier is what the export reads with: a transaction on PostgreSQL, the
// snapshot on SQLite.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Export reads a consistent copy of the data and gives each table, in order,
// to visit. It returns the schema version of the copy. The hub may be running:
// SQLite is copied with VACUUM INTO into a temporary file in the data
// directory (removed before it returns) and read from there; PostgreSQL is
// read in one REPEATABLE READ transaction. visit must read the rows to the end
// before it returns.
func Export(ctx context.Context, cfg Config, withMetrics bool, visit func(Table, Rows) error) (schema int, err error) {
	if err := cfg.Validate(); err != nil {
		return 0, err
	}
	if cfg.IsPostgres() {
		return exportPostgres(ctx, cfg, withMetrics, visit)
	}
	tmp, err := os.MkdirTemp(cfg.DataDir, ".backup-")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	copyPath := filepath.Join(tmp, "snapshot.db")
	if _, err := SnapshotSQLite(ctx, cfg.DataDir, copyPath); err != nil {
		return 0, err
	}
	db, err := openReadOnly(copyPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	return exportFrom(ctx, db, false, withMetrics, visit)
}

func exportPostgres(ctx context.Context, cfg Config, withMetrics bool, visit func(Table, Rows) error) (int, error) {
	db, err := openPool(cfg)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	if err := waitFor(ctx, db, cfg); err != nil {
		return 0, err
	}
	if _, err := verifySchema(ctx, db, cfg); err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // read only
	return exportFrom(ctx, tx, true, withMetrics, visit)
}

func exportFrom(ctx context.Context, q querier, postgres, withMetrics bool, visit func(Table, Rows) error) (int, error) {
	var v sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("read the schema version: %w", err)
	}
	tables, err := listTables(ctx, q, postgres)
	if err != nil {
		return 0, err
	}
	for _, t := range tables {
		if notExported[t.Name] || (metricsTables[t.Name] && !withMetrics) {
			continue
		}
		rows, err := q.QueryContext(ctx, `SELECT `+quoteList(t.Columns)+` FROM `+quoteIdent(t.Name))
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", t.Name, err)
		}
		verr := visit(t, rows)
		cerr := rows.Close()
		if verr != nil {
			return 0, verr
		}
		if err := errors.Join(rows.Err(), cerr); err != nil {
			return 0, fmt.Errorf("read %s: %w", t.Name, err)
		}
	}
	return int(v.Int64), nil
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func quoteList(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = quoteIdent(c)
	}
	return strings.Join(q, ", ")
}

// listTables returns every table with its columns, ordered so that a table
// comes after the tables it refers to (ties by name, so the order is the same
// on every run).
func listTables(ctx context.Context, q querier, postgres bool) ([]Table, error) {
	nameQ := `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`
	if postgres {
		nameQ = `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`
	}
	names, err := queryStrings(ctx, q, nameQ)
	if err != nil {
		return nil, fmt.Errorf("list the tables: %w", err)
	}
	sort.Strings(names)
	tables := make([]Table, 0, len(names))
	deps := map[string][]string{}
	for _, n := range names {
		var cols, refs []string
		if postgres {
			cols, err = queryStrings(ctx, q, `SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ? ORDER BY ordinal_position`, n)
			if err == nil {
				refs, err = queryStrings(ctx, q, `SELECT DISTINCT p.relname FROM pg_constraint c
					JOIN pg_class t ON t.oid = c.conrelid JOIN pg_class p ON p.oid = c.confrelid
					WHERE c.contype = 'f' AND t.relname = ? AND t.relnamespace = current_schema()::regnamespace`, n)
			}
		} else {
			cols, err = queryStrings(ctx, q, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, n)
			if err == nil {
				refs, err = queryStrings(ctx, q, `SELECT DISTINCT "table" FROM pragma_foreign_key_list(?)`, n)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("describe %s: %w", n, err)
		}
		tables = append(tables, Table{Name: n, Columns: cols})
		for _, r := range refs {
			if r != n {
				deps[n] = append(deps[n], r)
			}
		}
	}
	return orderByDependency(tables, deps)
}

// orderByDependency puts a table after the ones it refers to.
func orderByDependency(tables []Table, deps map[string][]string) ([]Table, error) {
	byName := map[string]Table{}
	for _, t := range tables {
		byName[t.Name] = t
	}
	done := map[string]bool{}
	visiting := map[string]bool{}
	var out []Table
	var visit func(string) error
	visit = func(n string) error {
		if done[n] {
			return nil
		}
		if visiting[n] {
			return fmt.Errorf("tables refer to each other in a cycle at %s", n)
		}
		visiting[n] = true
		for _, d := range deps[n] {
			if _, ok := byName[d]; ok {
				if err := visit(d); err != nil {
					return err
				}
			}
		}
		visiting[n] = false
		done[n] = true
		out = append(out, byName[n])
		return nil
	}
	for _, t := range tables {
		if err := visit(t.Name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func queryStrings(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ImportTable is the rows of one table to put into a database. Next returns
// the values of the next row in the order of Columns, and false when there
// are no more.
type ImportTable struct {
	Table
	Next func() (row []any, ok bool, err error)
}

// Import creates the schema at version schema in an empty database (a SQLite
// file that does not exist yet, a PostgreSQL schema with no hub tables),
// inserts the tables in the order given in one transaction, and then applies
// the migrations after schema, so a copy made by an older release comes out
// at the schema of this one. It refuses a schema newer than this binary
// knows (ErrSchemaNewer) before it touches anything, and a database that
// already has the tables (ErrNotEmpty).
func Import(ctx context.Context, cfg Config, schema int, tables []ImportTable) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	latest, err := LatestSchema(cfg)
	if err != nil {
		return err
	}
	if schema > latest {
		return fmt.Errorf("the copy has schema %d and this version of Regulus knows up to %d: %w", schema, latest, ErrSchemaNewer)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	db, err := openPool(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := waitFor(ctx, db, cfg); err != nil {
		return err
	}
	d := dialects[backendType(cfg)]
	if cfg.IsPostgres() {
		if _, err := verifySchema(ctx, db, cfg); err != nil {
			return err
		}
	}
	exists, err := hasMigrationsTable(ctx, db, d)
	if err != nil {
		return err
	}
	if exists {
		return ErrNotEmpty
	}

	migrations, err := loadMigrations(migrationFiles, d.dir)
	if err != nil {
		return err
	}
	var upTo []migration
	for _, m := range migrations {
		if m.version <= schema {
			upTo = append(upTo, m)
		}
	}
	if err := apply(ctx, db, d, upTo); err != nil {
		return err
	}
	if err := insertTables(ctx, db, cfg.IsPostgres(), tables); err != nil {
		return err
	}
	return Migrate(ctx, db, backendType(cfg))
}

func insertTables(ctx context.Context, db *sql.DB, postgres bool, tables []ImportTable) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	have, err := listTables(ctx, tx, postgres)
	if err != nil {
		return err
	}
	known := map[string]Table{}
	for _, t := range have {
		known[t.Name] = t
	}
	for _, t := range tables {
		kt, ok := known[t.Name]
		if !ok || notExported[t.Name] {
			return fmt.Errorf("the copy has a table %q that the schema does not have", t.Name)
		}
		for _, c := range t.Columns {
			if !contains(kt.Columns, c) {
				return fmt.Errorf("the copy has a column %s.%s that the schema does not have", t.Name, c)
			}
		}
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(t.Columns)), ", ")
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO `+quoteIdent(t.Name)+` (`+quoteList(t.Columns)+`) VALUES (`+marks+`)`)
		if err != nil {
			return fmt.Errorf("insert into %s: %w", t.Name, err)
		}
		for {
			row, more, err := t.Next()
			if err != nil {
				_ = stmt.Close()
				return err
			}
			if !more {
				break
			}
			if _, err := stmt.ExecContext(ctx, row...); err != nil {
				_ = stmt.Close()
				return fmt.Errorf("insert into %s: %w", t.Name, err)
			}
		}
		_ = stmt.Close()
	}
	if postgres {
		if err := resetIdentities(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// resetIdentities moves the sequence of every identity column past the rows
// that were inserted with their own ids.
func resetIdentities(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT table_name, column_name FROM information_schema.columns WHERE table_schema = current_schema() AND is_identity = 'YES'`)
	if err != nil {
		return err
	}
	type col struct{ table, column string }
	var cols []col
	for rows.Next() {
		var c col
		if err := rows.Scan(&c.table, &c.column); err != nil {
			_ = rows.Close()
			return err
		}
		cols = append(cols, c)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, c := range cols {
		q := fmt.Sprintf(`SELECT setval(pg_get_serial_sequence(%s, %s), COALESCE(MAX(%s), 1), MAX(%s) IS NOT NULL) FROM %s`,
			quoteLiteral(quoteIdent(c.table)), quoteLiteral(c.column), quoteIdent(c.column), quoteIdent(c.column), quoteIdent(c.table))
		var ignore any
		if err := tx.QueryRowContext(ctx, q).Scan(&ignore); err != nil {
			return fmt.Errorf("reset the numbering of %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}

func quoteLiteral(s string) string { return `'` + strings.ReplaceAll(s, `'`, `''`) + `'` }
