package store

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/plugin"
	"github.com/CodexCircle/Regulus/internal/update"
)

type entry struct {
	name string
	typ  byte
	mode int64
	body string
	link string
}

// pkg builds a package: a gzip-compressed tar.
func pkg(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: typ, Mode: e.mode, Linkname: e.link}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func manifestYAML(id, version string) string {
	return "id: " + id + "\nversion: " + version + "\nname: Disks\napi: 1\ncomponents:\n  ui:\n    module: ui/disks.js\npermissions:\n  - id: " + id + ".read\n    description: See the disks.\n"
}

// uiOnly is the package of a plugin with only a ui component, like Disks.
func uiOnly(t *testing.T, id, version string) []byte {
	return pkg(t,
		entry{name: id + "/", typ: tar.TypeDir, mode: 0o755},
		entry{name: id + "/plugin.yaml", mode: 0o644, body: manifestYAML(id, version)},
		entry{name: id + "/ui/disks.js", mode: 0o644, body: "export {}"},
	)
}

// env is a hub with a source served by httptest, and the helper in-process.
type env2 struct {
	t       *testing.T
	srv     *httptest.Server
	pkgs    map[string][]byte // version -> package
	yanked  map[string]bool
	data    string
	plugins string
	svc     *Service
	inst    *Installer
	app     *Applier
	ver     *fakeVerifier
	reloads atomic.Int32
	hits    atomic.Int32
	clock   int
}

func newEnv2(t *testing.T, versions ...string) *env2 {
	t.Helper()
	e := &env2{t: t, pkgs: map[string][]byte{}, yanked: map[string]bool{}, data: t.TempDir(), ver: &fakeVerifier{}}
	e.plugins = filepath.Join(t.TempDir(), "plugins")
	if err := os.Mkdir(e.plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, v := range versions {
		e.pkgs[v] = uiOnly(t, "disks", v)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(e.index()) })
	mux.HandleFunc("/index.json.sigstore.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) })
	mux.HandleFunc("/pkg/", func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		v := strings.TrimPrefix(r.URL.Path, "/pkg/")
		if b, ok := e.pkgs[v]; ok {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	})
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	src := Source{Name: "test", URL: e.srv.URL + "/index.json", Trust: TrustCustom, Signer: "s"}
	env := func() Env {
		return Env{Machine: Machine{OS: runtime.GOOS, Arch: runtime.GOARCH}}
	}
	e.svc = New(Options{
		Sources: []Source{src}, Verifier: e.ver, Env: env, Now: e.tick, TTL: time.Nanosecond,
		Installed: func() map[string]InstalledInfo {
			m := map[string]InstalledInfo{}
			for _, p := range plugin.ListInstalled(e.plugins) {
				m[p.ID] = InstalledInfo{Version: p.Version, Source: p.Source}
			}
			return m
		},
	})
	e.svc.o.Sources = []Source{src}
	e.inst = &Installer{Service: e.svc, DataDir: e.data, PluginsDir: e.plugins, Poll: 5 * time.Millisecond, Wait: 5 * time.Second,
		Reload: func() { e.reloads.Add(1) }}
	e.app = &Applier{DataDir: e.data, StateDir: t.TempDir(), PluginsDir: e.plugins, Sources: []Source{src}, Verifier: e.ver,
		Machine: Machine{OS: runtime.GOOS, Arch: runtime.GOARCH}, Now: func() time.Time { return t0 }, OwnerUID: os.Getuid()}
	return e
}

// tick is a clock that moves a second at each look, so a cached index is
// always read again.
func (e *env2) tick() time.Time {
	e.clock++
	return t0.Add(time.Duration(e.clock) * time.Second)
}

func (e *env2) index() []byte {
	ix := Index{Format: 1, Source: "test", IssuedAt: t0.Add(-time.Hour), Expires: t0.Add(24 * time.Hour)}
	p := IndexPlugin{ID: "disks", Name: "Disks"}
	for v, b := range e.pkgs {
		sum := sha256.Sum256(b)
		p.Versions = append(p.Versions, IndexVersion{Version: v, API: 1, Yanked: e.yanked[v],
			Artifacts: []Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, URL: e.srv.URL + "/pkg/" + v, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(b))}}})
	}
	ix.Plugins = []IndexPlugin{p}
	b, _ := json.Marshal(ix)
	return b
}

