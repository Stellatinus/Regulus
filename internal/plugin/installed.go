package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The layout the store gives a plugin on disk (docs/plugins/store.md):
//
//	<plugins dir>/<id>/<version>/   the unpacked package: plugin.yaml, programs, ui
//	<plugins dir>/<id>/current      a relative symbolic link to the version that runs
//	<plugins dir>/<id>/source.json  where it came from (SourceRecord), written by the root helper
//
// A plugin put by hand, <plugins dir>/<id>/plugin.yaml with no current, is
// still found: the example plugin and every test use that flat layout. Both
// are owned by root; only the helper (store.Applier) writes them.
const (
	// CurrentName is the link, inside a plugin's directory, naming the version
	// that runs.
	CurrentName = "current"
	// SourceFileName is the record of where an installed plugin came from.
	SourceFileName = "source.json"
)

// SourceRecord is what the helper remembers of an installation: the source the
// plugin came from (updates come only from it), the version that is current,
// the one before it (kept on disk for a rollback, which is not built yet) and
// the checksum of the package that was unpacked.
type SourceRecord struct {
	Source      string    `json:"source"`
	Version     string    `json:"version"`
	Previous    string    `json:"previous,omitempty"`
	SHA256      string    `json:"sha256,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
}

const maxSourceRecord = 16 << 10

// ReadSourceRecord returns the record of the plugin directory pluginDir
// (<plugins dir>/<id>). A missing, unreadable or oversized record is none.
func ReadSourceRecord(pluginDir string) (SourceRecord, bool) {
	p := filepath.Join(pluginDir, SourceFileName)
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSourceRecord {
		return SourceRecord{}, false
	}
	f, err := os.Open(p)
	if err != nil {
		return SourceRecord{}, false
	}
	defer func() { _ = f.Close() }()
	var r SourceRecord
	if json.NewDecoder(io.LimitReader(f, maxSourceRecord)).Decode(&r) != nil {
		return SourceRecord{}, false
	}
	return r, true
}

// WriteSourceRecord writes the record atomically, readable by everyone (the
// hub reads it to say which source an installed plugin came from).
func WriteSourceRecord(pluginDir string, r SourceRecord) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(pluginDir, ".tmp-source-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(pluginDir, SourceFileName))
}

// resolveVersionDir returns the directory that holds the plugin's files: the
// version <id>/current points at, or <id> itself in the flat layout. The link
// must be relative and name one directory next to it, so it cannot lead out of
// the plugin's directory; anything else is an error.
func resolveVersionDir(pdir string) (dir string, versioned bool, err error) {
	link := filepath.Join(pdir, CurrentName)
	info, lerr := os.Lstat(link)
	if errors.Is(lerr, os.ErrNotExist) {
		return pdir, false, nil
	}
	if lerr != nil {
		return "", false, lerr
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", false, fmt.Errorf("%s must be a symbolic link to a version", CurrentName)
	}
	target, err := os.Readlink(link)
	if err != nil {
		return "", false, err
	}
	if target == "" || target == "." || target == ".." || strings.ContainsAny(target, `/\`) || target[0] == '.' {
		return "", false, fmt.Errorf("%s points at %q: it must name one version directory next to it", CurrentName, target)
	}
	dir = filepath.Join(pdir, target)
	vi, err := os.Lstat(dir)
	if err != nil {
		return "", false, fmt.Errorf("%s points at %q: %w", CurrentName, target, err)
	}
	if !vi.IsDir() {
		return "", false, fmt.Errorf("%s points at %q, which is not a directory", CurrentName, target)
	}
	return dir, true, nil
}

// Installed is a plugin that is on disk, running or not (a plugin with only a
// UI component never runs and is not in the supervisor's status).
type Installed struct {
	ID      string
	Version string
	// Source is the source it was installed from; empty when it was put there
	// by hand (no record).
	Source string
	// Dir is where its files are (the current version's directory).
	Dir string
}

// ListInstalled lists the plugins of the plugins directory with a valid
// manifest, with the source they came from. Plugins that cannot be used are
// left out (Discover reports them).
func ListInstalled(dir string) []Installed {
	found, _, err := Discover(dir)
	if err != nil {
		return nil
	}
	out := make([]Installed, 0, len(found))
	for _, f := range found {
		in := Installed{ID: f.Manifest.ID, Version: f.Manifest.Version, Dir: f.Dir}
		if rec, ok := ReadSourceRecord(filepath.Join(dir, f.Manifest.ID)); ok && rec.Version == f.Manifest.Version {
			in.Source = rec.Source
		}
		out = append(out, in)
	}
	return out
}
