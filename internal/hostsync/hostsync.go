package hostsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Options say where things are; Defaults is what a package installs.
type Options struct {
	// UnitDirs are the places a package or an administrator puts units; a unit
	// found in any of them is left alone.
	UnitDirs []string
	// WriteDir is where a missing unit is written.
	WriteDir string
	// PluginsDir is the plugins directory the store's helper writes (root
	// owned); it is also the value given to plugins_dir.
	PluginsDir string
	// EnvFile is the hub's environment file (EnvironmentFile of the service).
	EnvFile string
	// ConfigFile is the hub's configuration file, read only to see whether
	// plugins_dir is set there.
	ConfigFile string
	// Units holds the embedded unit files (packaging.Units).
	Units fs.FS
	// Run runs a command (systemctl); nil uses the real one.
	Run func(ctx context.Context, name string, args ...string) error
}

// Defaults are the paths of a package installation.
func Defaults(units fs.FS) Options {
	return Options{
		UnitDirs:   []string{"/etc/systemd/system", "/usr/lib/systemd/system", "/lib/systemd/system"},
		WriteDir:   "/etc/systemd/system",
		PluginsDir: "/usr/local/lib/regulus/plugins",
		EnvFile:    "/etc/regulus/regulus.env",
		ConfigFile: "/etc/regulus/config.yaml",
		Units:      units,
	}
}

const (
	hubUpdateUnit = "regulus-update.path"
	hubService    = "regulus.service"
	pluginsEnv    = "REGULUS_PLUGINS_DIR"
)

// pluginUnits are the files of the plugin store's helper.
var pluginUnits = []string{"regulus-plugin-apply.path", "regulus-plugin-apply.service"}

func run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Sync brings the machine up to date and returns one line for each thing it
// changed (none when everything was right). It tries every step even when one
// fails, and returns the failures joined; the caller must not let them fail an
// update.
//
// Today: the plugin store's root helper on a hub that installs its updates
// (units, the plugins directory, plugins_dir, the helper enabled).
func Sync(ctx context.Context, o Options) (changes []string, err error) {
	if o.Run == nil {
		o.Run = run
	}
	// Only a hub that installs updates from the dashboard has the root
	// helpers; the installer and the package decide the same way.
	if o.Run(ctx, "systemctl", "is-enabled", "--quiet", hubUpdateUnit) != nil {
		return nil, nil
	}
	var errs []error
	note := func(format string, a ...any) { changes = append(changes, fmt.Sprintf(format, a...)) }
	fail := func(e error) { errs = append(errs, e) }

	wroteUnit := false
	for _, name := range pluginUnits {
		if unitExists(o.UnitDirs, name) {
			continue
		}
		data, rerr := fs.ReadFile(o.Units, name)
		if rerr != nil {
			fail(fmt.Errorf("the embedded %s: %w", name, rerr))
			continue
		}
		if werr := writeFile(filepath.Join(o.WriteDir, name), data, 0o644); werr != nil {
			fail(werr)
			continue
		}
		wroteUnit = true
		note("wrote %s", filepath.Join(o.WriteDir, name))
	}

	// The plugins directory and the directory above it must be readable and
	// searchable by the hub's user: this runs under UMask=0077 (the update
	// helper's), so a directory created here with 0755 would be 0700 and the
	// hub could not read the plugins ("permission denied"), and an installation
	// updated by an earlier version already has them that way.
	for _, dir := range []string{filepath.Dir(o.PluginsDir), o.PluginsDir} {
		msg, derr := ensureReadableDir(dir)
		if derr != nil {
			fail(derr)
		} else if msg != "" {
			note("%s", msg)
		}
	}

	envChanged := false
	if !pluginsDirSet(o) {
		if aerr := appendEnv(o.EnvFile, pluginsEnv+"="+o.PluginsDir); aerr != nil {
			fail(aerr)
		} else {
			envChanged = true
			note("set %s=%s in %s", pluginsEnv, o.PluginsDir, o.EnvFile)
		}
	}

	if wroteUnit {
		if rerr := o.Run(ctx, "systemctl", "daemon-reload"); rerr != nil {
			fail(rerr)
		}
	}
	if o.Run(ctx, "systemctl", "is-enabled", "--quiet", "regulus-plugin-apply.path") != nil {
		if eerr := o.Run(ctx, "systemctl", "enable", "--now", "regulus-plugin-apply.path"); eerr != nil {
			fail(eerr)
		} else {
			note("enabled regulus-plugin-apply.path")
		}
	}
	// The hub reads plugins_dir once, when it starts.
	if envChanged {
		if rerr := o.Run(ctx, "systemctl", "try-restart", hubService); rerr != nil {
			fail(rerr)
		} else {
			note("restarted %s to read plugins_dir", hubService)
		}
	}
	return changes, errors.Join(errs...)
}

// ensureReadableDir creates dir when it is missing (0755, whatever the umask)
// and, when it exists, adds the read and search bits for group and others that
// it lacks. It never removes a permission and never touches the owner: what an
// administrator made stricter than 0755 in another way (a group) is kept.
func ensureReadableDir(dir string) (string, error) {
	st, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		if err := os.Chmod(dir, 0o755); err != nil { // not the umask's
			return "", err
		}
		return fmt.Sprintf("created %s (owned by root: whatever is in it runs as the hub's user)", dir), nil
	}
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	perm := st.Mode().Perm()
	if want := perm | 0o055; want != perm {
		if err := os.Chmod(dir, want); err != nil {
			return "", err
		}
		return fmt.Sprintf("made %s readable by the hub's user (mode %o, was %o)", dir, want, perm), nil
	}
	return "", nil
}

func unitExists(dirs []string, name string) bool {
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, name)); err == nil {
			return true
		}
	}
	return false
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// pluginsDirSet reports whether the administrator (or an earlier run) already
// decided plugins_dir: a line for it in the environment file, even an empty
// one, or the key in the configuration file. The process environment is not
// looked at: this runs under systemd's helper unit, not the hub's.
func pluginsDirSet(o Options) bool {
	if data, err := os.ReadFile(o.EnvFile); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			l := strings.TrimSpace(line)
			l = strings.TrimPrefix(l, "export ")
			if strings.HasPrefix(l, pluginsEnv+"=") {
				return true
			}
		}
	}
	if data, err := os.ReadFile(o.ConfigFile); err == nil {
		var m map[string]any
		if yaml.Unmarshal(data, &m) == nil {
			if _, ok := m["plugins_dir"]; ok {
				return true
			}
		}
	}
	return false
}

// appendEnv adds a line to the environment file, keeping its owner and mode;
// a file that does not exist is created for root and the service's group.
func appendEnv(path, line string) error {
	existing, err := os.ReadFile(path)
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return err
	}
	var b bytes.Buffer
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		b.WriteByte('\n')
	}
	b.WriteString(line + "\n")
	if missing {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.Write(b.Bytes()); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if missing {
		if g, gerr := user.LookupGroup("regulus"); gerr == nil {
			if gid, cerr := strconv.Atoi(g.Gid); cerr == nil {
				_ = os.Chown(path, 0, gid)
			}
		}
	}
	return nil
}