// helper runs the root helper once, as soon as the hub leaves a request.
func (e *env2) helper() (done chan struct{}) {
	done = make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if RequestPending(ApplyDir(e.data)) {
				_, _ = e.app.Apply(context.Background())
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	return done
}

func (e *env2) do(fn func() (Outcome, error)) (Outcome, error) {
	e.t.Helper()
	d := e.helper()
	out, err := fn()
	<-d
	return out, err
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code() + "/" + e.Helper
	}
	return "?" + err.Error()
}

func TestInstallUIOnlyPluginEndToEnd(t *testing.T) {
	e := newEnv2(t, "0.1.0-alpha.1")
	e.svc.o.HubChannel = func(context.Context) update.Channel { return update.Alpha }
	out, err := e.do(func() (Outcome, error) { return e.inst.Install(context.Background(), "ann", "disks", "", "") })
	if err != nil {
		t.Fatal(err)
	}
	if out.Version != "0.1.0-alpha.1" || out.Source != "test" {
		t.Fatalf("outcome = %+v", out)
	}
	// Layout: <id>/<version>/, current -> <version>, source.json; the module is there.
	pdir := filepath.Join(e.plugins, "disks")
	if tgt, err := os.Readlink(filepath.Join(pdir, "current")); err != nil || tgt != "0.1.0-alpha.1" {
		t.Fatalf("current -> %q, %v", tgt, err)
	}
	if _, err := os.Stat(filepath.Join(pdir, "0.1.0-alpha.1", "ui", "disks.js")); err != nil {
		t.Fatal(err)
	}
	rec, ok := plugin.ReadSourceRecord(pdir)
	if !ok || rec.Source != "test" || rec.Version != "0.1.0-alpha.1" || len(rec.SHA256) != 64 {
		t.Fatalf("record = %+v %v", rec, ok)
	}
	// Discovery follows the link and accepts a manifest with only a ui component.
	found, problems, err := plugin.Discover(e.plugins)
	if err != nil || len(problems) != 0 || len(found) != 1 || !found[0].Versioned || found[0].Manifest.Components.UI == nil ||
		found[0].Dir != filepath.Join(pdir, "0.1.0-alpha.1") {
		t.Fatalf("discover: %+v %v %v", found, problems, err)
	}
	if e.reloads.Load() != 1 {
		t.Errorf("reloads = %d", e.reloads.Load())
	}
	// The catalogue says installed, from this source.
	cat := e.svc.Catalogue(context.Background())
	if len(cat.Plugins) != 1 || cat.Plugins[0].Installed == nil || cat.Plugins[0].Installed.Version != "0.1.0-alpha.1" {
		t.Fatalf("catalogue = %+v", cat.Plugins)
	}
	// The staged files are gone.
	if _, err := os.Stat(StageDir(e.data) + "/disks_0.1.0-alpha.1"); !os.IsNotExist(err) {
		t.Errorf("staged files left: %v", err)
	}
	// Installing again is refused, with a code.
	if _, err := e.inst.Install(context.Background(), "ann", "disks", "", ""); code(err) != "already_installed/" {
		t.Fatalf("second install: %v", err)
	}
}

func TestUpdateKeepsThePreviousVersionAndSwitchesCurrent(t *testing.T) {
	e := newEnv2(t, "0.1.0")
	if _, err := e.do(func() (Outcome, error) { return e.inst.Install(context.Background(), "ann", "disks", "", "") }); err != nil {
		t.Fatal(err)
	}
	// Nothing newer.
	if _, err := e.inst.Update(context.Background(), "ann", "disks", ""); code(err) != "up_to_date/" {
		t.Fatalf("update with nothing newer: %v", err)
	}
	e.pkgs["0.2.0"] = uiOnly(t, "disks", "0.2.0")
	out, err := e.do(func() (Outcome, error) { return e.inst.Update(context.Background(), "ann", "disks", "") })
	if err != nil || out.Version != "0.2.0" || out.From != "0.1.0" {
		t.Fatalf("update: %+v %v", out, err)
	}
	pdir := filepath.Join(e.plugins, "disks")
	if tgt, _ := os.Readlink(filepath.Join(pdir, "current")); tgt != "0.2.0" {
		t.Fatalf("current -> %q", tgt)
	}
	if _, err := os.Stat(filepath.Join(pdir, "0.1.0", "plugin.yaml")); err != nil {
		t.Fatalf("the previous version is not kept: %v", err)
	}
	if rec, _ := plugin.ReadSourceRecord(pdir); rec.Previous != "0.1.0" || rec.Version != "0.2.0" {
		t.Fatalf("record = %+v", rec)
	}
	// A third version prunes the first, keeps the second.
	e.pkgs["0.3.0"] = uiOnly(t, "disks", "0.3.0")
	if _, err := e.do(func() (Outcome, error) { return e.inst.Update(context.Background(), "ann", "disks", "") }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pdir, "0.1.0")); !os.IsNotExist(err) {
		t.Errorf("0.1.0 should have been pruned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pdir, "0.2.0")); err != nil {
		t.Errorf("0.2.0 (the previous) should be kept: %v", err)
	}
}

