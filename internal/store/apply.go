package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/CodexCircle/Regulus/internal/plugin"
	"github.com/CodexCircle/Regulus/internal/update"
)

// Codes of a failed Result. They are part of the API (docs/http-api.md): the
// dashboard translates them.
const (
	CodeBadRequest       = "bad_request"
	CodeUnknownSource    = "unknown_source"
	CodeNotVerified      = "not_verified"
	CodeIncompatible     = "incompatible"
	CodeYanked           = "yanked"
	CodeChecksum         = "checksum"
	CodeUnsafePackage    = "unsafe_package"
	CodeBadManifest      = "bad_manifest"
	CodeAlreadyInstalled = "already_installed"
	CodeNotInstalled     = "not_installed"
	CodeSourceMismatch   = "source_mismatch"
	CodeHandInstalled    = "hand_installed"
	CodePluginsDir       = "plugins_dir"
	CodeInternal         = "internal"
)

// applyError is a failure with a code.
type applyError struct {
	code string
	msg  string
	err  error
}

func (e *applyError) Error() string {
	if e.err != nil {
		return e.msg + ": " + e.err.Error()
	}
	return e.msg
}
func (e *applyError) Unwrap() error { return e.err }

func fail(code, format string, args ...any) *applyError {
	return &applyError{code: code, msg: fmt.Sprintf(format, args...)}
}
func failErr(code string, err error, format string, args ...any) *applyError {
	return &applyError{code: code, msg: fmt.Sprintf(format, args...), err: err}
}

// Applier is the helper that runs as root (`regulus plugin-apply`). The
// request only says which plugin, version and source; everything else is
// checked again here: the Sigstore signature of the index, the compatibility
// of the version, the package against the checksum and the size of the signed
// index, and every entry of the package.
type Applier struct {
	// DataDir is the hub's data directory (the request, the staged files).
	DataDir string
	// StateDir belongs to root; it holds the lock shared with the core's
	// update helper.
	StateDir string
	// PluginsDir is the plugins directory. It must be owned by OwnerUID and
	// not writable by anyone else.
	PluginsDir string
	// Sources are the custom sources of the configuration; the official one is
	// built in.
	Sources  []Source
	Verifier Verifier
	// Version is this Regulus' release ("" for a development build, which runs
	// any plugin whose api it speaks).
	Version string
	Machine Machine
	Now     func() time.Time
	// OwnerUID is the user that must own PluginsDir: 0 for the real helper;
	// tests, which are not root, set their own.
	OwnerUID int
	// Chown, if set, is called on every path the helper creates under
	// PluginsDir (the real helper runs as root, so they are root's already).
	Chown func(string) error
	Log   *slog.Logger

	// reqID is the request being handled, so progress.json says which it is for.
	reqID string
}

// step tells the hub which step the helper is in (progress.json, #530). It is
// information only: a failure to write it never stops the change.
func (a *Applier) step(plugin, action, version, step string) {
	now := a.now().UTC()
	_ = writeProgress(ApplyDir(a.DataDir), Progress{RequestID: a.reqID, Plugin: plugin, Action: action, Version: version, State: ProgressRunning, Step: step, Percent: -1, Started: now, At: now})
}

func (a *Applier) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Apply handles the request waiting in ApplyDir. It returns the result it
// wrote; the error is the failure, for the exit status. A missing request is
// ErrNoRequest.
func (a *Applier) Apply(ctx context.Context) (Result, error) {
	dir := ApplyDir(a.DataDir)
	req, found, err := takeRequest(dir)
	if !found {
		return Result{}, ErrNoRequest
	}
	res := Result{RequestID: req.ID, Action: req.Action, Plugin: req.Plugin, Version: req.Version, At: a.now()}
	if err != nil {
		res.Outcome, res.Code, res.Message = OutcomeFailed, CodeBadRequest, err.Error()
		_ = writeResult(dir, res)
		return res, err
	}
	var aerr *applyError
	a.reqID = req.ID
	switch req.Action {
	case ActionInstall:
		res.From, aerr = a.install(ctx, req)
	case ActionRemove:
		res.From, aerr = a.remove(req)
	default:
		aerr = fail(CodeBadRequest, "unknown action %q", req.Action)
	}
	res.At = a.now()
	if aerr != nil {
		res.Outcome, res.Code, res.Message = OutcomeFailed, aerr.code, aerr.Error()
		a.logf("plugin request failed", "action", req.Action, "plugin", req.Plugin, "code", aerr.code, "err", aerr.Error())
		if werr := writeResult(dir, res); werr != nil {
			a.logf("cannot write the result", "err", werr)
		}
		return res, aerr
	}
	res.Outcome = OutcomeApplied
	if werr := writeResult(dir, res); werr != nil {
		return res, werr
	}
	return res, nil
}

