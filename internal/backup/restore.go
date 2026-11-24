package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CodexCircle/Regulus/internal/storage"
)

// ErrDataDirNotEmpty is the answer to restoring into a data directory that
// already holds a hub's data, without Force.
var ErrDataDirNotEmpty = errors.New("the data directory already holds a hub's data")

// RestoreOptions say where a backup is restored to.
type RestoreOptions struct {
	// Storage is the data directory and the database to restore into. The hub
	// must not run: the caller holds the lock of the data directory.
	Storage storage.Config
	// Passphrase opens the file.
	Passphrase string
	// Force moves what the data directory holds (the SQLite files, secret.key
	// and pki/) into restore-old-<time>/ instead of refusing.
	Force bool
	// Now is the time of the restore (the name of the old files' directory).
	Now time.Time
}

// Restored is what a restore did.
type Restored struct {
	Manifest Manifest
	// MovedTo is the directory the old files went to, empty when there were none.
	MovedTo string
}

// Restore puts a backup into an empty data directory and database. It checks
// the whole file first (decryption, format, schema, every checksum) in a
// private directory and changes nothing until that has passed. Then it makes
// the database at the schema of the backup, loads the tables, migrates it
// forward to this version, and writes secret.key and pki/. A database or data
// directory that is not empty is refused (ErrNotEmpty, ErrDataDirNotEmpty);
// Force moves old files aside for a SQLite hub, and on failure puts them back.
// A PostgreSQL database with tables is never touched: give it an empty one.
func Restore(ctx context.Context, r io.Reader, opts RestoreOptions) (Restored, error) {
	cfg := opts.Storage
	if err := cfg.Validate(); err != nil {
		return Restored{}, err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return Restored{}, fmt.Errorf("create data directory: %w", err)
	}
	stage, err := os.MkdirTemp(cfg.DataDir, ".restore-stage-")
	if err != nil {
		return Restored{}, err
	}
	defer func() { _ = os.RemoveAll(stage) }()

	m, err := Extract(r, opts.Passphrase, stage)
	if err != nil {
		return Restored{}, err
	}
	latest, err := storage.LatestSchema(cfg)
	if err != nil {
		return Restored{}, err
	}
	if m.SchemaVersion > latest {
		return Restored{}, fmt.Errorf("the backup was made by Regulus %s with schema %d, and this version knows up to %d: %w",
			m.HubVersion, m.SchemaVersion, latest, storage.ErrSchemaNewer)
	}

	old := existing(cfg)
	out := Restored{Manifest: m}
	if len(old) > 0 {
		if !opts.Force {
			return Restored{}, fmt.Errorf("%w (%s): restore into an empty data directory, or use --force to move it aside", ErrDataDirNotEmpty, strings.Join(old, ", "))
		}
		now := opts.Now
		if now.IsZero() {
			now = time.Now()
		}
		out.MovedTo = filepath.Join(cfg.DataDir, "restore-old-"+now.UTC().Format("20060102T150405Z"))
		if err := moveAside(cfg.DataDir, out.MovedTo, old); err != nil {
			return Restored{}, err
		}
	}
	undo := func(cause error) (Restored, error) {
		removeNew(cfg)
		if out.MovedTo != "" {
			if err := moveBack(cfg.DataDir, out.MovedTo); err != nil {
				cause = fmt.Errorf("%w (and the old files could not be put back from %s: %v)", cause, out.MovedTo, err)
			}
		}
		return Restored{}, cause
	}

	tables, closeAll, err := openTables(stage, m)
	if err != nil {
		return undo(err)
	}
	defer closeAll()
	if err := storage.Import(ctx, cfg, m.SchemaVersion, tables); err != nil {
		return undo(err)
	}
	if err := copyKeys(stage, cfg.DataDir); err != nil {
		return undo(err)
	}
	return out, nil
}

// the names of what a hub keeps in its data directory and a restore replaces.
func existing(cfg storage.Config) []string {
	var found []string
	names := []string{secretFile, pkiDir}
	if !cfg.IsPostgres() {
		names = append([]string{storage.FileName, storage.FileName + "-wal", storage.FileName + "-shm"}, names...)
	}
	for _, n := range names {
		if _, err := os.Lstat(filepath.Join(cfg.DataDir, n)); err == nil {
			found = append(found, n)
		}
	}
	return found
}

func moveAside(dataDir, to string, names []string) error {
	if err := os.MkdirAll(to, 0o700); err != nil {
		return err
	}
	for _, n := range names {
		if err := os.Rename(filepath.Join(dataDir, n), filepath.Join(to, n)); err != nil {
			return fmt.Errorf("move %s aside: %w", n, err)
		}
	}
	return nil
}

func moveBack(dataDir, from string) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.Rename(filepath.Join(from, e.Name()), filepath.Join(dataDir, e.Name())); err != nil {
			return err
		}
	}
	return os.Remove(from)
}

// removeNew deletes what a failed restore wrote.
func removeNew(cfg storage.Config) {
	if !cfg.IsPostgres() {
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(filepath.Join(cfg.DataDir, storage.FileName+s))
		}
	}
	_ = os.Remove(filepath.Join(cfg.DataDir, secretFile))
	_ = os.RemoveAll(filepath.Join(cfg.DataDir, pkiDir))
}

func openTables(stage string, m Manifest) ([]storage.ImportTable, func(), error) {
	var files []*os.File
	closeAll := func() {
		for _, f := range files {
			_ = f.Close()
		}
	}
	var tables []storage.ImportTable
	for _, name := range m.Tables {
		f, err := os.Open(filepath.Join(stage, filepath.FromSlash(dbPrefix+name+tableSuffix)))
		if err != nil {
			closeAll()
			return nil, nil, err
		}
		files = append(files, f)
		rr, cols, err := newRowReader(f, name)
		if err != nil {
			closeAll()
			return nil, nil, err
		}
		tables = append(tables, storage.ImportTable{Table: storage.Table{Name: name, Columns: cols}, Next: rr.next})
	}
	return tables, closeAll, nil
}

func copyKeys(stage, dataDir string) error {
	copyOne := func(rel string, mode fs.FileMode) error {
		src, err := os.Open(filepath.Join(stage, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer src.Close()
		dst := filepath.Join(dataDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		_, cerr := io.Copy(f, src)
		if err := errors.Join(cerr, f.Sync(), f.Close()); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
		return nil
	}
	if err := copyOne(secretName, 0o600); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(stage, pkiDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyOne(pkiPrefix+e.Name(), 0o600); err != nil {
			return err
		}
	}
	return nil
}
