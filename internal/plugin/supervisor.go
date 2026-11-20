// supervisor.go runs the plugins of one role (agent or hub) as separate
// processes. Reading order: Supervisor and its defaults, Run, run (the life of
// one plugin: start, check, watch, restart), start, check, watch, stop, then
// the helpers. See docs/plugins/runtime.md.

package plugin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pluginv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/plugin/v1"
	"github.com/CodexCircle/Regulus/internal/plugin/sdk"
)

// States of a plugin, as Status reports them.
const (
	// StateStarting: the process was started and has not proved itself yet.
	StateStarting = "starting"
	// StateRunning: it answered Describe as its manifest says and is healthy.
	StateRunning = "running"
	// StateRestarting: it stopped or failed a check and is started again
	// after a pause.
	StateRestarting = "restarting"
	// StateFailed: it failed too often and is not started again until the
	// host restarts. The reason is in Status.
	StateFailed = "failed"
	// StateIncompatible: its manifest is valid but it cannot run on this
	// Regulus (API or core version). It is never started.
	StateIncompatible = "incompatible"
	// StateStopped: the host stopped it.
	StateStopped = "stopped"
	// StateInvalid: a directory in the plugins directory that is not a
	// usable plugin (no plugin.yaml, an invalid one, a directory not named
	// after the id, a symbolic link). Its ID is the directory's name, its
	// Version is empty and LastError says why. It is never started, and it is
	// listed by every role, since its manifest cannot say which it is for.
	StateInvalid = "invalid"
)

// Status is the state of one plugin.
type Status struct {
	ID        string
	Version   string
	State     string
	Restarts  int
	LastError string
	PID       int
	Since     time.Time
}

// Supervisor runs the component of one role of every plugin in Dir.
type Supervisor struct {
	// Dir is the plugins directory (see Discover).
	Dir string
	// RunDir holds the sockets; it is created with mode 0700. Keep its path
	// short: a Unix socket path is limited to about 100 bytes.
	RunDir string
	// DataDir, if set, is where each plugin gets a directory of its own
	// (<DataDir>/<id>, mode 0700), named in REGULUS_PLUGIN_DATA_DIR.
	DataDir string
	// Role is RoleAgent or RoleHub.
	Role string
	// CoreVersion is the version of Regulus, checked against `requires`.
	CoreVersion string
	Log         *slog.Logger

	// StartTimeout is how long a started plugin has to answer Describe.
	StartTimeout time.Duration
	// HealthEvery is how often Health is asked, and HealthTimeout how long an
	// answer may take. HealthFailures failures in a row restart the plugin.
	HealthEvery, HealthTimeout time.Duration
	HealthFailures             int
	// MinBackoff and MaxBackoff bound the pause before a restart; it doubles
	// after each failure and starts over after a plugin ran for StableAfter.
	MinBackoff, MaxBackoff, StableAfter time.Duration
	// MaxRestarts within RestartWindow make the plugin failed for good.
	MaxRestarts   int
	RestartWindow time.Duration
	// Grace is how long a plugin gets to exit after SIGTERM before SIGKILL.
	Grace time.Duration
	// OnChange, if set, is called after the state, the restart count or the
	// last error of a plugin changed, outside the supervisor's lock (the
	// hub pushes it on its live stream). It must not block.
	OnChange func()

	mu     sync.Mutex
	status map[string]*Status
}

func (s *Supervisor) defaults() {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	set := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	set(&s.StartTimeout, 10*time.Second)
	set(&s.HealthEvery, 15*time.Second)
	set(&s.HealthTimeout, 5*time.Second)
	set(&s.MinBackoff, time.Second)
	set(&s.MaxBackoff, time.Minute)
	set(&s.StableAfter, time.Minute)
	set(&s.RestartWindow, 10*time.Minute)
	set(&s.Grace, 10*time.Second)
	if s.HealthFailures <= 0 {
		s.HealthFailures = 3
	}
	if s.MaxRestarts <= 0 {
		s.MaxRestarts = 5
	}
}

// Run discovers the plugins and runs those that have a component for this
// role, each on its own, until ctx ends; then it stops them all and returns.
// Nothing a plugin does makes Run fail: a bad manifest, a plugin that does
// not start or one that crashes is logged and shown in Status. Only a plugins
// directory that cannot be read is an error.
func (s *Supervisor) Run(ctx context.Context) error {
	s.defaults()
	if s.Role != RoleAgent && s.Role != RoleHub {
		return fmt.Errorf("unknown plugin role %q", s.Role)
	}
	found, problems, err := Discover(s.Dir)
	if err != nil {
		return fmt.Errorf("reading the plugins directory: %w", err)
	}
	for _, p := range problems {
		s.Log.Error("a plugin cannot be used", "dir", p.Dir, "err", p.Err)
		s.set(Manifest{ID: filepath.Base(p.Dir)}, func(st *Status) { st.State, st.LastError = StateInvalid, p.Err.Error() })
	}
	if err := os.MkdirAll(s.RunDir, 0o700); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, f := range found {
		prog := f.Manifest.Program(s.Role)
		if prog == nil {
			continue
		}
		if err := f.Manifest.Compatible(s.CoreVersion); err != nil {
			s.set(f.Manifest, func(st *Status) { st.State, st.LastError = StateIncompatible, err.Error() })
			s.Log.Warn("a plugin cannot run on this Regulus", "plugin", f.Manifest.ID, "version", f.Manifest.Version, "err", err)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.run(ctx, f, prog)
		}()
	}
	wg.Wait()
	return nil
}

