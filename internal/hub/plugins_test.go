package hub

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/config"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"github.com/CodexCircle/Regulus/internal/plugin"
	"github.com/CodexCircle/Regulus/internal/protocol"
)

// fastPlugins makes the hub's supervisor quick to restart and to give up, and
// puts the sockets in a short directory (a socket path has a length limit).
func fastPlugins(t *testing.T) Option {
	t.Helper()
	run, err := os.MkdirTemp("", "rh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(run) })
	return WithPluginSupervisor(func(s *plugin.Supervisor) {
		s.RunDir = run
		s.StartTimeout, s.HealthEvery, s.HealthTimeout = 5*time.Second, 50*time.Millisecond, time.Second
		s.MinBackoff, s.MaxBackoff, s.StableAfter = 20*time.Millisecond, 50*time.Millisecond, time.Hour
		s.MaxRestarts, s.RestartWindow, s.Grace = 2, time.Minute, 2*time.Second
	})
}

// buildExample builds plugins/example-hello into root/example-hello.
func buildExample(t *testing.T, root string) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a program")
	}
	src := filepath.Join("..", "..", "plugins", "example-hello")
	dir := filepath.Join(root, "example-hello")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(src, plugin.ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestName), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "bin", "example-hello"), "./"+filepath.ToSlash(src))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the example: %v\n%s", err, out)
	}
}