func TestRemoveDeletesThePluginDirectory(t *testing.T) {
	e := newEnv2(t, "0.1.0")
	if _, err := e.do(func() (Outcome, error) { return e.inst.Install(context.Background(), "ann", "disks", "", "") }); err != nil {
		t.Fatal(err)
	}
	out, err := e.do(func() (Outcome, error) { return e.inst.Remove(context.Background(), "ann", "disks") })
	if err != nil || out.Action != "remove" {
		t.Fatalf("remove: %+v %v", out, err)
	}
	if _, err := os.Lstat(filepath.Join(e.plugins, "disks")); !os.IsNotExist(err) {
		t.Fatalf("the directory is still there: %v", err)
	}
	if _, err := e.inst.Remove(context.Background(), "ann", "disks"); code(err) != "not_installed/" {
		t.Fatalf("second remove: %v", err)
	}
}

func TestInstallRefusals(t *testing.T) {
	t.Run("plugins off", func(t *testing.T) {
		e := newEnv2(t, "0.1.0")
		e.inst.PluginsDir = ""
		if _, err := e.inst.Install(context.Background(), "a", "disks", "", ""); code(err) != "plugins_off/" {
			t.Fatal(err)
		}
	})
	t.Run("a yanked version is not installed, even by name", func(t *testing.T) {
		e := newEnv2(t, "0.1.0")
		e.yanked["0.1.0"] = true
		if _, err := e.inst.Install(context.Background(), "a", "disks", "0.1.0", ""); code(err) != "no_compatible_version/" {
			t.Fatal(err)
		}
		if _, err := e.inst.Install(context.Background(), "a", "disks", "", ""); code(err) != "plugin_not_found/" {
			t.Fatal(err)
		}
	})
	t.Run("unknown plugin and unknown source", func(t *testing.T) {
		e := newEnv2(t, "0.1.0")
		if _, err := e.inst.Install(context.Background(), "a", "nothing", "", ""); code(err) != "plugin_not_found/" {
			t.Fatal(err)
		}
		if _, err := e.inst.Install(context.Background(), "a", "disks", "", "other"); code(err) != "source_unknown/" {
			t.Fatal(err)
		}
	})
	t.Run("a package that is not what the signed index says is never staged", func(t *testing.T) {
		e := newEnv2(t, "0.1.0")
		idx := e.index()
		e.pkgs["0.1.0"] = uiOnly(t, "disks", "0.1.0-evil") // changed after the index was made
		e.svc.st = map[string]*sourceState{}
		_ = idx
		// The index served now matches the new bytes, so tamper in transit instead:
		e.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/pkg/") {
				_, _ = w.Write([]byte("not the package"))
				return
			}
			if r.URL.Path == "/index.json" {
				_, _ = w.Write(idx)
				return
			}
			_, _ = w.Write([]byte("{}"))
		})
		e.pkgs["0.1.0"] = uiOnly(t, "disks", "0.1.0")
		_, err := e.inst.Install(context.Background(), "a", "disks", "", "")
		if code(err) != "download_failed/" {
			t.Fatalf("%v", err)
		}
		if _, err := os.Lstat(filepath.Join(e.plugins, "disks")); !os.IsNotExist(err) {
			t.Fatal("something was installed")
		}
	})
	t.Run("no helper: the request is withdrawn", func(t *testing.T) {
		e := newEnv2(t, "0.1.0")
		e.inst.Wait = 50 * time.Millisecond
		_, err := e.inst.Install(context.Background(), "a", "disks", "", "")
		if code(err) != "helper_timeout/" {
			t.Fatal(err)
		}
		if RequestPending(ApplyDir(e.data)) {
			t.Fatal("the request was left for later")
		}
	})
	t.Run("a pending request makes the hub busy", func(t *testing.T) {
		e := newEnv2(t, "0.1.0")
		_ = os.MkdirAll(ApplyDir(e.data), 0o750)
		_ = WriteRequest(ApplyDir(e.data), Request{ID: "x", Action: ActionRemove, Plugin: "disks"})
		if _, err := e.inst.Install(context.Background(), "a", "disks", "", ""); code(err) != "busy/" {
			t.Fatal(err)
		}
	})
}