// Status returns the state of every plugin of this role, by id.
func (s *Supervisor) Status() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Status, 0, len(s.status))
	for _, st := range s.status {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Supervisor) set(m Manifest, fn func(*Status)) {
	s.mu.Lock()
	if s.status == nil {
		s.status = map[string]*Status{}
	}
	st := s.status[m.ID]
	if st == nil {
		st = &Status{ID: m.ID, Version: m.Version}
		s.status[m.ID] = st
	}
	old := *st
	fn(st)
	if st.State != old.State {
		st.Since = time.Now()
	}
	changed := st.State != old.State || st.Restarts != old.Restarts || st.LastError != old.LastError
	s.mu.Unlock()
	if changed && s.OnChange != nil {
		s.OnChange()
	}
}

// run is the life of one plugin: start it, check it, watch it, and when it
// stops or fails start it again after a pause, until it failed too often or
// ctx ends.
func (s *Supervisor) run(ctx context.Context, f Found, prog *Program) {
	m := f.Manifest
	log := s.Log.With("plugin", m.ID, "version", m.Version)
	backoff := s.MinBackoff
	var failures []time.Time
	for {
		s.set(m, func(st *Status) { st.State, st.PID = StateStarting, 0 })
		started := time.Now()
		err := s.once(ctx, f, prog, log)
		if ctx.Err() != nil {
			s.set(m, func(st *Status) { st.State, st.PID = StateStopped, 0 })
			return
		}
		if time.Since(started) >= s.StableAfter {
			backoff = s.MinBackoff
		}
		now := time.Now()
		failures = append(failures, now)
		for len(failures) > 0 && now.Sub(failures[0]) > s.RestartWindow {
			failures = failures[1:]
		}
		if len(failures) > s.MaxRestarts {
			msg := fmt.Sprintf("stopped %d times within %s; not started again until Regulus restarts: %v", len(failures), s.RestartWindow, err)
			s.set(m, func(st *Status) { st.State, st.LastError, st.PID = StateFailed, msg, 0 })
			log.Error("plugin failed too often; giving up", "err", err)
			return
		}
		wait := backoff/2 + rand.N(backoff/2+1)
		s.set(m, func(st *Status) {
			st.State, st.PID, st.Restarts = StateRestarting, 0, st.Restarts+1
			if err != nil {
				st.LastError = err.Error()
			}
		})
		log.Warn("plugin stopped; starting it again", "err", err, "retry_in", wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			s.set(m, func(st *Status) { st.State = StateStopped })
			return
		}
		backoff = min(backoff*2, s.MaxBackoff)
	}
}

// once runs the plugin until it stops, fails a check or ctx ends. It always
// leaves no process behind.
func (s *Supervisor) once(ctx context.Context, f Found, prog *Program, log *slog.Logger) error {
	m := f.Manifest
	exe, err := resolveCommand(f.Dir, prog.Command)
	if err != nil {
		return err
	}
	socket := filepath.Join(s.RunDir, m.ID+".sock")
	_ = os.Remove(socket)
	defer func() { _ = os.Remove(socket) }()

	env := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		sdk.EnvSocket + "=" + socket,
		sdk.EnvID + "=" + m.ID,
		sdk.EnvRole + "=" + s.Role,
		sdk.EnvCoreVersion + "=" + s.CoreVersion,
	}
	if s.DataDir != "" {
		dd := filepath.Join(s.DataDir, m.ID)
		if err := os.MkdirAll(dd, 0o700); err != nil {
			return err
		}
		env = append(env, sdk.EnvDataDir+"="+dd)
	}
	cmd := exec.Command(exe, prog.Args...)
	cmd.Dir = f.Dir
	cmd.Env = env
	// Its own process group, so that stopping it also stops what it started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", prog.Command, err)
	}
	s.set(m, func(st *Status) { st.PID = cmd.Process.Pid })
	var out sync.WaitGroup
	out.Add(2)
	go func() { defer out.Done(); relay(stdout, log, slog.LevelInfo) }()
	go func() { defer out.Done(); relay(stderr, log, slog.LevelWarn) }()
	proc := &process{cmd: cmd, done: make(chan struct{})}
	go func() {
		out.Wait() // the pipes must be read to the end before Wait
		proc.err = cmd.Wait()
		close(proc.done)
	}()
	defer proc.stop(s.Grace)

	conn, err := s.connect(ctx, socket, proc)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	client := pluginv1.NewPluginServiceClient(conn)
	if err := s.check(ctx, client, m); err != nil {
		return err
	}
	s.set(m, func(st *Status) { st.State, st.LastError = StateRunning, "" })
	log.Info("plugin running", "pid", cmd.Process.Pid)
	return s.watch(ctx, client, proc)
}