// crashingPlugin is a hub plugin that writes its environment to its data
// directory and exits at once, every time it is started.
func crashingPlugin(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "crasher")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nenv > \"$REGULUS_PLUGIN_DATA_DIR/env\"\necho crashing on purpose >&2\nexit 7\n"
	if err := os.WriteFile(filepath.Join(dir, "bin", "run"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "id: crasher\nversion: 0.0.1\nname: Crasher\napi: 1\ncomponents:\n  hub:\n    command: bin/run\n"
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

type pluginsBody struct {
	Hub struct {
		Enabled bool         `json:"enabled"`
		Problem string       `json:"problem"`
		Plugins []pluginJSON `json:"plugins"`
	} `json:"hub"`
	Agents []agentPluginsJSON `json:"agents"`
}

func getPlugins(t *testing.T, h http.Handler) pluginsBody {
	t.Helper()
	rec := get(t, h, "/api/plugins")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/plugins: %d %s", rec.Code, rec.Body)
	}
	var body pluginsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	return body
}

func (b pluginsBody) hubPlugin(id string) pluginJSON {
	for _, p := range b.Hub.Plugins {
		if p.ID == id {
			return p
		}
	}
	return pluginJSON{}
}

// A hub with plugins_dir runs the example plugin and shows it in GET
// /api/plugins; a plugin that keeps crashing is given up on without
// affecting the hub or the other plugin; and no plugin sees the hub's
// environment, where the database password lives (#262, "Done when").
func TestTheHubRunsItsPlugins(t *testing.T) {
	const secret = "database-password-that-must-not-leak"
	t.Setenv("REGULUS_DATABASE_PASSWORD", secret)
	t.Setenv("REGULUS_SOMETHING_ELSE", "also-private")

	root := t.TempDir()
	buildExample(t, root)
	crashingPlugin(t, root)
	cfg := config.Default()
	cfg.BuiltinAgent = false
	cfg.PluginsDir = root
	h := startHubWithConfig(t, cfg, fastPlugins(t))
	handler := NewHandler(Deps{UI: fstest.MapFS{}, Agents: h, Plugins: h, Log: quiet()})

	waitFor(t, "the example plugin to run", func() bool {
		return getPlugins(t, handler).hubPlugin("example-hello").State == plugin.StateRunning
	})
	waitFor(t, "the crashing plugin to be given up on", func() bool {
		return getPlugins(t, handler).hubPlugin("crasher").State == plugin.StateFailed
	})

	body := getPlugins(t, handler)
	if !body.Hub.Enabled || body.Hub.Problem != "" || len(body.Hub.Plugins) != 2 || len(body.Agents) != 0 {
		t.Fatalf("body = %+v", body)
	}
	ex := body.hubPlugin("example-hello")
	if ex.Version != "0.1.0" || ex.Restarts != 0 || ex.LastError != "" || ex.Since == nil {
		t.Errorf("example = %+v", ex)
	}
	crash := body.hubPlugin("crasher")
	if crash.Restarts != 2 || !strings.Contains(crash.LastError, "exit status 7") {
		t.Errorf("crasher = %+v", crash)
	}

	// The hub is unaffected: it still answers, and the example still runs
	// as the same process.
	pid := h.HubPlugins().Plugins[1].PID
	if rec := get(t, handler, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("healthz = %d", rec.Code)
	}
	if st := h.HubPlugins().Plugins[1]; st.ID != "example-hello" || st.State != plugin.StateRunning || st.PID != pid || pid == 0 {
		t.Errorf("example after the crashes = %+v", st)
	}
	if ls := h.localStatus(t.Context(), "x"); !ls.PluginsEnabled || len(ls.Plugins) != 2 {
		t.Errorf("local status = %+v", ls)
	}

	// The crashing plugin saw only the variables the supervisor sets.
	env, err := os.ReadFile(filepath.Join(h.cfg.DataDir, "plugins-data", "crasher", "env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(env), secret) || strings.Contains(string(env), "also-private") {
		t.Fatalf("the hub's environment reached a plugin:\n%s", env)
	}
	allowed := map[string]bool{"PATH": true, "PWD": true, "SHLVL": true, "_": true, "OLDPWD": true}
	for _, line := range strings.Split(strings.TrimSpace(string(env)), "\n") {
		key, _, _ := strings.Cut(line, "=")
		if !allowed[key] && !strings.HasPrefix(key, "REGULUS_PLUGIN_") && key != "REGULUS_CORE_VERSION" {
			t.Errorf("unexpected variable in a plugin's environment: %s", key)
		}
	}
}

// Without plugins_dir nothing runs and the API says plugins are off.
func TestPluginsAreOffByDefault(t *testing.T) {
	cfg := config.Default()
	cfg.BuiltinAgent = false
	h := startHubWithConfig(t, cfg)
	body := getPlugins(t, NewHandler(Deps{UI: fstest.MapFS{}, Agents: h, Plugins: h, Log: quiet()}))
	if body.Hub.Enabled || body.Hub.Plugins == nil || len(body.Hub.Plugins) != 0 || body.Agents == nil {
		t.Fatalf("body = %+v", body)
	}
	if _, err := os.Stat(filepath.Join(h.cfg.DataDir, "plugins-run")); !os.IsNotExist(err) {
		t.Errorf("a plugin directory was made without plugins_dir: %v", err)
	}
}

// A plugins directory that cannot be read (here a file) is reported; the
// hub runs on. A missing directory is no plugins (plugin.Discover).
func TestAnUnreadablePluginsDirectoryIsReported(t *testing.T) {
	cfg := config.Default()
	cfg.BuiltinAgent = false
	cfg.PluginsDir = filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(cfg.PluginsDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h := startHubWithConfig(t, cfg, fastPlugins(t))
	handler := NewHandler(Deps{UI: fstest.MapFS{}, Agents: h, Plugins: h, Log: quiet()})
	waitFor(t, "the problem", func() bool { return getPlugins(t, handler).Hub.Problem != "" })
	if body := getPlugins(t, handler); !body.Hub.Enabled || !strings.Contains(body.Hub.Problem, "plugins directory") {
		t.Errorf("body = %+v", body)
	}
}

// What an agent reports about its plugins is shown per agent, cleaned, and
// kept out of the metrics history.
func TestAgentPluginsAreShown(t *testing.T) {
	h := startHub(t)
	stream := connect(t, h)
	if err := stream.Send(helloMsg(protocol.Version, "web1")); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	report := &agentv1.MetricsReport{
		CollectedAt:    timestamppb.Now(),
		PluginsEnabled: true,
		Plugins: []*agentv1.PluginStatus{
			{Id: "example-hello", Version: "0.1.0", State: "running", Since: timestamppb.New(since)},
			{Id: "docker", Version: "1.0.0", State: "failed", Restarts: 6, LastError: "bad\x1b[31m exit"},
		},
	}
	if err := stream.Send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Metrics{Metrics: report}}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Deps{UI: fstest.MapFS{}, Agents: h, Plugins: h, Log: quiet()})
	waitFor(t, "the report", func() bool { return len(getPlugins(t, handler).Agents) == 1 })

	body := getPlugins(t, handler)
	a := body.Agents[0]
	if a.ID != "web1" || len(a.Plugins) != 2 {
		t.Fatalf("agents = %+v", body.Agents)
	}
	if p := a.Plugins[0]; p.ID != "example-hello" || p.State != "running" || p.Since == nil || !p.Since.Equal(since) {
		t.Errorf("first = %+v", p)
	}
	if p := a.Plugins[1]; p.State != "failed" || p.Restarts != 6 || strings.ContainsRune(p.LastError, 0x1b) {
		t.Errorf("second = %+v", p)
	}
	samples, _ := h.Metrics("web1")
	for _, s := range samples {
		if len(s.Report.GetPlugins()) != 0 {
			t.Error("the plugins were kept in the metrics history")
		}
	}

	// A later report without a plugins directory removes the agent.
	time.Sleep(60 * time.Millisecond) // past the minimum gap between reports
	if err := stream.Send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Metrics{Metrics: &agentv1.MetricsReport{CollectedAt: timestamppb.Now()}}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent to leave the list", func() bool { return len(getPlugins(t, handler).Agents) == 0 })
}

// Every signed-in user with servers.read sees the plugins, viewers included.
func TestPluginsNeedServersRead(t *testing.T) {
	h := startRemoteHub(t)
	viewer := newAPI(t, h, auth.RoleViewer)
	if rec := viewer.do("GET", "/api/plugins", nil); rec.Code != http.StatusOK {
		t.Errorf("viewer: %d %s", rec.Code, rec.Body)
	}
	rec := get(t, NewHandler(Deps{UI: fstest.MapFS{}, Agents: h, Plugins: h, Auth: h.auth, Log: quiet()}), "/api/plugins")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", rec.Code)
	}
}

