package hub

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/CodexCircle/Regulus/internal/agent"
	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/config"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"github.com/CodexCircle/Regulus/internal/update"
)

// startRolloutHub runs a hub (version 9.9.9) that has the release 9.9.9 on its
// release server and an agent listener.
func startRolloutHub(t *testing.T, mutate ...func(*config.Config)) (*Hub, releaseServer) {
	t.Helper()
	rs := newReleaseServer(t, "9.9.9")
	cfg := config.Default()
	cfg.BuiltinAgent = false
	cfg.AgentListen = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	useTestDatabase(t, &cfg)
	for _, m := range mutate {
		m(&cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	// The hub drops an agent that is silent for three heartbeat intervals. With
	// a short interval a scheduling stall (a loaded CI runner, -race) of a few
	// hundred milliseconds drops a healthy agent in the middle of a rollout,
	// and the test sees "not connected" instead of the outcome it tests.
	// Nothing here waits for a heartbeat, so a long interval costs no time.
	h, err := New(ctx, cfg, quiet(), fstest.MapFS{}, WithVersion("9.9.9"), WithAgentIntervals(2*time.Second, 2*time.Second),
		WithUpdateSource(rs.Source), WithUpdateVerifier(acceptAll{}))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- h.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve returned %v", err)
		}
		_ = h.Close()
	})
	waitFor(t, "the agent listener", func() bool { return h.AgentAddr() != "" })
	h.rollout.returnTimeout = 5 * time.Second
	h.rollout.settle = 300 * time.Millisecond
	return h, rs
}

// runAgentUpdating is an agent that may update itself into dir.
func runAgentUpdating(t *testing.T, h *Hub, creds *agent.Credentials, version, dir string) (stop func()) {
	t.Helper()
	conn, err := creds.Dial(h.AgentAddr())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = agent.Run(ctx, conn, agent.Options{
			Version: version, ID: creds.AgentID, Log: quiet(), UpdateDir: dir, Verifier: acceptAll{},
			MinBackoff: 20 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
		})
		_ = conn.Close()
	}()
	stop = func() { cancel(); <-done }
	t.Cleanup(stop)
	return stop
}

func decodeRollout(t *testing.T, rec interface {
	Result() *http.Response
}) RolloutStatus {
	t.Helper()
	var st RolloutStatus
	body, _ := io.ReadAll(rec.Result().Body)
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return st
}

// waitInstalling waits until the agent has answered the hub's update command.
// The installer restarts the agent only after that, so a test that restarts the
// agent earlier would race with the answer and see "disconnected before
// answering" instead of the outcome it means to test.
func waitInstalling(t *testing.T, admin *api) {
	t.Helper()
	waitFor(t, "the agent to be installing", func() bool {
		st := decodeRollout(t, admin.do("GET", "/api/updates/rollout", nil))
		return len(st.Agents) == 1 && st.Agents[0].Status == AgentInstalling
	})
}

func waitRollout(t *testing.T, admin *api, state string) RolloutStatus {
	t.Helper()
	var st RolloutStatus
	waitFor(t, "the rollout to be "+state, func() bool {
		st = decodeRollout(t, admin.do("GET", "/api/updates/rollout", nil))
		return st.State == state
	})
	return st
}