// connect waits for the plugin's socket and dials it.
func (s *Supervisor) connect(ctx context.Context, socket string, proc *process) (*grpc.ClientConn, error) {
	deadline := time.Now().Add(s.StartTimeout)
	for {
		if info, err := os.Lstat(socket); err == nil && info.Mode()&os.ModeSocket != 0 {
			break
		}
		select {
		case <-proc.done:
			return nil, fmt.Errorf("the plugin exited before it was ready: %v", exitText(proc.err))
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the plugin did not open its socket within %s", s.StartTimeout)
		}
	}
	return grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

// check asks the plugin who it is: a program that is not the plugin its
// manifest names is not run.
func (s *Supervisor) check(ctx context.Context, client pluginv1.PluginServiceClient, m Manifest) error {
	cctx, cancel := context.WithTimeout(ctx, s.StartTimeout)
	defer cancel()
	d, err := client.Describe(cctx, &pluginv1.DescribeRequest{HostApiVersion: APIVersion, HostVersion: s.CoreVersion, Role: s.Role}, grpc.WaitForReady(true))
	if err != nil {
		return fmt.Errorf("the plugin did not answer Describe: %w", err)
	}
	if d.GetId() != m.ID || d.GetVersion() != m.Version || d.GetApiVersion() != m.API {
		return fmt.Errorf("the program says it is %s %s (API %d), but the manifest says %s %s (API %d)", d.GetId(), d.GetVersion(), d.GetApiVersion(), m.ID, m.Version, m.API)
	}
	return nil
}

// watch asks for health until the plugin exits, fails too many checks in a
// row, or ctx ends (then nil).
func (s *Supervisor) watch(ctx context.Context, client pluginv1.PluginServiceClient, proc *process) error {
	tick := time.NewTicker(s.HealthEvery)
	defer tick.Stop()
	failed := 0
	for {
		select {
		case <-proc.done:
			return fmt.Errorf("the plugin exited: %v", exitText(proc.err))
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		hctx, cancel := context.WithTimeout(ctx, s.HealthTimeout)
		h, err := client.Health(hctx, &pluginv1.HealthRequest{})
		cancel()
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			failed++
			err = fmt.Errorf("health check: %w", err)
			if failed >= s.HealthFailures {
				return err
			}
		case h.GetStatus() != pluginv1.HealthResponse_STATUS_SERVING:
			failed++
			if failed >= s.HealthFailures {
				return fmt.Errorf("the plugin reports it is not serving: %s", clip(h.GetMessage(), 200))
			}
		default:
			failed = 0
		}
	}
}

// process is a started plugin. done is closed once it was reaped; err is its
// exit status, readable after that.
type process struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// stop ends the plugin's process group: SIGTERM, then SIGKILL after grace.
// It returns once the process was reaped. Children it left behind after it
// exited by itself are killed too.
func (p *process) stop(grace time.Duration) {
	pgid := -p.cmd.Process.Pid
	select {
	case <-p.done:
		_ = syscall.Kill(pgid, syscall.SIGKILL)
		return
	default:
	}
	_ = syscall.Kill(pgid, syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(grace):
		_ = syscall.Kill(pgid, syscall.SIGKILL)
		<-p.done
	}
}

// resolveCommand finds the executable of a component and refuses one that is
// not inside the plugin directory once links are followed, or not a regular
// executable file.
func resolveCommand(dir, command string) (string, error) {
	if err := checkRelative(command); err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	exe, err := filepath.EvalSymlinks(filepath.Join(dir, filepath.FromSlash(command)))
	if err != nil {
		return "", fmt.Errorf("the command %s: %w", command, err)
	}
	if rel, err := filepath.Rel(root, exe); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the command %s leads outside the plugin directory", command)
	}
	info, err := os.Stat(exe)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("the command %s is not an executable file", command)
	}
	return exe, nil
}

// relay copies what a plugin prints to the host's log, one entry a line,
// with long lines cut and the rate bounded, so that a noisy plugin cannot
// flood the log.
func relay(r io.Reader, log *slog.Logger, level slog.Level) {
	if r == nil {
		return
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	var window time.Time
	n := 0
	for sc.Scan() {
		now := time.Now()
		if now.Sub(window) > time.Second {
			window, n = now, 0
		}
		n++
		if n > 50 {
			continue
		}
		log.Log(context.Background(), level, clip(sc.Text(), 1000), "stream", map[slog.Level]string{slog.LevelInfo: "stdout", slog.LevelWarn: "stderr"}[level])
	}
	_, _ = io.Copy(io.Discard, r)
}

func exitText(err error) string {
	if err == nil {
		return "exit status 0"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.String()
	}
	return err.Error()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}
