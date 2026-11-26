package hostsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

var units = fstest.MapFS{
	"regulus-plugin-apply.path":    {Data: []byte("[Path]\n")},
	"regulus-plugin-apply.service": {Data: []byte("[Service]\n")},
}

type rig struct {
	o       Options
	dir     string
	calls   []string
	enabled map[string]bool
}

// newRig is a hub that installs its updates (regulus-update.path enabled) and
// has nothing of the plugin store, as one updated from the first release.
func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	r := &rig{dir: dir, enabled: map[string]bool{hubUpdateUnit: true}}
	r.o = Options{
		UnitDirs:   []string{filepath.Join(dir, "etc-systemd"), filepath.Join(dir, "usr-systemd")},
		WriteDir:   filepath.Join(dir, "etc-systemd"),
		PluginsDir: filepath.Join(dir, "lib", "plugins"),
		EnvFile:    filepath.Join(dir, "etc", "regulus.env"),
		ConfigFile: filepath.Join(dir, "etc", "config.yaml"),
		Units:      units,
		Run: func(_ context.Context, name string, args ...string) error {
			r.calls = append(r.calls, name+" "+strings.Join(args, " "))
			if len(args) >= 3 && args[0] == "is-enabled" {
				if r.enabled[args[2]] {
					return nil
				}
				return errors.New("disabled")
			}
			if len(args) >= 3 && args[0] == "enable" {
				r.enabled[args[2]] = true
			}
			return nil
		},
	}
	return r
}

