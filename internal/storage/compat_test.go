package storage

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// baselineMigration is the newest migration that was in a published release
// when the expand/contract rule (#247) started. Every migration above it must
// pass TestNewMigrationsAreExpandOnly. Raise it when a release is published
// (see docs/updates.md, "Rollback and the schema range").
const baselineMigration = 15

// A binary that knows migrations up to 3 starts on a database that also has
// migration 4, when 4 declares it is expand-only for readers of 3 or newer;
// it refuses 5, which declares nothing (the strict default).
func TestSchemaRangeAcceptsExpandOnlyMigrations(t *testing.T) {
	ctx := context.Background()
	base := []migration{
		{version: 1, name: "a", sql: `CREATE TABLE a (x INTEGER)`, readerMin: 1},
		{version: 2, name: "b", sql: `CREATE TABLE b (x INTEGER)`, readerMin: 2},
		{version: 3, name: "c", sql: `CREATE TABLE c (x INTEGER)`, readerMin: 3},
	}
	next := append(append([]migration{}, base...),
		migration{version: 4, name: "expand", sql: `ALTER TABLE c ADD COLUMN y INTEGER`, readerMin: 3},
	)

	db := memoryDB(t)
	if err := apply(ctx, db, dialects[TypeSQLite], next); err != nil {
		t.Fatal(err)
	}
	// The previous release (migrations 1 to 3) starts on the migrated database.
	if err := apply(ctx, db, dialects[TypeSQLite], base); err != nil {
		t.Fatalf("previous release on an expand-only schema: %v", err)
	}
	// And it did not touch the newer row.
	applied, err := appliedVersions(ctx, db, dialects[TypeSQLite])
	if err != nil {
		t.Fatal(err)
	}
	if applied[4] != 3 {
		t.Errorf("reader_min of migration 4 = %d, want 3", applied[4])
	}

	strict := append(append([]migration{}, next...),
		migration{version: 5, name: "contract", sql: `CREATE TABLE d (x INTEGER)`, readerMin: 5},
	)
	if err := apply(ctx, db, dialects[TypeSQLite], strict); err != nil {
		t.Fatal(err)
	}
	err = apply(ctx, db, dialects[TypeSQLite], base)
	if err == nil || !strings.Contains(err.Error(), "newer version") || !strings.Contains(err.Error(), "knows migration 5") {
		t.Fatalf("previous release on a strict migration: error = %v, want a newer-version error naming migration 5", err)
	}
}

// A database written before the reader_min column existed gets it, and its
// rows keep the strict meaning.
func TestReaderMinColumnIsAddedToAnOlderTable(t *testing.T) {
	ctx := context.Background()
	db := memoryDB(t)
	d := dialects[TypeSQLite]
	if _, err := db.Exec(d.createMigrations); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, name) VALUES (1, 'a'), (7, 'from_the_future')`); err != nil {
		t.Fatal(err)
	}
	err := apply(ctx, db, d, []migration{{version: 1, name: "a", sql: `SELECT 1`, readerMin: 1}})
	if err == nil || !strings.Contains(err.Error(), "newer version") {
		t.Fatalf("error = %v, want a newer-version error: a row without reader_min is strict", err)
	}
}

func TestParseReaderMin(t *testing.T) {
	for name, tc := range map[string]struct {
		body    string
		version int
		want    int
		wantErr bool
	}{
		"none":            {"CREATE TABLE a (x INT)", 5, 5, false},
		"declared":        {"-- Expand only.\n-- regulus:reader-min 3\nALTER TABLE a ADD COLUMN y INT", 5, 3, false},
		"after code":      {"CREATE TABLE a (x INT);\n-- regulus:reader-min 3", 5, 5, false},
		"its own version": {"-- regulus:reader-min 5\nSELECT 1", 5, 0, true},
		"newer":           {"-- regulus:reader-min 9\nSELECT 1", 5, 0, true},
		"not a number":    {"-- regulus:reader-min x\nSELECT 1", 5, 0, true},
		"zero":            {"-- regulus:reader-min 0\nSELECT 1", 5, 0, true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseReaderMin("0005_x.sql", tc.body, tc.version)
			if (err != nil) != tc.wantErr || (err == nil && got != tc.want) {
				t.Fatalf("got %d, %v; want %d, error %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestLoadMigrationsReadsTheDirective(t *testing.T) {
	fsys := fstest.MapFS{
		"m/0001_a.sql": {Data: []byte("SELECT 1")},
		"m/0002_b.sql": {Data: []byte("-- regulus:reader-min 1\nSELECT 1")},
	}
	list, err := loadMigrations(fsys, "m")
	if err != nil {
		t.Fatal(err)
	}
	if list[0].readerMin != 1 || list[1].readerMin != 1 {
		t.Fatalf("readerMin = %d, %d; want 1, 1", list[0].readerMin, list[1].readerMin)
	}
}

// destructive matches what makes the previous release fail on the new schema:
// dropping or renaming something it reads, changing a column, or adding a
// NOT NULL column without a default (its INSERTs would fail).
var destructive = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bDROP\s+(TABLE|COLUMN|INDEX|VIEW|TRIGGER|CONSTRAINT)\b`),
	regexp.MustCompile(`(?i)\bRENAME\b`),
	regexp.MustCompile(`(?i)\bALTER\s+COLUMN\b`),
	regexp.MustCompile(`(?i)\bADD\s+COLUMN\b[^;]*\bNOT\s+NULL\b`),
}

