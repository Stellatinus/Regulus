package backup

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"filippo.io/age"

	"github.com/CodexCircle/Regulus/internal/storage"
)

// secretFile is secret.key of the data directory (internal/auth).
const secretFile = "secret.key"

// pkiDir is the directory of the CA (internal/pki, as the hub names it).
const pkiDir = "pki"

// CreateOptions say what a backup is made of.
type CreateOptions struct {
	// Storage is the hub's database and data directory.
	Storage storage.Config
	// HubVersion is written in the manifest.
	HubVersion string
	// Passphrase encrypts the file; it is never stored.
	Passphrase string
	// WithMetrics includes the metrics history (#405), left out by default.
	WithMetrics bool
	// WorkFactor is the scrypt work factor of age; zero means age's default.
	// Tests lower it.
	WorkFactor int
	// Now is the creation time; zero means the current time.
	Now time.Time
}

// Create writes an encrypted backup of the hub to w. The hub may be running.
// The database is exported through internal/storage, secret.key and pki/ are
// copied, and the manifest, which goes first, lists every part with its
// SHA-256. The parts are staged in a private directory of the data directory
// (removed before it returns), because the manifest needs their checksums
// before the first one is written.
func Create(ctx context.Context, opts CreateOptions, w io.Writer) (Manifest, error) {
	if opts.Passphrase == "" {
		return Manifest{}, errors.New("a backup needs a passphrase")
	}
	dataDir := opts.Storage.DataDir
	stage, err := os.MkdirTemp(dataDir, ".backup-stage-")
	if err != nil {
		return Manifest{}, fmt.Errorf("stage the backup: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()

	m := Manifest{FormatVersion: FormatVersion, HubVersion: opts.HubVersion, DatabaseType: opts.Storage.Type, CreatedAt: opts.Now, WithMetrics: opts.WithMetrics}
	if m.DatabaseType == "" {
		m.DatabaseType = storage.TypeSQLite
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	m.CreatedAt = m.CreatedAt.UTC()

	stageFile := func(name string, write func(io.Writer) error) error {
		path := filepath.Join(stage, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		hw := newHashingWriter(f)
		werr := write(hw)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return fmt.Errorf("%s: %w", name, werr)
		}
		m.Parts = append(m.Parts, Part{Name: name, Size: hw.n, SHA256: hw.sum()})
		return nil
	}

	// The database.
	schema, err := storage.Export(ctx, opts.Storage, opts.WithMetrics, func(t storage.Table, rows storage.Rows) error {
		m.Tables = append(m.Tables, t.Name)
		return stageFile(dbPrefix+t.Name+tableSuffix, func(w io.Writer) error { return writeTable(w, t, rows) })
	})
	if err != nil {
		return Manifest{}, fmt.Errorf("export the database: %w", err)
	}
	m.SchemaVersion = schema

	// The keys.
	if err := copyIfExists(filepath.Join(dataDir, secretFile), secretName, stageFile); err != nil {
		return Manifest{}, err
	}
	pki := filepath.Join(dataDir, pkiDir)
	entries, err := os.ReadDir(pki)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, err
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			if err := copyIfExists(filepath.Join(pki, e.Name()), pkiPrefix+e.Name(), stageFile); err != nil {
				return Manifest{}, err
			}
		}
	}
	sort.Slice(m.Parts, func(i, j int) bool { return m.Parts[i].Name < m.Parts[j].Name })

	// The file: manifest first, then the parts, all encrypted.
	rec, err := age.NewScryptRecipient(opts.Passphrase)
	if err != nil {
		return Manifest{}, err
	}
	if opts.WorkFactor > 0 {
		rec.SetWorkFactor(opts.WorkFactor)
	}
	enc, err := age.Encrypt(w, rec)
	if err != nil {
		return Manifest{}, err
	}
	tw := tar.NewWriter(enc)
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := writeEntry(tw, manifestName, int64(len(mb)), m.CreatedAt, func(w io.Writer) error { _, err := w.Write(mb); return err }); err != nil {
		return Manifest{}, err
	}
	for _, p := range m.Parts {
		path := filepath.Join(stage, filepath.FromSlash(p.Name))
		err := writeEntry(tw, p.Name, p.Size, m.CreatedAt, func(w io.Writer) error {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(w, f)
			return err
		})
		if err != nil {
			return Manifest{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return Manifest{}, err
	}
	if err := enc.Close(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func writeEntry(tw *tar.Writer, name string, size int64, mod time.Time, write func(io.Writer) error) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: mod, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	return write(tw)
}

func copyIfExists(src, name string, stageFile func(string, func(io.Writer) error) error) error {
	f, err := os.Open(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	return stageFile(name, func(w io.Writer) error { _, err := io.Copy(w, f); return err })
}

func writeTable(w io.Writer, t storage.Table, rows storage.Rows) error {
	if err := jsonLine(w, header{Table: t.Name, Columns: t.Columns}); err != nil {
		return err
	}
	vals := make([]any, len(t.Columns))
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		out := make([]any, len(vals))
		for i, v := range vals {
			out[i] = encodeValue(v)
		}
		if err := jsonLine(w, out); err != nil {
			return err
		}
	}
	return rows.Err()
}
