package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/plugin/sdk"
)

// The test binary doubles as a plugin: started with REGULUS_TEST_PLUGIN set,
// it behaves as the mode says instead of running the tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv("REGULUS_TEST_PLUGIN"); mode != "" {
		os.Exit(fakePlugin(mode))
	}
	os.Exit(m.Run())
}

func fakePlugin(mode string) int {
	id, version := os.Getenv(sdk.EnvID), "0.1.0"
	health := func(context.Context) error { return nil }
	switch mode {
	case "crash":
		fmt.Fprintln(os.Stderr, "crashing on purpose")
		return 2
	case "crash-after-start":
		go func() { time.Sleep(200 * time.Millisecond); os.Exit(3) }()
	case "wrong-id":
		id = "someone-else"
	case "unhealthy":
		health = func(context.Context) error { return errors.New("the disk is full") }
	case "no-socket":
		// It also ignores SIGTERM: only the SIGKILL after the grace period
		// ends it.
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(time.Hour)
		return 0
	case "env":
		// It writes what it was given, to be checked against the host's.
		_ = os.WriteFile(filepath.Join(os.Getenv(sdk.EnvDataDir), "env"), []byte(strings.Join(os.Environ(), "\n")), 0o600)
	case "spawns-child":
		// A child the plugin starts must not outlive it.
		child := exec.Command("sleep", "300")
		_ = child.Start()
		_ = os.WriteFile(filepath.Join(os.Getenv(sdk.EnvDataDir), "child.pid"), []byte(fmt.Sprint(child.Process.Pid)), 0o600)
	}
	if err := sdk.Serve(context.Background(), sdk.Plugin{ID: id, Version: version, APIVersion: 1, Health: health}, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// pluginDir makes a plugin directory whose command is a copy of the test
// binary, started in the given mode.
func pluginDir(t *testing.T, root, id, mode string, roles ...string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "bin", "plugin")
	if err := os.Link(self, exe); err != nil {
		data, rerr := os.ReadFile(self)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if err := os.WriteFile(exe, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The mode travels as an argument the wrapper turns into the variable:
	// the supervisor gives a plugin a clean environment.
	wrapper := fmt.Sprintf("#!/bin/sh\nREGULUS_TEST_PLUGIN=%s exec \"$(dirname \"$0\")/plugin\" \"$@\"\n", mode)
	if err := os.WriteFile(filepath.Join(dir, "bin", "run"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if len(roles) == 0 {
		roles = []string{RoleAgent}
	}
	var comps strings.Builder
	for _, r := range roles {
		fmt.Fprintf(&comps, "  %s:\n    command: bin/run\n", r)
	}
	writeManifest(t, dir, fmt.Sprintf("id: %s\nversion: 0.1.0\nname: %s\napi: 1\ncomponents:\n%s", id, id, comps.String()))
}

type runningSupervisor struct {
	*Supervisor
	cancel context.CancelFunc
	done   chan error
}

func startSupervisor(t *testing.T, root string, mutate ...func(*Supervisor)) *runningSupervisor {
	t.Helper()
	run, err := os.MkdirTemp("", "rp") // short: a socket path has a length limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(run) })
	s := &Supervisor{
		Dir: root, RunDir: run, DataDir: t.TempDir(), Role: RoleAgent, CoreVersion: "0.3.0",
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		StartTimeout: 5 * time.Second, HealthEvery: 50 * time.Millisecond, HealthTimeout: time.Second,
		MinBackoff: 20 * time.Millisecond, MaxBackoff: 50 * time.Millisecond, StableAfter: time.Hour,
		MaxRestarts: 3, RestartWindow: time.Minute, Grace: 2 * time.Second,
	}
	for _, m := range mutate {
		m(s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rs := &runningSupervisor{Supervisor: s, cancel: cancel, done: make(chan error, 1)}
	go func() { rs.done <- s.Run(ctx) }()
	t.Cleanup(rs.stop)
	return rs
}

var stopOnce sync.Map

func (rs *runningSupervisor) stop() {
	if _, done := stopOnce.LoadOrStore(rs, true); done {
		return
	}
	rs.cancel()
	if err := <-rs.done; err != nil {
		panic(fmt.Sprintf("the supervisor returned %v", err))
	}
}

func (rs *runningSupervisor) state(id string) Status {
	for _, st := range rs.Status() {
		if st.ID == id {
			return st
		}
	}
	return Status{}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

// A plugin that crashes, again and again, is given up on; the other plugins
// and the supervisor itself are not affected (#19: a crash does not affect
// the host).
func TestACrashingPluginDoesNotAffectTheOthers(t *testing.T) {
	root := t.TempDir()
	pluginDir(t, root, "good", "ok")
	pluginDir(t, root, "bad", "crash-after-start")
	rs := startSupervisor(t, root)

	waitFor(t, "the good plugin to run", func() bool { return rs.state("good").State == StateRunning })
	pid := rs.state("good").PID
	waitFor(t, "the bad plugin to be given up on", func() bool { return rs.state("bad").State == StateFailed })
	bad := rs.state("bad")
	if bad.Restarts != 3 || !strings.Contains(bad.LastError, "exit status 3") {
		t.Errorf("bad = %+v", bad)
	}
	if st := rs.state("good"); st.State != StateRunning || st.PID != pid || st.Restarts != 0 {
		t.Errorf("good = %+v, want still running as pid %d", st, pid)
	}
	select {
	case err := <-rs.done:
		t.Fatalf("the supervisor stopped: %v", err)
	default:
	}
	rs.stop()
	if alive(pid) {
		t.Error("the good plugin outlived the supervisor")
	}
	if st := rs.state("good"); st.State != StateStopped {
		t.Errorf("after stop: %+v", st)
	}
}

func TestAPluginThatIsNotWhatItsManifestSaysIsNotRun(t *testing.T) {
	root := t.TempDir()
	pluginDir(t, root, "impostor", "wrong-id")
	rs := startSupervisor(t, root)
	waitFor(t, "the impostor to be given up on", func() bool { return rs.state("impostor").State == StateFailed })
	if st := rs.state("impostor"); !strings.Contains(st.LastError, "someone-else") {
		t.Errorf("status = %+v", st)
	}
}

func TestAnUnhealthyPluginIsRestarted(t *testing.T) {
	root := t.TempDir()
	pluginDir(t, root, "sick", "unhealthy")
	rs := startSupervisor(t, root)
	waitFor(t, "a restart", func() bool { return rs.state("sick").Restarts >= 1 })
	if st := rs.state("sick"); !strings.Contains(st.LastError, "the disk is full") {
		t.Errorf("status = %+v", st)
	}
}

func TestAPluginThatNeverOpensItsSocketIsStopped(t *testing.T) {
	root := t.TempDir()
	pluginDir(t, root, "mute", "no-socket")
	rs := startSupervisor(t, root, func(s *Supervisor) { s.StartTimeout, s.Grace = 300*time.Millisecond, 200*time.Millisecond })
	waitFor(t, "the first start", func() bool { return rs.state("mute").PID > 0 })
	first := rs.state("mute").PID
	waitFor(t, "a restart", func() bool { return rs.state("mute").Restarts >= 1 })
	if st := rs.state("mute"); !strings.Contains(st.LastError, "did not open its socket") {
		t.Errorf("status = %+v", st)
	}
	// It ignored SIGTERM; the SIGKILL after the grace period ended it.
	waitFor(t, "the first process to be gone", func() bool { return !alive(first) || zombie(first) })
}

func TestStoppingAPluginStopsWhatItStarted(t *testing.T) {
	root := t.TempDir()
	pluginDir(t, root, "parent", "spawns-child")
	rs := startSupervisor(t, root)
	waitFor(t, "the plugin to run", func() bool { return rs.state("parent").State == StateRunning })
	var child int
	waitFor(t, "the child's pid", func() bool {
		data, err := os.ReadFile(filepath.Join(rs.DataDir, "parent", "child.pid"))
		_, _ = fmt.Sscan(string(data), &child)
		return err == nil && child > 0
	})
	rs.stop()
	waitFor(t, "the child to be gone", func() bool { return !alive(child) || zombie(child) })
}

func zombie(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	f := strings.Fields(string(data))
	return len(f) > 2 && f[2] == "Z"
}

func TestOnlyTheComponentOfTheRoleIsStarted(t *testing.T) {
	root := t.TempDir()
	pluginDir(t, root, "hub-only", "ok", RoleHub)
	pluginDir(t, root, "both", "ok", RoleAgent, RoleHub)
	rs := startSupervisor(t, root)
	waitFor(t, "the agent component", func() bool { return rs.state("both").State == StateRunning })
	if st := rs.state("hub-only"); st.ID != "" {
		t.Errorf("a hub-only plugin was started on an agent: %+v", st)
	}
}

func TestAnIncompatiblePluginIsNeverStarted(t *testing.T) {
	root := t.TempDir()
	pluginDir(t, root, "future", "ok")
	writeManifest(t, filepath.Join(root, "future"), "id: future\nversion: 0.1.0\nname: F\napi: 1\nrequires: \">=9.0.0\"\ncomponents:\n  agent:\n    command: bin/run\n")
	rs := startSupervisor(t, root)
	waitFor(t, "the status", func() bool { return rs.state("future").State == StateIncompatible })
	if st := rs.state("future"); st.PID != 0 || !strings.Contains(st.LastError, "needs Regulus") {
		t.Errorf("status = %+v", st)
	}
}

func TestACommandThatLeadsOutsideThePluginIsRefused(t *testing.T) {
	root := t.TempDir()
	pluginDir(t, root, "escape", "ok")
	outside := filepath.Join(t.TempDir(), "evil")
	if err := os.WriteFile(outside, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(root, "escape", "bin", "run")
	_ = os.Remove(run)
	if err := os.Symlink(outside, run); err != nil {
		t.Fatal(err)
	}
	rs := startSupervisor(t, root)
	waitFor(t, "a refusal", func() bool { return strings.Contains(rs.state("escape").LastError, "outside the plugin directory") })
}

// The example plugin of the repository, built from source, is found,
// validated and run (#19, "Done when").
func TestTheExamplePluginRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	root := t.TempDir()
	src := filepath.Join("..", "..", "plugins", "example-hello")
	dir := filepath.Join(root, "example-hello")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(src, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	writeManifest(t, dir, string(manifest))
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "bin", "example-hello"), "./"+filepath.ToSlash(src))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the example: %v\n%s", err, out)
	}
	rs := startSupervisor(t, root)
	waitFor(t, "the example to run", func() bool { return rs.state("example-hello").State == StateRunning })
	time.Sleep(200 * time.Millisecond) // a few health checks
	if st := rs.state("example-hello"); st.State != StateRunning || st.Restarts != 0 || st.Version != "0.1.0" {
		t.Errorf("status = %+v", st)
	}
}

// A plugin gets the variables the supervisor sets and nothing of the host's
// environment: on the hub that environment holds the database password
// (REGULUS_DATABASE_PASSWORD, #262).
func TestAPluginGetsACleanEnvironment(t *testing.T) {
	const secret = "the-database-password"
	t.Setenv("REGULUS_DATABASE_PASSWORD", secret)
	t.Setenv("HOME", "/root/secret-home")
	root := t.TempDir()
	pluginDir(t, root, "nosy", "env", RoleHub)
	rs := startSupervisor(t, root, func(s *Supervisor) { s.Role = RoleHub })
	waitFor(t, "the plugin to run", func() bool { return rs.state("nosy").State == StateRunning })
	data, err := os.ReadFile(filepath.Join(rs.DataDir, "nosy", "env"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, _ := strings.Cut(line, "=")
		got[k] = v
	}
	// The wrapper script adds its own mode, and the shell PWD and SHLVL.
	for _, k := range []string{"REGULUS_TEST_PLUGIN", "PWD", "SHLVL", "_", "OLDPWD"} {
		delete(got, k)
	}
	want := map[string]string{
		"PATH":             "/usr/local/bin:/usr/bin:/bin",
		sdk.EnvSocket:      filepath.Join(rs.RunDir, "nosy.sock"),
		sdk.EnvID:          "nosy",
		sdk.EnvRole:        RoleHub,
		sdk.EnvCoreVersion: "0.3.0",
		sdk.EnvDataDir:     filepath.Join(rs.DataDir, "nosy"),
	}
	if len(got) != len(want) {
		t.Errorf("environment = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if strings.Contains(string(data), secret) {
		t.Error("the database password reached the plugin")
	}
}
