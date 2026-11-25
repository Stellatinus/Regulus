package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/CodexCircle/Regulus/internal/agent"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"github.com/CodexCircle/Regulus/internal/pki"
	"github.com/CodexCircle/Regulus/internal/protocol"
)

const password = "e2e-test-password"

// TestHubAndRemoteAgentOverMTLS runs the hub and a remote agent as separate
// processes of the real binary. Each step says what it would catch if broken.
func TestHubAndRemoteAgentOverMTLS(t *testing.T) {
	if os.Getenv("REGULUS_E2E") != "1" {
		t.Skip("set REGULUS_E2E=1 to run the end-to-end test (make e2e)")
	}
	bin := binary(t)
	dir := t.TempDir()
	agentPort := freePort(t)
	hubArgs := []string{
		"hub", "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:" + agentPort,
		"--data-dir", filepath.Join(dir, "hub"), "--builtin-agent=false", "--metrics-interval", "1s",
	}
	hub := startHub(t, bin, filepath.Join(dir, "hub-1.log"), hubArgs...)
	api := newAPI(t, hub.httpAddr)
	api.setup(hub.setupToken)
	token, fingerprint := api.newToken("e2e")
	agentDir := filepath.Join(dir, "agent")

	// Enrollment pins the hub's CA before the token is sent: with a wrong
	// fingerprint it fails, saves nothing, and the token stays usable.
	wrong := flipLastDigit(fingerprint)
	if code := run(t, bin, filepath.Join(dir, "enroll-wrong.log"), 30*time.Second,
		"agent", "--hub", hub.agentAddr, "--token", token, "--ca-fingerprint", wrong, "--data-dir", agentDir, "--enroll-only"); code == 0 {
		t.Fatal("enrollment with the wrong CA fingerprint succeeded")
	}
	if strings.Contains(hub.log(), `"msg":"agent enrolled"`) {
		t.Fatal("the token was sent to the hub although its CA does not match the fingerprint")
	}
	if _, err := os.Stat(filepath.Join(agentDir, "agent.key")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a failed enrollment left credentials behind (stat: %v)", err)
	}

	// Enrollment over the network with the token, then the agent connects
	// over mTLS and its metrics reach the HTTP API.
	ag := start(t, bin, filepath.Join(dir, "agent.log"),
		"agent", "--hub", hub.agentAddr, "--token", token, "--ca-fingerprint", fingerprint, "--data-dir", agentDir)
	id := api.waitConnected(t, "", ag)
	api.waitMetrics(t, id, 2)
	if code := run(t, bin, filepath.Join(dir, "enroll-again.log"), 30*time.Second,
		"agent", "--hub", hub.agentAddr, "--token", token, "--ca-fingerprint", fingerprint, "--data-dir", filepath.Join(dir, "agent-2"), "--enroll-only"); code == 0 {
		t.Fatal("a used token enrolled a second agent")
	}
	creds, err := agent.LoadCredentials(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if creds.AgentID != id {
		t.Fatalf("the API shows %s, the agent saved %s", id, creds.AgentID)
	}

	// Another protocol version is refused with the reason, over the real
	// listener and with a valid certificate.
	for _, v := range []struct{ min, max uint32 }{{protocol.Version + 1, protocol.Version + 1}, {0, 0}} {
		conn, err := creds.Dial(hub.agentAddr)
		if err != nil {
			t.Fatal(err)
		}
		first, err := hello(conn, id, v.min, v.max)
		_ = conn.Close()
		if err != nil {
			t.Fatalf("protocol %d: %v", v.max, err)
		}
		if r := first.GetReject().GetReason(); r != agentv1.Reject_REASON_UNSUPPORTED_PROTOCOL_VERSION {
			t.Fatalf("protocol %d: got %v, want a rejection for the protocol version", v.max, first)
		}
	}

	// A certificate for the same agent id from another CA, or an expired one
	// from the hub's own CA, is refused.
	now := time.Now()
	foreign, err := pki.LoadOrCreate(filepath.Join(dir, "foreign-ca"), now)
	if err != nil {
		t.Fatal(err)
	}
	hubCA, err := pki.LoadOrCreate(filepath.Join(dir, "hub", "pki"), now)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		ca *pki.CA
		at time.Time
	}{"from another CA": {foreign, now}, "expired": {hubCA, now.AddDate(-6, 0, 0)}} {
		key, csr, err := pki.NewAgentKey()
		if err != nil {
			t.Fatal(err)
		}
		der, _, err := c.ca.SignClient(csr, id, c.at)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := pki.PinnedClientTLS(fingerprint, &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key})
		if err != nil {
			t.Fatal(err)
		}
		conn, err := grpc.NewClient(hub.agentAddr, grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
		if err != nil {
			t.Fatal(err)
		}
		first, err := hello(conn, id, protocol.MinSupported, protocol.Version)
		_ = conn.Close()
		if err == nil {
			t.Fatalf("a certificate %s was accepted: %v", name, first)
		}
		t.Logf("a certificate %s is refused: %v", name, err)
	}

	// A hub restart: the agent reconnects by itself and reports again (the
	// metrics history is in memory, so these are new reports).
	hub.stop(t)
	hub = startHub(t, bin, filepath.Join(dir, "hub-2.log"), hubArgs...)
	api = newAPI(t, hub.httpAddr)
	api.login()
	api.waitConnected(t, id, ag)
	api.waitMetrics(t, id, 1)

	// Pinning on every connection: the agent's credentials pointed at another
	// hub (another CA) do not connect.
	other := startHub(t, bin, filepath.Join(dir, "hub-other.log"),
		"hub", "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:0",
		"--data-dir", filepath.Join(dir, "hub-other"), "--builtin-agent=false")
	impostorDir := filepath.Join(dir, "agent-to-other-hub")
	copyDir(t, agentDir, impostorDir)
	lost := start(t, bin, filepath.Join(dir, "agent-to-other-hub.log"), "agent", "--hub", other.agentAddr, "--data-dir", impostorDir)
	lost.waitLog(t, "connection to the hub lost", 20*time.Second)
	if strings.Contains(other.log(), "agent connected") {
		t.Fatal("an agent of another hub connected: the CA is not pinned")
	}
	if code := lost.stop(t); code != 0 {
		t.Fatalf("the agent stopped with %d after SIGTERM, want 0", code)
	}
	other.stop(t)

	// A cloned identity: the newer connection takes over, the older agent
	// backs off instead of taking it back every second.
	cloneDir := filepath.Join(dir, "agent-clone")
	copyDir(t, agentDir, cloneDir)
	before := strings.Count(hub.log(), `"msg":"agent connected"`)
	clone := start(t, bin, filepath.Join(dir, "agent-clone.log"), "agent", "--hub", hub.agentAddr, "--data-dir", cloneDir)
	ag.waitLog(t, "another connection with this agent's identity", 20*time.Second)
	time.Sleep(5 * time.Second)
	if n := strings.Count(hub.log(), `"msg":"agent connected"`) - before; n > 3 {
		t.Fatalf("%d connections in about 5 seconds with a cloned identity: the two agents fight over it", n)
	}
	if code := ag.stop(t); code != 0 {
		t.Fatalf("the agent stopped with %d after SIGTERM, want 0", code)
	}
	api.waitConnected(t, id, clone)

	// Revocation closes the live stream and the agent exits with the status
	// that stops systemd from restarting it.
	api.do(http.MethodDelete, "/api/agents/"+id, struct{}{}, http.StatusNoContent)
	if code := clone.wait(t, 20*time.Second); code != 3 {
		t.Fatalf("the revoked agent exited with %d, want 3", code)
	}

	// The revocation outlives a restart: the agent is refused at once.
	hub.stop(t)
	hub = startHub(t, bin, filepath.Join(dir, "hub-3.log"), hubArgs...)
	api = newAPI(t, hub.httpAddr)
	api.login()
	if code := run(t, bin, filepath.Join(dir, "agent-revoked.log"), 30*time.Second,
		"agent", "--hub", hub.agentAddr, "--data-dir", agentDir); code != 3 {
		t.Fatalf("the revoked agent exited with %d after a hub restart, want 3", code)
	}
	var all []agentJSON
	api.getJSON("/api/agents?all=true", &all)
	if len(all) != 1 || all[0].ID != id || all[0].Status != "revoked" {
		t.Fatalf("agents after revocation: %+v, want %s revoked", all, id)
	}
	hub.stop(t)
}