func (r *rig) called(prefix string) bool {
	for _, c := range r.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func TestAnOldInstallationGetsTheHelper(t *testing.T) {
	r := newRig(t)
	if err := os.MkdirAll(filepath.Dir(r.o.EnvFile), 0o755); err != nil {
		t.Fatal(err)
	}
	// An environment file without a newline at its end, as an editor may leave it.
	if err := os.WriteFile(r.o.EnvFile, []byte("REGULUS_UPDATES_APPLY=true"), 0o640); err != nil {
		t.Fatal(err)
	}
	changes, err := Sync(context.Background(), r.o)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range pluginUnits {
		if _, err := os.Stat(filepath.Join(r.o.WriteDir, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
	if st, err := os.Stat(r.o.PluginsDir); err != nil || !st.IsDir() || st.Mode().Perm() != 0o755 {
		t.Errorf("the plugins directory: %v, %v", st, err)
	}
	env, _ := os.ReadFile(r.o.EnvFile)
	if string(env) != "REGULUS_UPDATES_APPLY=true\nREGULUS_PLUGINS_DIR="+r.o.PluginsDir+"\n" {
		t.Errorf("the environment file is %q", env)
	}
	if st, _ := os.Stat(r.o.EnvFile); st.Mode().Perm() != 0o640 {
		t.Errorf("the environment file's mode changed to %v", st.Mode().Perm())
	}
	for _, want := range []string{"systemctl daemon-reload", "systemctl enable --now regulus-plugin-apply.path", "systemctl try-restart regulus.service"} {
		if !r.called(want) {
			t.Errorf("did not run %q (ran %v)", want, r.calls)
		}
	}
	if len(changes) != 6 {
		t.Errorf("changes = %v", changes)
	}
}

func TestSecondRunChangesNothing(t *testing.T) {
	r := newRig(t)
	if _, err := Sync(context.Background(), r.o); err != nil {
		t.Fatal(err)
	}
	r.calls = nil
	changes, err := Sync(context.Background(), r.o)
	if err != nil || len(changes) != 0 {
		t.Fatalf("second run: %v, %v", changes, err)
	}
	for _, c := range r.calls {
		if !strings.HasPrefix(c, "systemctl is-enabled") {
			t.Errorf("a second run ran %q", c)
		}
	}
}

func TestNeverOverridesWhatTheAdministratorChose(t *testing.T) {
	cases := map[string]func(*rig){
		"a line in the environment file, even an empty one": func(r *rig) {
			_ = os.MkdirAll(filepath.Dir(r.o.EnvFile), 0o755)
			_ = os.WriteFile(r.o.EnvFile, []byte("REGULUS_PLUGINS_DIR=\n"), 0o640)
		},
		"another directory in the environment file": func(r *rig) {
			_ = os.MkdirAll(filepath.Dir(r.o.EnvFile), 0o755)
			_ = os.WriteFile(r.o.EnvFile, []byte("export REGULUS_PLUGINS_DIR=/opt/plugins\n"), 0o640)
		},
		"the key in config.yaml": func(r *rig) {
			_ = os.MkdirAll(filepath.Dir(r.o.ConfigFile), 0o755)
			_ = os.WriteFile(r.o.ConfigFile, []byte("listen: 127.0.0.1:8080\nplugins_dir: /opt/plugins\n"), 0o640)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			setup(r)
			before, _ := os.ReadFile(r.o.EnvFile)
			if _, err := Sync(context.Background(), r.o); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(r.o.EnvFile)
			if string(before) != string(after) {
				t.Errorf("the environment file changed: %q -> %q", before, after)
			}
			if r.called("systemctl try-restart") {
				t.Error("restarted the hub though no setting changed")
			}
		})
	}
}

func TestACommentedKeyIsNotAChoice(t *testing.T) {
	r := newRig(t)
	_ = os.MkdirAll(filepath.Dir(r.o.ConfigFile), 0o755)
	// The packaged config.yaml has the line commented out.
	_ = os.WriteFile(r.o.ConfigFile, []byte("# plugins_dir: /usr/local/lib/regulus/plugins\n"), 0o640)
	if _, err := Sync(context.Background(), r.o); err != nil {
		t.Fatal(err)
	}
	if env, _ := os.ReadFile(r.o.EnvFile); !strings.Contains(string(env), "REGULUS_PLUGINS_DIR=") {
		t.Errorf("plugins_dir was not set: %q", env)
	}
}

func TestAUnitFromThePackageIsNotCopied(t *testing.T) {
	r := newRig(t)
	pkg := r.o.UnitDirs[1]
	_ = os.MkdirAll(pkg, 0o755)
	for _, name := range pluginUnits {
		_ = os.WriteFile(filepath.Join(pkg, name), []byte("from the package\n"), 0o644)
	}
	if _, err := Sync(context.Background(), r.o); err != nil {
		t.Fatal(err)
	}
	for _, name := range pluginUnits {
		if _, err := os.Stat(filepath.Join(r.o.WriteDir, name)); err == nil {
			t.Errorf("%s was written next to the package's", name)
		}
	}
	if r.called("systemctl daemon-reload") {
		t.Error("reloaded systemd though no unit was written")
	}
}

func TestOnlyAHubThatInstallsItsUpdates(t *testing.T) {
	r := newRig(t)
	r.enabled[hubUpdateUnit] = false // an agent, or a hub whose updates are by hand
	changes, err := Sync(context.Background(), r.o)
	if err != nil || len(changes) != 0 {
		t.Fatalf("%v, %v", changes, err)
	}
	if _, err := os.Stat(r.o.PluginsDir); err == nil {
		t.Error("created the plugins directory on a machine that is not such a hub")
	}
	if len(r.calls) != 1 {
		t.Errorf("ran %v", r.calls)
	}
}

func TestAFailureDoesNotStopTheOtherSteps(t *testing.T) {
	r := newRig(t)
	good := r.o.Run
	r.o.Run = func(ctx context.Context, name string, args ...string) error {
		if len(args) > 0 && args[0] == "daemon-reload" {
			return errors.New("no bus")
		}
		return good(ctx, name, args...)
	}
	changes, err := Sync(context.Background(), r.o)
	if err == nil || !strings.Contains(err.Error(), "no bus") {
		t.Fatalf("the failure was not returned: %v", err)
	}
	if !r.called("systemctl enable --now regulus-plugin-apply.path") || len(changes) == 0 {
		t.Errorf("the steps after the failure did not run: %v %v", changes, r.calls)
	}
}
