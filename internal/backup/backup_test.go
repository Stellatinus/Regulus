package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/storage"
	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

const pass = "correct horse battery staple"

// hubDir makes a data directory like a hub's: a migrated database with a few
// rows (one in a table that is never exported), secret.key and pki/.
func hubDir(t *testing.T) (storage.Config, string) {
	t.Helper()
	dir := t.TempDir()
	db := storagetest.Open(t, dir)
	for _, q := range []string{
		`INSERT INTO users (username, password_hash, role, created_at) VALUES ('admin', 'hash-a', 'admin', '2026-01-01T00:00:00Z')`,
		`INSERT INTO users (username, password_hash, role, created_at) VALUES ('gina', 'hash-g', 'viewer', '2026-01-02T00:00:00Z')`,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES ('tok', 1, '2026-01-01T00:00:00Z', '2099-01-01T00:00:00Z')`,
		`INSERT INTO metric_buckets (agent_id, tier, start, samples, load1, mem_used, mem_total, swap_used, swap_total) VALUES ('a', 1, 60, 2, 0.5, 1, 2, 3, 4)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.key"), []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "pki"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"ca.crt": "CERT", "ca.key": "KEY"} {
		if err := os.WriteFile(filepath.Join(dir, "pki", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return storagetest.Config(t, dir), dir
}

func create(t *testing.T, cfg storage.Config, withMetrics bool) ([]byte, Manifest) {
	t.Helper()
	var buf bytes.Buffer
	m, err := Create(context.Background(), CreateOptions{
		Storage: cfg, HubVersion: "0.9.0", Passphrase: pass, WithMetrics: withMetrics, WorkFactor: 10,
		Now: time.Date(2026, 3, 1, 3, 30, 0, 0, time.UTC),
	}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), m
}

func names(m Manifest) map[string]bool {
	out := map[string]bool{}
	for _, p := range m.Parts {
		out[p.Name] = true
	}
	return out
}

// A backup decrypts, verifies its checksums and contains the parts the design
// lists, and nothing that should be left out (#407).
func TestBackupDecryptsVerifiesAndHasTheListedParts(t *testing.T) {
	cfg, _ := hubDir(t)
	file, m := create(t, cfg, false)

	if bytes.Contains(file, []byte("hash-a")) || bytes.Contains(file, []byte("CERT")) {
		t.Fatal("the file is not encrypted")
	}
	got, err := Extract(bytes.NewReader(file), pass, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.FormatVersion != FormatVersion || got.HubVersion != "0.9.0" || got.CreatedAt != m.CreatedAt || got.DatabaseType != storage.TypeSQLite && got.DatabaseType != storage.TypePostgres {
		t.Errorf("manifest = %+v", got)
	}
	latest, _ := storage.LatestSchema(cfg)
	if got.SchemaVersion != latest {
		t.Errorf("schema = %d, want %d", got.SchemaVersion, latest)
	}
	n := names(got)
	for _, want := range []string{"secret.key", "pki/ca.crt", "pki/ca.key", "db/users.jsonl", "db/agents.jsonl", "db/audit_log.jsonl", "db/settings.jsonl"} {
		if !n[want] {
			t.Errorf("part %s is missing from %v", want, got.Parts)
		}
	}
	for _, not := range []string{"db/sessions.jsonl", "db/schema_migrations.jsonl", "db/pending_logins.jsonl", "db/metric_buckets.jsonl"} {
		if n[not] {
			t.Errorf("%s is in the backup", not)
		}
	}
	// With the metrics asked for, the table is there too.
	_, withM := create(t, cfg, true)
	if !names(withM)["db/metric_buckets.jsonl"] || !withM.WithMetrics {
		t.Error("--with-metrics did not include the metrics history")
	}
}

func TestBackupIsRefusedWithTheWrongPassphraseOrWhenDamaged(t *testing.T) {
	cfg, _ := hubDir(t)
	file, _ := create(t, cfg, false)
	if _, err := Extract(bytes.NewReader(file), "not the passphrase", t.TempDir()); !errors.Is(err, ErrWrongPassphrase) {
		t.Errorf("wrong passphrase: %v", err)
	}
	if _, err := Extract(strings.NewReader("this is not a backup"), pass, t.TempDir()); err == nil {
		t.Error("garbage accepted")
	}
	// Any flipped byte is found: by age's authentication or by a checksum.
	for _, at := range []int{len(file) / 3, len(file) / 2, len(file) - 5} {
		bad := append([]byte(nil), file...)
		bad[at] ^= 0x01
		if _, err := Extract(bytes.NewReader(bad), pass, t.TempDir()); err == nil {
			t.Errorf("a changed byte at %d was not found", at)
		}
	}
	if _, err := Extract(bytes.NewReader(file[:len(file)-40]), pass, t.TempDir()); err == nil {
		t.Error("a cut file was accepted")
	}
	if _, err := Create(context.Background(), CreateOptions{Storage: cfg}, &bytes.Buffer{}); err == nil {
		t.Error("a backup without a passphrase was made")
	}
}

func query(t *testing.T, cfg storage.Config, q string, dest ...any) {
	t.Helper()
	db, err := storage.OpenExisting(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.QueryRow(q).Scan(dest...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// A restore into an empty data directory brings back the users with their ids,
// the keys and the CA, and not the sessions (#408).
func TestRestoreBringsBackTheHub(t *testing.T) {
	cfg, dir := hubDir(t)
	file, m := create(t, cfg, false)

	newDir := t.TempDir()
	target := storagetest.Config(t, newDir)
	res, err := Restore(context.Background(), bytes.NewReader(file), RestoreOptions{Storage: target, Passphrase: pass})
	if err != nil {
		t.Fatal(err)
	}
	if res.Manifest.CreatedAt != m.CreatedAt || res.MovedTo != "" {
		t.Errorf("result = %+v", res)
	}
	var id int64
	var hash, role string
	query(t, target, `SELECT id, password_hash, role FROM users WHERE username = 'gina'`, &id, &hash, &role)
	if id != 2 || hash != "hash-g" || role != "viewer" {
		t.Errorf("gina = %d %s %s", id, hash, role)
	}
	var sessions, buckets int
	query(t, target, `SELECT COUNT(*) FROM sessions`, &sessions)
	query(t, target, `SELECT COUNT(*) FROM metric_buckets`, &buckets)
	if sessions != 0 || buckets != 0 {
		t.Errorf("sessions = %d, buckets = %d", sessions, buckets)
	}
	for _, f := range []string{"secret.key", "pki/ca.crt", "pki/ca.key"} {
		a, _ := os.ReadFile(filepath.Join(dir, f))
		b, err := os.ReadFile(filepath.Join(newDir, f))
		if err != nil || !bytes.Equal(a, b) {
			t.Errorf("%s: %q, %v", f, b, err)
		}
	}
	if info, _ := os.Stat(filepath.Join(newDir, "secret.key")); info.Mode().Perm() != 0o600 {
		t.Errorf("secret.key mode = %v", info.Mode().Perm())
	}
	// The numbering goes on after the restored ids.
	db, _ := storage.OpenExisting(context.Background(), target)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO users (username, password_hash, role, created_at) VALUES ('new', 'h', 'viewer', '2026-02-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	// The scratch directories are gone.
	left, _ := filepath.Glob(filepath.Join(newDir, ".*-stage-*"))
	if len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

func TestRestoreRefusesANonEmptyTargetAndForceMovesItAside(t *testing.T) {
	cfg, _ := hubDir(t)
	file, _ := create(t, cfg, false)

	other, otherDir := hubDir(t)
	_, err := Restore(context.Background(), bytes.NewReader(file), RestoreOptions{Storage: other, Passphrase: pass})
	if storagetest.Postgres() {
		// The database is not empty: never touched.
		if !errors.Is(err, storage.ErrNotEmpty) && !errors.Is(err, ErrDataDirNotEmpty) {
			t.Fatalf("err = %v", err)
		}
		return
	}
	if !errors.Is(err, ErrDataDirNotEmpty) {
		t.Fatalf("err = %v", err)
	}
	var users int
	query(t, other, `SELECT COUNT(*) FROM users`, &users)
	if users != 2 {
		t.Fatalf("a refused restore changed the database: %d users", users)
	}

	res, err := Restore(context.Background(), bytes.NewReader(file), RestoreOptions{Storage: other, Passphrase: pass, Force: true, Now: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(otherDir, "restore-old-20260401T000000Z"); res.MovedTo != want {
		t.Fatalf("moved to %q, want %q", res.MovedTo, want)
	}
	for _, f := range []string{"regulus.db", "secret.key", "pki/ca.key"} {
		if _, err := os.Stat(filepath.Join(res.MovedTo, f)); err != nil {
			t.Errorf("old %s was not kept: %v", f, err)
		}
	}
}

func TestAFailedRestoreLeavesTheOldFilesInPlace(t *testing.T) {
	if storagetest.Postgres() {
		t.Skip("the files that move aside are SQLite's")
	}
	cfg, _ := hubDir(t)
	file, _ := create(t, cfg, false)
	other, otherDir := hubDir(t)
	// A wrong passphrase fails before anything moves.
	if _, err := Restore(context.Background(), bytes.NewReader(file), RestoreOptions{Storage: other, Passphrase: "no", Force: true}); err == nil {
		t.Fatal("restored with a wrong passphrase")
	}
	for _, f := range []string{"regulus.db", "secret.key", "pki/ca.key"} {
		if _, err := os.Stat(filepath.Join(otherDir, f)); err != nil {
			t.Errorf("%s moved by a refused restore: %v", f, err)
		}
	}
	if m, _ := filepath.Glob(filepath.Join(otherDir, "restore-old-*")); len(m) != 0 {
		t.Errorf("an old-files directory was made: %v", m)
	}
}

// A backup made by an older release is restored at its schema and migrated
// forward; one made by a newer release is refused before anything is written.
func TestRestoreMigratesForwardAndRefusesANewerSchema(t *testing.T) {
	cfg, _ := hubDir(t)
	file, _ := create(t, cfg, false)

	stage := t.TempDir()
	m, err := Extract(bytes.NewReader(file), pass, stage)
	if err != nil {
		t.Fatal(err)
	}
	tables, closeAll, err := openTables(stage, m)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	// Schema 17 is before the alerts, the API tokens and the metrics history.
	var old []storage.ImportTable
	for _, tb := range tables {
		switch tb.Name {
		case "alert_rules", "alerts", "api_tokens", "metric_buckets":
		default:
			old = append(old, tb)
		}
	}
	target := storagetest.Config(t, t.TempDir())
	if err := storage.Import(context.Background(), target, 17, old); err != nil {
		t.Fatal(err)
	}
	var users, version int
	query(t, target, `SELECT COUNT(*) FROM users`, &users)
	query(t, target, `SELECT MAX(version) FROM schema_migrations`, &version)
	latest, _ := storage.LatestSchema(target)
	if users != 2 || version != latest {
		t.Errorf("users = %d, schema = %d (want %d)", users, version, latest)
	}
	var rules int
	query(t, target, `SELECT COUNT(*) FROM alerts`, &rules) // a table the old schema did not have
	// Newer than this binary: refused.
	err = storage.Import(context.Background(), storagetest.Config(t, t.TempDir()), latest+1, nil)
	if !errors.Is(err, storage.ErrSchemaNewer) {
		t.Errorf("a newer schema: %v", err)
	}
}

// The export is portable: a backup of one backend restores on the other.
func TestBackupRestoresOnTheOtherBackend(t *testing.T) {
	if !storagetest.Postgres() {
		t.Skip("needs REGULUS_TEST_DATABASE_URL: SQLite to PostgreSQL and back")
	}
	pg, _ := hubDir(t) // the tests' backend is PostgreSQL here
	lite := storage.Config{Type: storage.TypeSQLite, DataDir: t.TempDir()}

	file, _ := create(t, pg, false)
	if _, err := Restore(context.Background(), bytes.NewReader(file), RestoreOptions{Storage: lite, Passphrase: pass}); err != nil {
		t.Fatalf("PostgreSQL to SQLite: %v", err)
	}
	var users int
	query(t, lite, `SELECT COUNT(*) FROM users`, &users)
	if users != 2 {
		t.Errorf("users = %d", users)
	}
	back, m := create(t, lite, false)
	if m.DatabaseType != storage.TypeSQLite {
		t.Errorf("type = %s", m.DatabaseType)
	}
	target := storagetest.Config(t, t.TempDir())
	if _, err := Restore(context.Background(), bytes.NewReader(back), RestoreOptions{Storage: target, Passphrase: pass}); err != nil {
		t.Fatalf("SQLite to PostgreSQL: %v", err)
	}
	query(t, target, `SELECT COUNT(*) FROM users`, &users)
	if users != 2 {
		t.Errorf("users = %d", users)
	}
}
