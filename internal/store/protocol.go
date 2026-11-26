package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Installing a plugin takes root, which the hub does not have (the plugins
// directory is owned by root, docs/plugins/runtime.md). As for the core's
// updates (internal/update/apply.go), the two sides talk through directories
// the hub owns: the hub puts the verified package in StageDir and a request in
// ApplyDir, systemd notices the request (regulus-plugin-apply.path) and starts
// the helper, `regulus plugin-apply`, which trusts nothing in those
// directories: it verifies the signed index again, then the package against
// the index, and writes a result.

// Names of the files the hub and the helper exchange.
const (
	requestName = "request.json"
	resultName  = "result.json"
	// Names inside StageDir/<plugin>_<version>/.
	stagedIndex   = "index.json"
	stagedBundle  = "index.json.sigstore.json"
	stagedPackage = "package.tar.gz"

	maxControlFile = 64 << 10
	maxBundleFile  = 1 << 20
)

// Actions of a Request.
const (
	ActionInstall = "install"
	ActionRemove  = "remove"
)

// Outcomes of a Result.
const (
	OutcomeApplied = "applied"
	OutcomeFailed  = "failed"
)

// Request asks the helper to install or remove a plugin. It names the plugin,
// the version and the source and nothing else: the helper never takes a path,
// a command or a checksum from it. ID is a random value the hub makes up so it
// knows the result it reads answers this request.
type Request struct {
	ID          string    `json:"id"`
	Action      string    `json:"action"`
	Plugin      string    `json:"plugin"`
	Version     string    `json:"version,omitempty"`
	Source      string    `json:"source,omitempty"`
	RequestedBy string    `json:"requested_by,omitempty"`
	RequestedAt time.Time `json:"requested_at"`
}

// Result is what the helper reports. Code is stable and is what a client
// tests; Message is English text for people.
type Result struct {
	RequestID string    `json:"request_id"`
	Action    string    `json:"action"`
	Plugin    string    `json:"plugin"`
	Version   string    `json:"version,omitempty"`
	From      string    `json:"from,omitempty"`
	Outcome   string    `json:"outcome"`
	Code      string    `json:"code,omitempty"`
	Message   string    `json:"message,omitempty"`
	At        time.Time `json:"at"`
}

// ApplyDir is the directory, inside the hub's data directory, that the hub
// and the helper share; systemd watches the request file in it.
func ApplyDir(dataDir string) string { return filepath.Join(dataDir, "plugin-apply") }

// StageDir is where the hub puts what the helper is to install, one directory
// per plugin and version.
func StageDir(dataDir string) string { return filepath.Join(dataDir, "plugin-store") }

func stagedDir(dataDir, plugin, version string) string {
	return filepath.Join(StageDir(dataDir), plugin+"_"+version)
}

// WriteRequest leaves a request for the helper; the file appears all at once.
func WriteRequest(dir string, req Request) error {
	return writeJSON(filepath.Join(dir, requestName), req, 0o600)
}

// ReadResult returns the last result the helper wrote.
func ReadResult(dir string) (Result, bool) {
	var res Result
	return res, readJSON(filepath.Join(dir, resultName), &res)
}

// RequestPending reports whether a request is waiting: the helper has not
// taken it yet.
func RequestPending(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, requestName))
	return err == nil
}

// takeRequest reads the request and removes whatever is at its place, so the
// file that woke the helper cannot wake it again (a path unit starts the
// service for as long as the file exists).
func takeRequest(dir string) (Request, bool, error) {
	p := filepath.Join(dir, requestName)
	if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
		return Request{}, false, nil
	}
	var req Request
	ok := readJSON(p, &req)
	_ = os.Remove(p)
	if !ok {
		return Request{}, true, errors.New("the request cannot be read")
	}
	return req, true, nil
}

// writeResult is the helper's: readable by the hub's user.
func writeResult(dir string, res Result) error {
	return writeJSON(filepath.Join(dir, resultName), res, 0o644)
}

func writeJSON(path string, v any, mode os.FileMode) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// openRegular opens a regular file without following a link at its last
// component and without blocking on a FIFO.
func openRegular(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		if err == nil {
			err = fmt.Errorf("%s is not a regular file", path)
		}
		return nil, err
	}
	return f, nil
}

// readRegular reads a regular file of at most max bytes.
func readRegular(path string, max int64) ([]byte, error) {
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, max)
	}
	return data, nil
}

func readJSON(path string, v any) bool {
	data, err := readRegular(path, maxControlFile)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, v) == nil
}