// ErrNoRequest is returned when the hub left nothing to do.
var ErrNoRequest = errors.New("there is no plugin request")

func (a *Applier) logf(msg string, args ...any) {
	if a.Log != nil {
		a.Log.Info(msg, args...)
	}
}

func (a *Applier) source(name string) (Source, bool) {
	for _, s := range append(DefaultSources(), a.Sources...) {
		if s.Name == name {
			return s, true
		}
	}
	return Source{}, false
}

// checkPluginsDir refuses a plugins directory that someone other than root
// could write: whatever is in it runs as the hub's user.
func (a *Applier) checkPluginsDir() *applyError {
	if a.PluginsDir == "" {
		return fail(CodePluginsDir, "plugins_dir is not set")
	}
	if err := os.MkdirAll(a.PluginsDir, 0o755); err != nil {
		return failErr(CodePluginsDir, err, "the plugins directory cannot be created")
	}
	info, err := os.Lstat(a.PluginsDir)
	if err != nil {
		return failErr(CodePluginsDir, err, "the plugins directory cannot be read")
	}
	if !info.IsDir() {
		return fail(CodePluginsDir, "%s is not a directory", a.PluginsDir)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fail(CodePluginsDir, "%s is writable by others than its owner (mode %o)", a.PluginsDir, info.Mode().Perm())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != a.OwnerUID {
		return fail(CodePluginsDir, "%s is not owned by the user the helper expects (uid %d)", a.PluginsDir, a.OwnerUID)
	}
	// The hub's user must be able to read what is installed. An installation
	// made by an earlier version has the directory at 0700 (created under
	// UMask=0077); add the read and search bits, never take any away.
	if perm := info.Mode().Perm(); perm&0o055 != 0o055 {
		if err := os.Chmod(a.PluginsDir, perm|0o055); err != nil {
			return failErr(CodePluginsDir, err, "the plugins directory cannot be made readable by the hub")
		}
	}
	return nil
}

func (a *Applier) install(ctx context.Context, req Request) (from string, aerr *applyError) {
	if !idRE.MatchString(req.Plugin) || len(req.Plugin) < 2 || len(req.Plugin) > 40 {
		return "", fail(CodeBadRequest, "%q is not a plugin id", req.Plugin)
	}
	_, err := update.ParseVersion(req.Version)
	if err != nil || strings.HasPrefix(req.Version, "v") {
		return "", fail(CodeBadRequest, "%q is not a version", req.Version)
	}
	src, ok := a.source(req.Source)
	if !ok {
		return "", fail(CodeUnknownSource, "the source %q is not configured", req.Source)
	}
	if aerr := a.checkPluginsDir(); aerr != nil {
		return "", aerr
	}
	a.step(req.Plugin, req.Action, req.Version, StepVerifying)

	// The index again, from the staged copy, with the source's own signer.
	sdir := stagedDir(a.DataDir, req.Plugin, req.Version)
	indexData, err := readRegular(filepath.Join(sdir, stagedIndex), MaxIndexSize)
	if err != nil {
		return "", failErr(CodeBadRequest, err, "the staged index cannot be read")
	}
	bundle, err := readRegular(filepath.Join(sdir, stagedBundle), maxBundleFile)
	if err != nil {
		return "", failErr(CodeBadRequest, err, "the staged signature cannot be read")
	}
	if a.Verifier == nil {
		return "", fail(CodeNotVerified, "no signature verifier is configured")
	}
	if err := a.Verifier.Verify(ctx, indexData, bundle, src.Signer, src.Issuer); err != nil {
		return "", failErr(CodeNotVerified, err, "the signature of the index of %s is not valid", src.Name)
	}
	ix, err := ParseIndex(indexData, a.now())
	if err != nil {
		return "", failErr(CodeNotVerified, err, "the index cannot be used")
	}
	var iv *IndexVersion
	for i := range ix.Plugins {
		if ix.Plugins[i].ID != req.Plugin {
			continue
		}
		for j := range ix.Plugins[i].Versions {
			if ix.Plugins[i].Versions[j].Version == req.Version {
				iv = &ix.Plugins[i].Versions[j]
			}
		}
	}
	if iv == nil {
		return "", fail(CodeBadRequest, "the index of %s has no %s %s", src.Name, req.Plugin, req.Version)
	}
	hub, hubKnown := update.ParseBuildVersion(a.Version)
	env := Env{Hub: hub, HubKnown: hubKnown, Machine: a.Machine}
	if ok, why := CheckVersion(*iv, env); !ok {
		return "", fail(CodeIncompatible, "%s %s cannot be installed: %s", req.Plugin, req.Version, why)
	}
	if iv.Yanked {
		return "", fail(CodeYanked, "%s %s was withdrawn (yanked)", req.Plugin, req.Version)
	}
	var art *Artifact
	for i := range iv.Artifacts {
		if iv.Artifacts[i].OS == a.Machine.OS && iv.Artifacts[i].Arch == a.Machine.Arch {
			art = &iv.Artifacts[i]
		}
	}
	if art == nil || art.Size <= 0 || art.Size > MaxPackage {
		return "", fail(CodeChecksum, "the index gives no usable package for %s/%s", a.Machine.OS, a.Machine.Arch)
	}
	if len(art.SHA256) != 64 {
		return "", fail(CodeChecksum, "the index gives no checksum for the package")
	}

	// What is installed now: updates come only from the source that installed.
	pdir := filepath.Join(a.PluginsDir, req.Plugin)
	switch info, lerr := os.Lstat(pdir); {
	case lerr == nil && !info.IsDir():
		return "", fail(CodeHandInstalled, "%s is not a directory", pdir)
	case lerr == nil:
		if _, flat := os.Lstat(filepath.Join(pdir, plugin.ManifestName)); flat == nil {
			return "", fail(CodeHandInstalled, "%s was put in the plugins directory by hand; remove it first", req.Plugin)
		}
		if rec, ok := plugin.ReadSourceRecord(pdir); ok {
			from = rec.Version
			if rec.Source != src.Name {
				return from, fail(CodeSourceMismatch, "%s was installed from %s; it is not updated from %s", req.Plugin, rec.Source, src.Name)
			}
		}
	}

	// A copy that only root can change, checked as it is copied: the hub's
	// user could swap the staged file while it is read twice.
	stage, err := os.MkdirTemp(a.PluginsDir, ".stage-")
	if err != nil {
		return from, failErr(CodeInternal, err, "no room to stage the package")
	}
	defer func() { _ = os.RemoveAll(stage) }()
	pkgPath := filepath.Join(stage, "package.tar.gz")
	sum, err := a.copyPackage(filepath.Join(sdir, stagedPackage), pkgPath, art.Size)
	if err != nil {
		return from, failErr(CodeChecksum, err, "the package cannot be read")
	}
	if subtle.ConstantTimeCompare([]byte(sum), []byte(strings.ToLower(art.SHA256))) != 1 {
		return from, fail(CodeChecksum, "the package does not match the checksum of the signed index")
	}
	pf, err := os.Open(pkgPath)
	if err != nil {
		return from, failErr(CodeInternal, err, "cannot read the staged copy")
	}
	defer func() { _ = pf.Close() }()
	a.step(req.Plugin, req.Action, req.Version, StepInstalling)
	unpacked := filepath.Join(stage, req.Plugin)
	if err := unpack(pf, unpacked, req.Plugin, a.Chown); err != nil {
		return from, failErr(CodeUnsafePackage, err, "the package is refused")
	}
	if err := checkUnpacked(unpacked, req.Plugin, req.Version); err != nil {
		return from, err
	}

	// Put it in place: <id>/<version>, then the link, then the record.
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		return from, failErr(CodeInternal, err, "cannot create %s", pdir)
	}
	if err := a.mark(pdir); err != nil {
		return from, failErr(CodeInternal, err, "cannot set the owner of %s", pdir)
	}
	vdir := filepath.Join(pdir, req.Version)
	oldTarget, _ := os.Readlink(filepath.Join(pdir, plugin.CurrentName))
	if oldTarget == req.Version {
		return from, fail(CodeAlreadyInstalled, "%s %s is installed and current", req.Plugin, req.Version)
	}
	if _, err := os.Lstat(vdir); err == nil {
		if err := os.RemoveAll(vdir); err != nil { // a leftover that is not current
			return from, failErr(CodeInternal, err, "cannot replace %s", vdir)
		}
	}
	if err := os.Rename(unpacked, vdir); err != nil {
		return from, failErr(CodeInternal, err, "cannot put the version in place")
	}
	if oldTarget != "" && from == "" {
		from = oldTarget
	}
	if err := switchCurrent(pdir, req.Version); err != nil {
		return from, failErr(CodeInternal, err, "cannot switch to the new version")
	}
	rec := plugin.SourceRecord{Source: src.Name, Version: req.Version, Previous: oldTarget, SHA256: sum, InstalledAt: a.now().UTC()}
	if err := plugin.WriteSourceRecord(pdir, rec); err != nil {
		return from, failErr(CodeInternal, err, "cannot record the source")
	}
	if err := a.mark(filepath.Join(pdir, plugin.SourceFileName)); err != nil {
		return from, failErr(CodeInternal, err, "cannot set the owner of the record")
	}
	pruneVersions(pdir, req.Version, oldTarget)
	return from, nil
}