var hasDefault = regexp.MustCompile(`(?i)\bDEFAULT\b`)

// unsafeForPreviousRelease reports whether a statement can break the
// previous release on the new schema.
func unsafeForPreviousRelease(stmt string) bool {
	for _, re := range destructive {
		if re.MatchString(stmt) && (!strings.Contains(re.String(), "NOT") || !hasDefault.MatchString(stmt)) {
			return true
		}
	}
	return false
}

// The rule of the project (#247): a migration newer than the last released one
// either only expands the schema and says so (reader-min below its own
// version), or it is strict (no directive) and so a rollback past it needs the
// database put back. The test cannot tell the two apart by reading SQL, so it
// makes the author choose: a statement that can break the previous release
// is allowed only without the directive, and then it must be named in
// contractMigrations so the choice is visible in review.
func TestNewMigrationsAreExpandOnly(t *testing.T) {
	for dir, d := range map[string]dialect{"sqlite": dialects[TypeSQLite], "postgres": dialects[TypePostgres]} {
		list, err := loadMigrations(migrationFiles, d.dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range list {
			if m.version <= baselineMigration {
				continue
			}
			unsafe := false
			for _, stmt := range strings.Split(m.sql, ";") {
				unsafe = unsafe || unsafeForPreviousRelease(stmt)
			}
			switch {
			case unsafe && m.readerMin < m.version:
				t.Errorf("%s migration %04d_%s declares reader-min %d but drops, renames or changes something the previous release uses; remove the directive (a strict migration) and list it in contractMigrations", dir, m.version, m.name, m.readerMin)
			case unsafe && !contractMigrations[m.version]:
				t.Errorf("%s migration %04d_%s drops, renames or changes something; a contract step belongs one release after the expand step. If this is that step, add %d to contractMigrations", dir, m.version, m.name, m.version)
			}
		}
	}
}

// contractMigrations lists the migrations that are allowed to contract the
// schema: each is strict, so a rollback past it restores the database.
var contractMigrations = map[int]bool{}

func TestExpandOnlyLintCatchesTheUnsafeStatements(t *testing.T) {
	for stmt, unsafe := range map[string]bool{
		"CREATE TABLE a (x INT)":                            false,
		"ALTER TABLE a ADD COLUMN y INT":                    false,
		"ALTER TABLE a ADD COLUMN y INT NOT NULL DEFAULT 0": false,
		"ALTER TABLE a ADD COLUMN y INT NOT NULL":           true,
		"ALTER TABLE a DROP COLUMN y":                       true,
		"DROP TABLE a":                                      true,
		"ALTER TABLE a RENAME TO b":                         true,
		"ALTER TABLE a ALTER COLUMN y TYPE TEXT":            true,
	} {
		if got := unsafeForPreviousRelease(stmt); got != unsafe {
			t.Errorf("%q: unsafe = %v, want %v", stmt, got, unsafe)
		}
	}
}