// The agent is behind, takes the release from the hub over its connection,
// leaves a request for the installer, and comes back with the new version.
func TestRolloutUpdatesAnAgentBehind(t *testing.T) {
	h, rs := startRolloutHub(t)
	admin := newAPI(t, h, auth.RoleAdmin)
	creds := enrollNew(t, h, "web1")
	dir := t.TempDir()
	stop := runAgentUpdating(t, h, creds, "9.8.1", dir)
	waitFor(t, "the agent", func() bool { return len(h.Agents()) == 1 })

	st := decodeRollout(t, admin.do("GET", "/api/updates/rollout", nil))
	if st.Target != "9.9.9" || st.State != RolloutIdle || len(st.Candidates) != 1 || st.Candidates[0].From != "9.8.1" {
		t.Fatalf("status = %+v", st)
	}

	rec := admin.do("POST", "/api/updates/rollout", map[string]any{"wave_size": 1})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}

	// The agent hands the release to the installer.
	applyDir := update.ApplyDir(dir)
	waitFor(t, "the request for the installer", func() bool { _, ok := update.ReadRequest(applyDir); return ok })
	req, _ := update.ReadRequest(applyDir)
	if req.Version != "9.9.9" || req.RequestedBy != "hub" {
		t.Errorf("request = %+v", req)
	}
	archive, err := os.ReadFile(filepath.Join(dir, "updates", "9.9.9", rs.archive))
	if err != nil || !strings.HasPrefix(string(archive), "archive of 9.9.9") {
		t.Errorf("archive in the agent = %q, %v", archive, err)
	}
	waitInstalling(t, admin)
	if st := decodeRollout(t, admin.do("GET", "/api/updates/rollout", nil)); st.State != RolloutRunning {
		t.Errorf("while installing: %+v", st)
	}
	if rec := admin.do("POST", "/api/updates/rollout", map[string]any{}); rec.Code != http.StatusConflict {
		t.Errorf("a second rollout: %d", rec.Code)
	}

	// The installer replaces the binary and restarts the agent.
	_ = os.Remove(filepath.Join(applyDir, "request.json"))
	stop()
	runAgentUpdating(t, h, creds, "9.9.9", dir)

	st = waitRollout(t, admin, RolloutDone)
	if len(st.Agents) != 1 || st.Agents[0].Status != AgentUpdated || st.StartedBy != "admin" || st.FinishedAt == nil {
		t.Errorf("after: %+v", st)
	}
	if got := len(st.Candidates); got != 0 {
		t.Errorf("candidates after the update: %d", got)
	}
	entries, _ := h.auth.Audit(context.Background(), 50)
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Event] = true
	}
	for _, ev := range []string{EventRolloutStarted, EventAgentUpdated, EventRolloutFinished} {
		if !seen[ev] {
			t.Errorf("audit lacks %s", ev)
		}
	}
	// The connected marker lets the installer judge the restarted agent.
	if c, ok := update.ReadConnected(dir); !ok || c.Version != "9.9.9" {
		t.Errorf("connected = %+v, %v", c, ok)
	}
}

// A server that was off for months (two release series behind, which the
// hub would otherwise refuse) is connected only to be updated, and the
// rollout takes it straight to the hub's version from the dashboard (#212).
func TestRolloutUpdatesAnAgentThatIsTooOld(t *testing.T) {
	h, _ := startRolloutHub(t)
	admin := newAPI(t, h, auth.RoleAdmin)
	creds := enrollNew(t, h, "was-off")
	dir := t.TempDir()
	stop := runAgentUpdating(t, h, creds, "9.7.0", dir)
	waitFor(t, "the old agent", func() bool { return len(h.Agents()) == 1 })

	st := decodeRollout(t, admin.do("GET", "/api/updates/rollout", nil))
	if len(st.Candidates) != 1 || st.Candidates[0].From != "9.7.0" {
		t.Fatalf("candidates = %+v", st.Candidates)
	}
	if rec := admin.do("POST", "/api/updates/rollout", map[string]any{"wave_size": 1}); rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	applyDir := update.ApplyDir(dir)
	waitFor(t, "the request for the installer", func() bool { _, ok := update.ReadRequest(applyDir); return ok })
	if req, _ := update.ReadRequest(applyDir); req.Version != "9.9.9" {
		t.Errorf("request = %+v, want straight to 9.9.9", req)
	}
	waitInstalling(t, admin)
	_ = os.Remove(filepath.Join(applyDir, "request.json"))
	stop()
	runAgentUpdating(t, h, creds, "9.9.9", dir)

	st = waitRollout(t, admin, RolloutDone)
	if len(st.Agents) != 1 || st.Agents[0].Status != AgentUpdated {
		t.Errorf("after: %+v", st)
	}
	if a := h.Agents(); len(a) != 1 || a[0].UpdateOnly || a[0].Compat != update.CompatCurrent {
		t.Errorf("after the update: %+v", a)
	}
}

func TestRolloutStopsAtTheFirstFailure(t *testing.T) {
	h, _ := startRolloutHub(t)
	admin := newAPI(t, h, auth.RoleAdmin)
	// Neither agent is set up to update itself.
	for _, name := range []string{"a", "b"} {
		runAgentVersion(t, h, enrollNew(t, h, name), "9.8.1")
	}
	waitFor(t, "the agents", func() bool { return len(h.Agents()) == 2 })

	if rec := admin.do("POST", "/api/updates/rollout", map[string]any{"wave_size": 1}); rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	st := waitRollout(t, admin, RolloutFailed)
	counts := map[string]int{}
	for _, a := range st.Agents {
		counts[a.Status]++
		if a.Status == AgentFailed && !strings.Contains(a.Message, "not set up to update itself") {
			t.Errorf("message = %q", a.Message)
		}
	}
	if counts[AgentFailed] != 1 || counts[AgentSkipped] != 1 {
		t.Errorf("agents = %+v", st.Agents)
	}
}