// binary returns REGULUS_E2E_BIN or builds the binary with the race detector.
func binary(t *testing.T) string {
	if b := os.Getenv("REGULUS_E2E_BIN"); b != "" {
		abs, err := filepath.Abs(b)
		if err != nil {
			t.Fatal(err)
		}
		return abs
	}
	out := filepath.Join(t.TempDir(), "regulus")
	cmd := exec.Command("go", "build", "-race", "-o", out, "../../cmd/regulus")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}
	return out
}

func freePort(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
}

func flipLastDigit(fp string) string {
	last := fp[len(fp)-1]
	repl := byte('0')
	if last == '0' {
		repl = '1'
	}
	return fp[:len(fp)-1] + string(repl)
}

func copyDir(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// hello opens an agent stream on conn, sends Hello and returns the hub's
// first answer.
func hello(conn grpc.ClientConnInterface, id string, minVersion, version uint32) (*agentv1.HubMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, err := agentv1.NewAgentServiceClient(conn).Connect(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Hello{Hello: &agentv1.Hello{
		ProtocolVersion: version, MinProtocolVersion: minVersion, AgentVersion: "dev", AgentId: id,
	}}}); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	first, err := stream.Recv()
	if err != nil {
		if c := status.Code(err); c == codes.DeadlineExceeded {
			return nil, fmt.Errorf("no answer: %w", err)
		}
		return nil, err
	}
	return first, nil
}