// A directory whose plugin.yaml is invalid is shown in GET /api/plugins with
// its error, and a change of a hub plugin's state is pushed on the live
// stream (#319).
func TestInvalidHubPluginsAreShownAndChangesArePushed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken", plugin.ManifestName), []byte("id: broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A plugin that dies a second after it starts: its state changes after
	// the stream below is open.
	dir := filepath.Join(root, "slowcrash")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "run"), []byte("#!/bin/sh\nsleep 1\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "id: slowcrash\nversion: 0.0.1\nname: Slow\napi: 1\ncomponents:\n  hub:\n    command: bin/run\n"
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.BuiltinAgent = false
	cfg.PluginsDir = root
	h := startHubWithConfig(t, cfg, fastPlugins(t))
	conn, ctx := dialStream(t, h)
	next(t, ctx, conn, "snapshot")

	handler := NewHandler(Deps{UI: fstest.MapFS{}, Agents: h, Plugins: h, Log: quiet()})
	waitFor(t, "the invalid plugin", func() bool { return getPlugins(t, handler).hubPlugin("broken").State == plugin.StateInvalid })
	if p := getPlugins(t, handler).hubPlugin("broken"); p.Version != "" || !strings.Contains(p.LastError, "version") {
		t.Errorf("broken = %+v", p)
	}

	if ev := next(t, ctx, conn, "plugins.changed"); ev["source"] != "hub" || ev["id"] != nil {
		t.Errorf("event = %v", ev)
	}
}

// An agent's report pushes "plugins.changed" only when its plugins changed.
func TestAgentPluginChangesArePushed(t *testing.T) {
	h := startHub(t)
	stream, _ := register(t, h, "a1")
	waitFor(t, "agent", func() bool { return len(h.Agents()) == 1 })
	conn, ctx := dialStream(t, h)
	next(t, ctx, conn, "snapshot")

	send := func(state string) {
		t.Helper()
		report := &agentv1.MetricsReport{
			CollectedAt: timestamppb.Now(), PluginsEnabled: true,
			Plugins: []*agentv1.PluginStatus{{Id: "p", Version: "1", State: state}},
		}
		if err := stream.Send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Metrics{Metrics: report}}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond) // past the minimum gap between reports
	}
	send("running")
	send("running") // the same: no event
	send("failed")

	var changes []map[string]any
	for metrics := 0; metrics < 3; {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatal(err)
		}
		switch msg["type"] {
		case "agent.metrics":
			metrics++
		case "plugins.changed":
			changes = append(changes, msg)
		}
	}
	if len(changes) != 2 {
		t.Fatalf("events = %v, want two", changes)
	}
	for _, ev := range changes {
		if ev["source"] != "agent" || ev["id"] != "a1" {
			t.Errorf("event = %v", ev)
		}
	}
}

// What the store installs or removes is seen after ReloadPlugins, without
// restarting the hub: the supervisor reads the plugins directory when it starts.
func TestReloadPluginsSeesWhatTheStoreChanged(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.BuiltinAgent = false
	cfg.PluginsDir = root
	h := startHubWithConfig(t, cfg, fastPlugins(t))
	handler := NewHandler(Deps{UI: fstest.MapFS{}, Agents: h, Plugins: h, Log: quiet()})
	if n := len(getPlugins(t, handler).Hub.Plugins); n != 0 {
		t.Fatalf("plugins before = %d", n)
	}
	// A plugin in the layout of the store: <id>/<version>/ and <id>/current.
	long := filepath.Join(root, "crasher")
	crashingPlugin(t, root)
	if err := os.MkdirAll(filepath.Join(long, "0.0.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"bin", plugin.ManifestName} {
		if err := os.Rename(filepath.Join(long, n), filepath.Join(long, "0.0.1", n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("0.0.1", filepath.Join(long, plugin.CurrentName)); err != nil {
		t.Fatal(err)
	}
	h.ReloadPlugins()
	waitFor(t, "the new plugin to be seen", func() bool {
		return getPlugins(t, handler).hubPlugin("crasher").Version == "0.0.1"
	})
	// Removed: gone after the next reload, and a reload with nothing changed is harmless.
	if err := os.RemoveAll(long); err != nil {
		t.Fatal(err)
	}
	h.ReloadPlugins()
	waitFor(t, "the removed plugin to go", func() bool { return len(getPlugins(t, handler).Hub.Plugins) == 0 })
	h.ReloadPlugins()
}