func TestHelperRefusesWhatTheHubCouldNotCatch(t *testing.T) {
	// The hub stages a package; the helper must refuse when the signature, the
	// checksum, the manifest or the plugin directory is wrong. Each case stages
	// by hand and runs the helper.
	stage := func(t *testing.T, e *env2, pk []byte, mutate func(*IndexVersion)) {
		t.Helper()
		sum := sha256.Sum256(pk)
		ix := Index{Format: 1, Source: "test", IssuedAt: t0.Add(-time.Hour), Expires: t0.Add(time.Hour), Plugins: []IndexPlugin{{ID: "disks", Name: "D",
			Versions: []IndexVersion{{Version: "0.1.0", API: 1, Artifacts: []Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, URL: "x", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(pk))}}}}}}}
		if mutate != nil {
			mutate(&ix.Plugins[0].Versions[0])
		}
		raw, _ := json.Marshal(ix)
		dir := stagedDir(e.data, "disks", "0.1.0")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for n, b := range map[string][]byte{stagedIndex: raw, stagedBundle: []byte("{}"), stagedPackage: pk} {
			if err := os.WriteFile(filepath.Join(dir, n), b, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(ApplyDir(e.data), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := WriteRequest(ApplyDir(e.data), Request{ID: "r1", Action: ActionInstall, Plugin: "disks", Version: "0.1.0", Source: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	good := func(t *testing.T) []byte { return uiOnly(t, "disks", "0.1.0") }
	cases := []struct {
		name   string
		setup  func(t *testing.T, e *env2)
		result string
	}{
		{"bad signature", func(t *testing.T, e *env2) { e.ver.err = errors.New("no"); stage(t, e, good(t), nil) }, CodeNotVerified},
		{"checksum", func(t *testing.T, e *env2) {
			stage(t, e, good(t), func(v *IndexVersion) { v.Artifacts[0].SHA256 = strings.Repeat("0", 64) })
		}, CodeChecksum},
		{"size", func(t *testing.T, e *env2) { stage(t, e, good(t), func(v *IndexVersion) { v.Artifacts[0].Size++ }) }, CodeChecksum},
		{"yanked", func(t *testing.T, e *env2) { stage(t, e, good(t), func(v *IndexVersion) { v.Yanked = true }) }, CodeYanked},
		{"api too new", func(t *testing.T, e *env2) { stage(t, e, good(t), func(v *IndexVersion) { v.API = 99 }) }, CodeIncompatible},
		{"manifest says another version", func(t *testing.T, e *env2) { stage(t, e, uiOnly(t, "disks", "0.9.9"), nil) }, CodeBadManifest},
		{"manifest names a module that is not there", func(t *testing.T, e *env2) {
			stage(t, e, pkg(t, entry{name: "disks/plugin.yaml", mode: 0o644, body: manifestYAML("disks", "0.1.0")}), nil)
		}, CodeBadManifest},
		{"a program that is not executable", func(t *testing.T, e *env2) {
			m := "id: disks\nversion: 0.1.0\nname: D\napi: 1\ncomponents:\n  hub:\n    command: bin/x\n"
			stage(t, e, pkg(t, entry{name: "disks/plugin.yaml", mode: 0o644, body: m}, entry{name: "disks/bin/x", mode: 0o644, body: "x"}), nil)
		}, CodeBadManifest},
		{"plugins directory writable by others", func(t *testing.T, e *env2) {
			if err := os.Chmod(e.plugins, 0o777); err != nil {
				t.Fatal(err)
			}
			stage(t, e, good(t), nil)
		}, CodePluginsDir},
		{"plugins directory owned by someone else", func(t *testing.T, e *env2) { e.app.OwnerUID = os.Getuid() + 1; stage(t, e, good(t), nil) }, CodePluginsDir},
		{"installed by hand", func(t *testing.T, e *env2) {
			_ = os.MkdirAll(filepath.Join(e.plugins, "disks"), 0o755)
			_ = os.WriteFile(filepath.Join(e.plugins, "disks", "plugin.yaml"), []byte(manifestYAML("disks", "0.0.1")), 0o644)
			stage(t, e, good(t), nil)
		}, CodeHandInstalled},
		{"installed from another source", func(t *testing.T, e *env2) {
			p := filepath.Join(e.plugins, "disks")
			_ = os.MkdirAll(p, 0o755)
			_ = plugin.WriteSourceRecord(p, plugin.SourceRecord{Source: "official", Version: "0.0.1"})
			stage(t, e, good(t), nil)
		}, CodeSourceMismatch},
		{"unknown source", func(t *testing.T, e *env2) { e.app.Sources = nil; stage(t, e, good(t), nil) }, CodeUnknownSource},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv2(t, "0.1.0")
			c.setup(t, e)
			res, err := e.app.Apply(context.Background())
			if err == nil || res.Outcome != OutcomeFailed || res.Code != c.result {
				t.Fatalf("result = %+v, err = %v; want code %s", res, err, c.result)
			}
			got, ok := ReadResult(ApplyDir(e.data))
			if !ok || got.Code != c.result || got.RequestID != "r1" {
				t.Fatalf("written result = %+v %v", got, ok)
			}
			if RequestPending(ApplyDir(e.data)) {
				t.Fatal("the request was left in place: systemd would run the helper again")
			}
			if _, err := os.Lstat(filepath.Join(e.plugins, "disks", "current")); err == nil {
				t.Fatal("a refused package was made current")
			}
			if left, _ := filepath.Glob(filepath.Join(e.plugins, ".stage-*")); len(left) != 0 {
				t.Fatalf("staging left behind: %v", left)
			}
		})
	}
}

func TestHelperWithNoRequestAndWithGarbage(t *testing.T) {
	e := newEnv2(t, "0.1.0")
	if _, err := e.app.Apply(context.Background()); !errors.Is(err, ErrNoRequest) {
		t.Fatalf("err = %v", err)
	}
	_ = os.MkdirAll(ApplyDir(e.data), 0o750)
	_ = os.WriteFile(filepath.Join(ApplyDir(e.data), requestName), []byte("not json"), 0o600)
	if _, err := e.app.Apply(context.Background()); err == nil {
		t.Fatal("garbage was accepted")
	}
	if RequestPending(ApplyDir(e.data)) {
		t.Fatal("garbage left in place")
	}
	// A link in the place of the request is removed, never followed.
	target := filepath.Join(t.TempDir(), "secret")
	_ = os.WriteFile(target, []byte(`{"action":"remove","plugin":"disks"}`), 0o600)
	_ = os.Symlink(target, filepath.Join(ApplyDir(e.data), requestName))
	if _, err := e.app.Apply(context.Background()); err == nil {
		t.Fatal("a symbolic link was followed")
	}
	if _, err := os.Lstat(target); err != nil {
		t.Fatal("the target of the link was touched")
	}
}

func TestRemoveRefusesALinkAndABadId(t *testing.T) {
	e := newEnv2(t, "0.1.0")
	victim := t.TempDir()
	_ = os.WriteFile(filepath.Join(victim, "keep"), []byte("x"), 0o600)
	_ = os.Symlink(victim, filepath.Join(e.plugins, "disks"))
	_ = os.MkdirAll(ApplyDir(e.data), 0o750)
	for _, id := range []string{"disks", "../disks", ".", "a/b"} {
		_ = WriteRequest(ApplyDir(e.data), Request{ID: "r", Action: ActionRemove, Plugin: id})
		if _, err := e.app.Apply(context.Background()); err == nil {
			t.Errorf("remove %q was accepted", id)
		}
	}
	if _, err := os.Stat(filepath.Join(victim, "keep")); err != nil {
		t.Fatal("the target of a link was removed")
	}
}

func TestLoadSources(t *testing.T) {
	dir := t.TempDir()
	write := func(mode os.FileMode, body string) string {
		p := filepath.Join(dir, "c.yaml")
		_ = os.Remove(p)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if s, err := LoadSources(filepath.Join(dir, "missing"), false); err != nil || s != nil {
		t.Fatalf("missing file: %v %v", s, err)
	}
	good := "listen: x\nplugin_sources:\n  - {name: mine, url: 'https://x/index.json', trust: custom, signer: 'https://s'}\n"
	if s, err := LoadSources(write(0o644, good), false); err != nil || len(s) != 1 || s[0].Name != "mine" {
		t.Fatalf("good: %v %v", s, err)
	}
	if _, err := LoadSources(write(0o666, good), false); err == nil {
		t.Fatal("a writable configuration was trusted")
	}
	if _, err := LoadSources(write(0o644, "plugin_sources:\n  - {name: mine, url: 'x', trust: custom}\n"), false); err == nil {
		t.Fatal("a source without a signer was accepted")
	}
}