// proc is a process of the binary, its output in a file.
type proc struct {
	cmd     *exec.Cmd
	logPath string
	done    chan struct{}
	code    int
}

func start(t *testing.T, bin, logPath string, args ...string) *proc {
	t.Helper()
	return startEnv(t, bin, logPath, nil, args...)
}

// startEnv is start with more environment variables for the process.
func startEnv(t *testing.T, bin, logPath string, env []string, args ...string) *proc {
	t.Helper()
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	args = append(args, "--log-format", "json")
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = f, f
	// Never reach out for releases, and keep REGULUS_* of the caller out.
	cmd.Env = append(append(filteredEnv(), "REGULUS_UPDATES_CHECK=false"), env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &proc{cmd: cmd, logPath: logPath, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		_ = f.Close()
		p.code = 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			p.code = exit.ExitCode()
		} else if err != nil {
			p.code = -1
		}
		close(p.done)
	}()
	t.Cleanup(func() {
		if !p.exited() {
			_ = cmd.Process.Kill()
			<-p.done
		}
		if t.Failed() {
			t.Logf("--- %s ---\n%s", filepath.Base(logPath), p.log())
		}
		if strings.Contains(p.log(), "DATA RACE") {
			t.Errorf("%s: the race detector found a data race:\n%s", filepath.Base(logPath), p.log())
		}
	})
	return p
}

func filteredEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "REGULUS_") {
			env = append(env, e)
		}
	}
	return env
}

func (p *proc) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *proc) log() string {
	data, _ := os.ReadFile(p.logPath)
	return string(data)
}

// field returns the value of key in the first JSON log line whose message
// is msg.
func (p *proc) field(msg, key string) (string, bool) {
	sc := bufio.NewScanner(strings.NewReader(p.log()))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var line map[string]any
		if json.Unmarshal(sc.Bytes(), &line) != nil || line["msg"] != msg {
			continue
		}
		if v, ok := line[key].(string); ok {
			return v, true
		}
	}
	return "", false
}