// An agent that comes back with the old version and never with the new one
// is judged when returnTimeout ends: until then it could be a network blip
// before the installer ran (#211).
func TestRolloutNoticesAnAgentThatCameBackRolledBack(t *testing.T) {
	h, _ := startRolloutHub(t)
	h.rollout.returnTimeout = time.Second
	admin := newAPI(t, h, auth.RoleAdmin)
	creds := enrollNew(t, h, "web1")
	dir := t.TempDir()
	stop := runAgentUpdating(t, h, creds, "9.8.1", dir)
	waitFor(t, "the agent", func() bool { return len(h.Agents()) == 1 })

	admin.do("POST", "/api/updates/rollout", map[string]any{})
	waitFor(t, "the request", func() bool { _, ok := update.ReadRequest(update.ApplyDir(dir)); return ok })
	waitInstalling(t, admin)
	// The installer rolled back: the old version is running again.
	_ = os.Remove(filepath.Join(update.ApplyDir(dir), "request.json"))
	stop()
	runAgentUpdating(t, h, creds, "9.8.1", dir)

	st := waitRollout(t, admin, RolloutFailed)
	if a := st.Agents[0]; a.Status != AgentFailed || !strings.Contains(a.Message, "rolled back") {
		t.Errorf("agent = %+v", a)
	}
}

func TestRolloutGivesUpOnAnAgentThatDoesNotReturn(t *testing.T) {
	h, _ := startRolloutHub(t)
	h.rollout.returnTimeout = 300 * time.Millisecond
	admin := newAPI(t, h, auth.RoleAdmin)
	creds := enrollNew(t, h, "web1")
	runAgentUpdating(t, h, creds, "9.8.1", t.TempDir())
	waitFor(t, "the agent", func() bool { return len(h.Agents()) == 1 })

	admin.do("POST", "/api/updates/rollout", map[string]any{})
	st := waitRollout(t, admin, RolloutFailed)
	if a := st.Agents[0]; !strings.Contains(a.Message, "did not connect again") {
		t.Errorf("agent = %+v", a)
	}
}