func (a *Applier) mark(p string) error {
	if a.Chown == nil {
		return nil
	}
	return a.Chown(p)
}

// copyPackage copies the staged package to dst and returns its SHA-256. It
// refuses a file whose size is not the one in the signed index.
func (a *Applier) copyPackage(src, dst string, wantSize int64) (string, error) {
	in, err := openRegular(src)
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() != wantSize {
		return "", fmt.Errorf("the package is %d bytes, the signed index says %d", info.Size(), wantSize)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, wantSize+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if n != wantSize {
		return "", fmt.Errorf("the package changed while it was read")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// checkUnpacked reads the manifest of what was unpacked and checks it says
// what the index said, and that every file it names is there. A manifest with
// only a ui component is accepted: it has no program.
func checkUnpacked(dir, id, version string) *applyError {
	f, err := openRegular(filepath.Join(dir, plugin.ManifestName))
	if err != nil {
		return failErr(CodeBadManifest, err, "plugin.yaml cannot be read")
	}
	defer func() { _ = f.Close() }()
	m, err := plugin.ParseManifest(f)
	if err != nil {
		return failErr(CodeBadManifest, err, "plugin.yaml is not valid")
	}
	if m.ID != id || m.Version != version {
		return fail(CodeBadManifest, "plugin.yaml says %s %s, the index %s %s", m.ID, m.Version, id, version)
	}
	for _, c := range []struct {
		what, rel string
		exe       bool
	}{
		{"the agent program", progCommand(m.Components.Agent), true},
		{"the hub program", progCommand(m.Components.Hub), true},
		{"the ui module", uiModule(m.Components.UI), false},
	} {
		if c.rel == "" {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(c.rel))
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() {
			return fail(CodeBadManifest, "%s %s is not in the package", c.what, c.rel)
		}
		if c.exe && info.Mode().Perm()&0o111 == 0 {
			return fail(CodeBadManifest, "%s %s is not executable", c.what, c.rel)
		}
	}
	return nil
}

func progCommand(p *plugin.Program) string {
	if p == nil {
		return ""
	}
	return p.Command
}

func uiModule(u *plugin.UIModule) string {
	if u == nil {
		return ""
	}
	return u.Module
}

// switchCurrent points <pdir>/current at the version, atomically: a hub that
// looks at the directory sees the old version or the new one.
func switchCurrent(pdir, version string) error {
	tmp := filepath.Join(pdir, ".current-new")
	_ = os.Remove(tmp)
	if err := os.Symlink(version, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(pdir, plugin.CurrentName)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// pruneVersions keeps the current version and the one before it (the
// rollback, not built yet, would use it) and removes the older ones.
func pruneVersions(pdir, current, previous string) {
	entries, err := os.ReadDir(pdir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() || n == current || n == previous {
			continue
		}
		if _, err := update.ParseVersion(n); err != nil {
			continue
		}
		_ = os.RemoveAll(filepath.Join(pdir, n))
	}
}

func (a *Applier) remove(req Request) (from string, aerr *applyError) {
	if !idRE.MatchString(req.Plugin) || len(req.Plugin) < 2 || len(req.Plugin) > 40 {
		return "", fail(CodeBadRequest, "%q is not a plugin id", req.Plugin)
	}
	if aerr := a.checkPluginsDir(); aerr != nil {
		return "", aerr
	}
	a.step(req.Plugin, req.Action, "", StepRemoving)
	pdir := filepath.Join(a.PluginsDir, req.Plugin)
	info, err := os.Lstat(pdir)
	if errors.Is(err, os.ErrNotExist) {
		return "", fail(CodeNotInstalled, "%s is not installed", req.Plugin)
	}
	if err != nil {
		return "", failErr(CodeInternal, err, "cannot read %s", pdir)
	}
	if !info.IsDir() { // a link is removed as a link, never followed
		return "", fail(CodeHandInstalled, "%s is not a directory", pdir)
	}
	if rec, ok := plugin.ReadSourceRecord(pdir); ok {
		from = rec.Version
	}
	if err := os.RemoveAll(pdir); err != nil {
		return from, failErr(CodeInternal, err, "cannot remove %s", pdir)
	}
	return from, nil
}