func (p *proc) waitLog(t *testing.T, substr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !strings.Contains(p.log(), substr) {
		if time.Now().After(deadline) {
			t.Fatalf("%s: %q not logged within %v", filepath.Base(p.logPath), substr, timeout)
		}
		if p.exited() {
			t.Fatalf("%s: exited with %d before logging %q", filepath.Base(p.logPath), p.code, substr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (p *proc) wait(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case <-p.done:
		return p.code
	case <-time.After(timeout):
		t.Fatalf("%s: still running after %v", filepath.Base(p.logPath), timeout)
		return 0
	}
}

func (p *proc) stop(t *testing.T) int {
	t.Helper()
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	return p.wait(t, 30*time.Second)
}

// run runs the binary to the end and returns its exit status.
func run(t *testing.T, bin, logPath string, timeout time.Duration, args ...string) int {
	t.Helper()
	return start(t, bin, logPath, args...).wait(t, timeout)
}

type hubProc struct {
	*proc
	httpAddr, agentAddr, setupToken string
}

func startHub(t *testing.T, bin, logPath string, args ...string) *hubProc {
	t.Helper()
	return startHubEnv(t, bin, logPath, nil, args...)
}

func startHubEnv(t *testing.T, bin, logPath string, env []string, args ...string) *hubProc {
	t.Helper()
	h := &hubProc{proc: startEnv(t, bin, logPath, env, args...)}
	deadline := time.Now().Add(60 * time.Second)
	for {
		var ok1, ok2 bool
		h.httpAddr, ok1 = h.field("hub listening", "addr")
		h.agentAddr, ok2 = h.field("agent listener ready", "addr")
		if ok1 && ok2 {
			break
		}
		if h.exited() {
			t.Fatalf("the hub exited with %d during startup", h.code)
		}
		if time.Now().After(deadline) {
			t.Fatal("the hub did not report its addresses within 60s")
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.setupToken, _ = h.field("no accounts yet: open the dashboard and create the admin with this one-time setup token", "setup_token")
	return h
}

func (h *hubProc) stop(t *testing.T) {
	t.Helper()
	if code := h.proc.stop(t); code != 0 {
		t.Fatalf("the hub exited with %d after SIGTERM, want 0", code)
	}
}

type api struct {
	t    *testing.T
	base string
	c    *http.Client
}

type agentJSON struct {
	ID            string     `json:"id"`
	Status        string     `json:"status"`
	LastHeartbeat *time.Time `json:"last_heartbeat"`
}

func newAPI(t *testing.T, addr string) *api {
	jar, _ := cookiejar.New(nil)
	return &api{t: t, base: "http://" + addr, c: &http.Client{Jar: jar, Timeout: 15 * time.Second}}
}

func (a *api) do(method, path string, body any, want int) []byte {
	a.t.Helper()
	status, out := a.send(method, path, body)
	if status != want {
		a.t.Fatalf("%s %s answered %d, want %d: %s", method, path, status, want, out)
	}
	return out
}

// send makes one request and returns the status and the body, whatever they are.
func (a *api) send(method, path string, body any) (int, []byte) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			a.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.base+path, rd)
	if err != nil {
		a.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.c.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func (a *api) getJSON(path string, v any) {
	a.t.Helper()
	if err := json.Unmarshal(a.do(http.MethodGet, path, nil, http.StatusOK), v); err != nil {
		a.t.Fatalf("GET %s: %v", path, err)
	}
}

func (a *api) setup(token string) {
	a.t.Helper()
	if token == "" {
		a.t.Fatal("the hub did not log a setup token")
	}
	a.do(http.MethodPost, "/api/auth/setup", map[string]string{"setup_token": token, "username": "admin", "password": password}, http.StatusOK)
}

func (a *api) login() {
	a.t.Helper()
	a.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": password}, http.StatusOK)
}

func (a *api) newToken(name string) (token, fingerprint string) {
	a.t.Helper()
	var out struct {
		Token         string `json:"token"`
		CAFingerprint string `json:"ca_fingerprint"`
	}
	if err := json.Unmarshal(a.do(http.MethodPost, "/api/enrollment-tokens", map[string]string{"name": name}, http.StatusCreated), &out); err != nil {
		a.t.Fatal(err)
	}
	if out.Token == "" || out.CAFingerprint == "" {
		a.t.Fatalf("unexpected enrollment answer: %+v", out)
	}
	return out.Token, out.CAFingerprint
}

// waitConnected waits until an agent (id, or any when id is empty) is
// connected and has sent a heartbeat, and returns its id.
func (a *api) waitConnected(t *testing.T, id string, p *proc) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		var agents []agentJSON
		a.getJSON("/api/agents", &agents)
		for _, ag := range agents {
			if (id == "" || ag.ID == id) && ag.Status == "connected" && ag.LastHeartbeat != nil {
				return ag.ID
			}
		}
		if p.exited() {
			t.Fatalf("the agent exited with %d", p.code)
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent %q not connected within 60s: %+v", id, agents)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waitMetrics waits until the API holds n metrics points of the agent with
// the figures every Linux machine has.
func (a *api) waitMetrics(t *testing.T, id string, n int) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		var m struct {
			Points []struct {
				CPUPercent       float64 `json:"cpu_percent"`
				MemoryTotalBytes uint64  `json:"memory_total_bytes"`
				UptimeSeconds    uint64  `json:"uptime_seconds"`
			} `json:"points"`
		}
		a.getJSON("/api/agents/"+id+"/metrics", &m)
		if len(m.Points) >= n {
			for _, p := range m.Points {
				if p.MemoryTotalBytes == 0 || p.UptimeSeconds == 0 || p.CPUPercent < 0 || p.CPUPercent > 100 {
					t.Fatalf("implausible metrics point: %+v", p)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fewer than %d metrics points within 60s: %+v", n, m.Points)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