func TestRolloutRefusals(t *testing.T) {
	h, _ := startRolloutHub(t)
	admin := newAPI(t, h, auth.RoleAdmin)
	if rec := admin.do("POST", "/api/updates/rollout", map[string]any{}); rec.Code != http.StatusConflict {
		t.Errorf("no agent behind: %d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]map[string]any{
		"negative wave": {"wave_size": -1},
		"huge wave":     {"wave_size": 1000},
		"unknown field": {"extra": 1},
	} {
		if rec := admin.do("POST", "/api/updates/rollout", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	op := newAPI(t, startRemoteHub(t), auth.RoleOperator)
	if rec := op.do("POST", "/api/updates/rollout", map[string]any{}); rec.Code != http.StatusForbidden {
		t.Errorf("operator: %d", rec.Code)
	}
	if rec := op.do("GET", "/api/updates/rollout", nil); rec.Code != http.StatusForbidden {
		t.Errorf("operator status: %d", rec.Code)
	}
	if rec := admin.do("POST", "/api/updates/rollout/stop", struct{}{}); rec.Code != http.StatusOK {
		t.Errorf("stop with nothing running: %d", rec.Code)
	}
}

func TestRolloutNeedsAReleaseBuild(t *testing.T) {
	h := newUpdateHub(t, "dev", staticSource{})
	if _, err := h.StartRollout(t.Context(), "admin", RolloutOptions{}); err != ErrNotRelease {
		t.Errorf("error = %v", err)
	}
}

// An agent gets only the release the hub runs, its own build, and only while
// connected: the cache is not a file server.
func TestFetchUpdateServesOnlyWhatItShould(t *testing.T) {
	h, _ := startRolloutHub(t)
	admin := newAPI(t, h, auth.RoleAdmin)
	creds := enrollNew(t, h, "web1")
	runAgentVersion(t, h, creds, "9.8.1")
	waitFor(t, "the agent", func() bool { return len(h.Agents()) == 1 })
	// Put the release in the cache.
	if rec := admin.do("POST", "/api/updates/rollout", map[string]any{}); rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	waitRollout(t, admin, RolloutFailed) // the agent cannot update itself, which is fine here

	conn, err := creds.Dial(h.AgentAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client := agentv1.NewAgentServiceClient(conn)
	fetch := func(version string, file agentv1.UpdateFile) (int, error) {
		stream, err := client.FetchUpdate(t.Context(), &agentv1.FetchUpdateRequest{Version: version, File: file})
		if err != nil {
			return 0, err
		}
		n := 0
		for {
			chunk, err := stream.Recv()
			if err == io.EOF {
				return n, nil
			}
			if err != nil {
				return n, err
			}
			n += len(chunk.GetData())
		}
	}
	for _, f := range []agentv1.UpdateFile{agentv1.UpdateFile_UPDATE_FILE_ARCHIVE, agentv1.UpdateFile_UPDATE_FILE_CHECKSUMS, agentv1.UpdateFile_UPDATE_FILE_BUNDLE} {
		if n, err := fetch("9.9.9", f); err != nil || n == 0 {
			t.Errorf("file %v: %d bytes, %v", f, n, err)
		}
	}
	for name, tc := range map[string]struct {
		version string
		file    agentv1.UpdateFile
		code    codes.Code
	}{
		"another version": {"1.0.0", agentv1.UpdateFile_UPDATE_FILE_ARCHIVE, codes.NotFound},
		"a path":          {"../../etc", agentv1.UpdateFile_UPDATE_FILE_ARCHIVE, codes.NotFound},
		"no file":         {"9.9.9", agentv1.UpdateFile_UPDATE_FILE_UNSPECIFIED, codes.InvalidArgument},
		"not the hub's":   {"9.8.1", agentv1.UpdateFile_UPDATE_FILE_ARCHIVE, codes.NotFound},
	} {
		if _, err := fetch(tc.version, tc.file); status.Code(err) != tc.code {
			t.Errorf("%s: error = %v, want %v", name, err, tc.code)
		}
	}
}

func TestAutomaticModeUpdatesTheServersInsideTheWindowOnly(t *testing.T) {
	h, _ := startRolloutHub(t, func(c *config.Config) { c.Updates.Apply = true })
	creds := enrollNew(t, h, "web1")
	runAgentVersion(t, h, creds, "9.8.1")
	waitFor(t, "the agent", func() bool { return len(h.Agents()) == 1 })

	now := time.Now()
	around := func(from, to time.Duration) update.Settings {
		return update.Settings{Mode: update.ModeAutomatic, Channel: update.Stable,
			WindowStart: now.Add(from).Format("15:04"), WindowEnd: now.Add(to).Format("15:04")}
	}
	if _, err := h.updates.SetSettings(t.Context(), "admin", around(3*time.Hour, 5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	h.autoRollout(t.Context())
	if st, _ := h.RolloutStatus(t.Context()); st.State != RolloutIdle {
		t.Fatalf("a rollout started outside the window: %+v", st)
	}

	if _, err := h.updates.SetSettings(t.Context(), "admin", around(-time.Hour, time.Hour)); err != nil {
		t.Fatal(err)
	}
	h.autoRollout(t.Context())
	st, _ := h.RolloutStatus(t.Context())
	if st.State == RolloutIdle || st.StartedBy != update.SystemActor {
		t.Fatalf("no rollout inside the window: %+v", st)
	}
	// The agent cannot update itself, so the rollout fails, and the automatic
	// mode does not start it again by itself.
	waitFor(t, "the rollout to fail", func() bool {
		st, _ := h.RolloutStatus(t.Context())
		return st.State == RolloutFailed
	})
	h.autoRollout(t.Context())
	if st, _ := h.RolloutStatus(t.Context()); st.State != RolloutFailed || len(st.Agents) != 1 {
		t.Errorf("the failed rollout was started again: %+v", st)
	}
}

// awaitRequest waits until one of dirs holds a request for the installer and
// returns its index.
func awaitRequest(t *testing.T, dirs ...string) int {
	t.Helper()
	found := -1
	waitFor(t, "a request for the installer", func() bool {
		for i, d := range dirs {
			if _, ok := update.ReadRequest(update.ApplyDir(d)); ok {
				found = i
				return true
			}
		}
		return false
	})
	return found
}

// The installer on the server watches the new agent for a while after it
// connects and can still roll it back (#211). An agent that connects with the
// new version and is then rolled back is not counted as updated, and the
// rollout stops there.
func TestRolloutDoesNotCountAnAgentRolledBackAfterConnecting(t *testing.T) {
	h, _ := startRolloutHub(t)
	h.rollout.settle = 3 * time.Second
	admin := newAPI(t, h, auth.RoleAdmin)
	names := []string{"a", "b"}
	creds := make([]*agent.Credentials, 2)
	dirs := make([]string, 2)
	stops := make([]func(), 2)
	for i, name := range names {
		creds[i], dirs[i] = enrollNew(t, h, name), t.TempDir()
		stops[i] = runAgentUpdating(t, h, creds[i], "9.8.1", dirs[i])
	}
	waitFor(t, "the agents", func() bool { return len(h.Agents()) == 2 })

	if rec := admin.do("POST", "/api/updates/rollout", map[string]any{"wave_size": 1}); rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	i := awaitRequest(t, dirs...)
	first := creds[i].AgentID
	waitFor(t, "the first server to be installing", func() bool {
		st, _ := h.RolloutStatus(t.Context())
		for _, a := range st.Agents {
			if a.ID == first {
				return a.Status == AgentInstalling
			}
		}
		return false
	})
	_ = os.Remove(filepath.Join(update.ApplyDir(dirs[i]), "request.json"))
	stops[i]()
	stops[i] = runAgentUpdating(t, h, creds[i], "9.9.9", dirs[i])
	waitFor(t, "the new version to connect", func() bool {
		a, ok := h.agentInfo(first)
		return ok && a.Version == "9.9.9"
	})
	if st, _ := h.RolloutStatus(t.Context()); st.State != RolloutRunning {
		t.Fatalf("judged before the installer finished watching: %+v", st)
	}
	// The installer did not like it and put the old version back.
	stops[i]()
	runAgentUpdating(t, h, creds[i], "9.8.1", dirs[i])

	var st RolloutStatus
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _ = h.RolloutStatus(t.Context())
		if st.State == RolloutFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the rollout did not fail: %+v; agents %+v", st, h.Agents())
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, a := range st.Agents {
		switch {
		case a.ID == first && (a.Status != AgentFailed || !strings.Contains(a.Message, "rolled the update back")):
			t.Errorf("first server = %+v", a)
		case a.ID != first && a.Status != AgentSkipped:
			t.Errorf("the second server was not held back: %+v", a)
		}
	}
	entries, _ := h.auth.Audit(t.Context(), 50)
	for _, e := range entries {
		if e.Event == EventAgentUpdated {
			t.Errorf("audit says updated: %+v", e)
		}
	}
}

// A network blip that reconnects the old agent before the installer ran is
// not a failure: the agent that then comes back with the new version is
// updated (#211).
func TestRolloutWaitsOutAReconnectWithTheOldVersion(t *testing.T) {
	h, _ := startRolloutHub(t)
	admin := newAPI(t, h, auth.RoleAdmin)
	creds := enrollNew(t, h, "web1")
	dir := t.TempDir()
	stop := runAgentUpdating(t, h, creds, "9.8.1", dir)
	waitFor(t, "the agent", func() bool { return len(h.Agents()) == 1 })

	admin.do("POST", "/api/updates/rollout", map[string]any{})
	awaitRequest(t, dir)
	waitInstalling(t, admin)
	before, _ := h.agentInfo(creds.AgentID)

	// The connection drops and the old agent connects again.
	stop()
	stop = runAgentUpdating(t, h, creds, "9.8.1", dir)
	waitFor(t, "the old agent to reconnect", func() bool {
		a, ok := h.agentInfo(creds.AgentID)
		return ok && !a.ConnectedAt.Equal(before.ConnectedAt)
	})
	time.Sleep(500 * time.Millisecond)
	if st := decodeRollout(t, admin.do("GET", "/api/updates/rollout", nil)); st.State != RolloutRunning || st.Agents[0].Status != AgentInstalling {
		t.Fatalf("a reconnect with the old version was judged: %+v", st)
	}

	// Then the installer runs and the new version comes up.
	_ = os.Remove(filepath.Join(update.ApplyDir(dir), "request.json"))
	stop()
	runAgentUpdating(t, h, creds, "9.9.9", dir)
	st := waitRollout(t, admin, RolloutDone)
	if st.Agents[0].Status != AgentUpdated {
		t.Errorf("after: %+v", st)
	}
}

// A restart of the hub forgets neither the version for which a rollout
// failed (the automatic mode must not try it again) nor the last rollout; a
// rollout that was running is shown as stopped, and the servers it waited for
// as unknown (#211).
func TestRolloutStateSurvivesARestart(t *testing.T) {
	h, rs := startRolloutHub(t)
	admin := newAPI(t, h, auth.RoleAdmin)
	runAgentVersion(t, h, enrollNew(t, h, "web1"), "9.8.1") // cannot update itself
	waitFor(t, "the agent", func() bool { return len(h.Agents()) == 1 })
	admin.do("POST", "/api/updates/rollout", map[string]any{})
	waitRollout(t, admin, RolloutFailed)

	// One hub per data directory (and per database): the old one stops
	// before the next starts, as in a real restart. Close may run again in the
	// cleanups; only its first call does anything.
	reopen := func(old *Hub) *Hub {
		t.Helper()
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		h2, err := New(t.Context(), h.cfg, quiet(), fstest.MapFS{}, WithVersion("9.9.9"),
			WithUpdateSource(rs.Source), WithUpdateVerifier(acceptAll{}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h2.Close() })
		return h2
	}
	h2 := reopen(h)
	if h2.rollout.failedTarget != "9.9.9" {
		t.Errorf("failed target after a restart = %q", h2.rollout.failedTarget)
	}
	st, _ := h2.RolloutStatus(t.Context())
	if st.State != RolloutFailed || len(st.Agents) != 1 || st.Agents[0].Status != AgentFailed || st.StartedBy != "admin" {
		t.Errorf("last rollout after a restart = %+v", st)
	}

	// A rollout that was running when the hub stopped.
	h2.rollout.mu.Lock()
	run := h2.rollout.run
	run.status.State = RolloutRunning
	run.status.FinishedAt = nil
	run.status.Agents = []RolloutAgent{{ID: "x", Status: AgentInstalling}, {ID: "y", Status: AgentPending}}
	h2.rollout.mu.Unlock()
	h2.persistRollout(run)
	h3 := reopen(h2)
	st, _ = h3.RolloutStatus(t.Context())
	if st.State != RolloutStopped || st.FinishedAt == nil || st.Agents[0].Status != AgentUnknown || st.Agents[0].Message != msgHubRestarted || st.Agents[1].Status != AgentSkipped {
		t.Errorf("an interrupted rollout after a restart = %+v", st)
	}

	// A hub of another version does not show a rollout to the old one.
	if err := h3.Close(); err != nil {
		t.Fatal(err)
	}
	h4, err := New(t.Context(), h.cfg, quiet(), fstest.MapFS{}, WithVersion("9.9.10"),
		WithUpdateSource(rs.Source), WithUpdateVerifier(acceptAll{}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h4.Close() }()
	if st, _ := h4.RolloutStatus(t.Context()); st.State != RolloutIdle || st.Target != "9.9.10" {
		t.Errorf("a rollout to the previous version is shown: %+v", st)
	}
}

// A successful rollout clears the memory of a failure.
func TestRolloutSuccessClearsTheFailedTarget(t *testing.T) {
	h, _ := startRolloutHub(t)
	h.setFailedTarget("9.9.9")
	admin := newAPI(t, h, auth.RoleAdmin)
	creds := enrollNew(t, h, "web1")
	dir := t.TempDir()
	stop := runAgentUpdating(t, h, creds, "9.8.1", dir)
	waitFor(t, "the agent", func() bool { return len(h.Agents()) == 1 })
	admin.do("POST", "/api/updates/rollout", map[string]any{})
	awaitRequest(t, dir)
	waitInstalling(t, admin)
	_ = os.Remove(filepath.Join(update.ApplyDir(dir), "request.json"))
	stop()
	runAgentUpdating(t, h, creds, "9.9.9", dir)
	waitRollout(t, admin, RolloutDone)
	if v, _, _ := h.loadSetting(t.Context(), settingRolloutFailed); v != "" || h.rollout.failedTarget != "" {
		t.Errorf("failed target after a success = %q / %q", v, h.rollout.failedTarget)
	}
}
