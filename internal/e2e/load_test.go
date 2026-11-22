package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CodexCircle/Regulus/internal/agent"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"github.com/CodexCircle/Regulus/internal/protocol"
)

// TestHubUnderRealLoad is #370, what TestHubUnderLoad (internal/hub, #234)
// does not measure. The hub is the real binary in a process of its own, so its
// CPU and its memory are its own; the agents connect over real mTLS (a
// loopback listener, a certificate each), the dashboards hold a real WebSocket
// each and one user each saves a layout every second, which is a database
// write, on SQLite or on the PostgreSQL named by REGULUS_LOAD_DATABASE_URL.
//
//	REGULUS_LOAD=1 go test -run TestHubUnderRealLoad -v -count=1 -timeout 20m ./internal/e2e/
//
// Knobs: REGULUS_LOAD_AGENTS (200), REGULUS_LOAD_DASHBOARDS (20),
// REGULUS_LOAD_INTERVAL (10s, the real default), REGULUS_LOAD_SECONDS (60),
// REGULUS_LOAD_DATABASE_URL (postgres://user:password@host:port/db?sslmode=…
// of an empty database: the test creates the hub's tables in it),
// REGULUS_LOAD_HOLD (keeps the hub and the simulated agents up for that long
// after the run, so a browser can be pointed at it: see
// scripts/browser-load.mjs; the address and the login are printed).
// REGULUS_E2E_BIN uses a binary you built; otherwise one is built without the
// race detector. The results of a run are in docs/development.md.
func TestHubUnderRealLoad(t *testing.T) {
	if os.Getenv("REGULUS_LOAD") != "1" {
		t.Skip("set REGULUS_LOAD=1 to run the real-transport load test (about two minutes)")
	}
	agents := envInt("REGULUS_LOAD_AGENTS", 200)
	dashboards := envInt("REGULUS_LOAD_DASHBOARDS", 20)
	seconds := envInt("REGULUS_LOAD_SECONDS", 60)
	interval := 10 * time.Second
	if v := os.Getenv("REGULUS_LOAD_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatal(err)
		}
		interval = d
	}

	bin := os.Getenv("REGULUS_E2E_BIN")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "regulus")
		cmd := exec.Command("go", "build", "-o", bin, "../../cmd/regulus")
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("build: %v", err)
		}
	} else if abs, err := filepath.Abs(bin); err == nil {
		bin = abs
	}

	dir := t.TempDir()
	hubArgs := []string{
		"hub", "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:" + freePort(t),
		"--data-dir", filepath.Join(dir, "hub"), "--builtin-agent=false", "--metrics-interval", interval.String(),
	}
	dbName := "SQLite"
	var env []string
	if u := os.Getenv("REGULUS_LOAD_DATABASE_URL"); u != "" {
		pu, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		pass, _ := pu.User.Password()
		port := pu.Port()
		if port == "" {
			port = "5432"
		}
		env = []string{
			"REGULUS_DATABASE_TYPE=postgres", "REGULUS_DATABASE_HOST=" + pu.Hostname(), "REGULUS_DATABASE_PORT=" + port,
			"REGULUS_DATABASE_NAME=" + strings.TrimPrefix(pu.Path, "/"), "REGULUS_DATABASE_USER=" + pu.User.Username(),
			"REGULUS_DATABASE_PASSWORD=" + pass, "REGULUS_DATABASE_SSLMODE=" + pu.Query().Get("sslmode"),
		}
		dbName = "PostgreSQL"
	}
	hub := startHubEnv(t, bin, filepath.Join(dir, "hub.log"), env, hubArgs...)
	if agents == 0 {
		t.Fatal("REGULUS_LOAD_AGENTS must be at least 1")
	}
	pid := hub.cmd.Process.Pid
	admin := newAPI(t, hub.httpAddr)
	admin.setup(hub.setupToken)

	// The dashboards' users: each has an account and a session. Creating an
	// account asks for a recent proof of the password.
	admin.do("POST", "/api/account/reauth", map[string]string{"password": password}, http.StatusOK)
	type user struct{ api *api }
	users := make([]user, dashboards)
	for i := range users {
		name := fmt.Sprintf("viewer%02d", i)
		admin.do("POST", "/api/users", map[string]string{"username": name, "password": password, "role": "viewer"}, http.StatusCreated)
		u := newAPI(t, hub.httpAddr)
		u.do("POST", "/api/auth/login", map[string]string{"username": name, "password": password}, http.StatusOK)
		users[i] = user{api: u}
	}

	// What the hub holds before any agent connects, after its start-up has
	// settled (the first seconds hold memory the Go runtime gives back later).
	rss0 := settledRSS(pid)

	// The agents: one enrollment each, and a stream over mTLS.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var lastSent sync.Map // agent id -> time.Time of the last report sent
	var sent atomic.Int64
	ids := make([]string, agents)
	enrollStart := time.Now()
	for i := range agents {
		token, fingerprint := admin.newToken(fmt.Sprintf("load-%04d", i))
		creds, err := agent.Enroll(ctx, hub.agentAddr, fingerprint, token, "1", t.TempDir())
		if err != nil {
			t.Fatalf("enroll agent %d: %v", i, err)
		}
		ids[i] = creds.AgentID
		conn, err := creds.Dial(hub.agentAddr)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		stream, err := agentv1.NewAgentServiceClient(conn).Connect(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Hello{Hello: &agentv1.Hello{
			ProtocolVersion: protocol.Version, MinProtocolVersion: protocol.Version, AgentVersion: "dev", AgentId: creds.AgentID,
		}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Recv(); err != nil { // Welcome
			t.Fatalf("agent %d: %v", i, err)
		}
		go func() { // the hub's commands and answers: read and ignore
			for {
				if _, err := stream.Recv(); err != nil {
					return
				}
			}
		}()
		id := creds.AgentID
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A random start inside the interval, as agents that were not started together.
			time.Sleep(time.Duration(int64(interval) / int64(agents) * int64(i%agents)))
			tick := time.NewTicker(interval)
			defer tick.Stop()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				case <-tick.C:
				}
				lastSent.Store(id, time.Now())
				if stream.Send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Heartbeat{Heartbeat: &agentv1.Heartbeat{SentAt: timestamppb.Now()}}}) != nil {
					return
				}
				err := stream.Send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Metrics{Metrics: &agentv1.MetricsReport{
					CollectedAt: timestamppb.Now(), Collector: "linux",
					Cpu:    &agentv1.CpuMetrics{UsagePercent: float64(n % 100), Load_1: 0.5},
					Memory: &agentv1.MemoryMetrics{TotalBytes: 16 << 30, UsedBytes: 4 << 30, AvailableBytes: 12 << 30},
					Disks: []*agentv1.DiskMetrics{
						{MountPoint: "/", TotalBytes: 100 << 30, UsedBytes: 40 << 30},
						{MountPoint: "/data", TotalBytes: 1 << 40, UsedBytes: 300 << 30},
					},
					Networks: []*agentv1.NetworkMetrics{{Interface: "eth0", RxBytes: uint64(n) << 20, TxBytes: uint64(n) << 19}},
				}}})
				if err != nil {
					return
				}
				sent.Add(1)
			}
		}()
	}
	t.Logf("%d agents enrolled and connected over mTLS in %v", agents, time.Since(enrollStart).Round(time.Second))
	waitAgentsConnected(t, admin, agents)
	rss1 := settledRSS(pid)

	// The dashboards: a WebSocket each, counting what the hub pushes and how
	// late a metrics event arrives after the report that caused it.
	var events, bytesIn atomic.Int64
	var latMu sync.Mutex
	var latencies []time.Duration
	var wsErr atomic.Int64
	for i, u := range users {
		base, _ := url.Parse(u.api.base)
		cookies := u.api.c.Jar.Cookies(base)
		hdr := http.Header{}
		for _, c := range cookies {
			hdr.Add("Cookie", c.Name+"="+c.Value)
		}
		hdr.Set("Origin", u.api.base)
		c, _, err := websocket.Dial(ctx, "ws://"+hub.httpAddr+"/api/stream", &websocket.DialOptions{HTTPHeader: hdr})
		if err != nil {
			t.Fatalf("dashboard %d: websocket: %v", i, err)
		}
		c.SetReadLimit(1 << 24)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = c.CloseNow() }()
			for {
				_, data, err := c.Read(ctx)
				if err != nil {
					select {
					case <-stop:
					default:
						wsErr.Add(1)
					}
					return
				}
				events.Add(1)
				bytesIn.Add(int64(len(data)))
				if !bytes.Contains(data[:min(len(data), 64)], []byte("agent.metrics")) {
					continue
				}
				var ev struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(data, &ev) != nil {
					continue
				}
				if at, ok := lastSent.Load(ev.ID); ok {
					latMu.Lock()
					latencies = append(latencies, time.Since(at.(time.Time)))
					latMu.Unlock()
				}
			}
		}()
	}

	// Each user saves a layout once a second: one database write each.
	type timing struct {
		path string
		d    time.Duration
		code int
	}
	var mu sync.Mutex
	var timings []timing
	layout := `{"layout":{"version":1,"widgets":[` + strings.Repeat(`{"id":"cpu","x":0,"y":0,"w":4,"h":3},`, 20) + `{"id":"mem","x":4,"y":0,"w":4,"h":3}]}}`
	started := time.Now()
	cpu0, _ := procCPU(pid)
	for _, u := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				case <-tick.C:
				}
				method, path, body := "PUT", "/api/layout", layout
				if n%2 == 1 {
					method, path, body = "GET", "/api/agents", ""
				}
				t0 := time.Now()
				req, _ := http.NewRequest(method, u.api.base+path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				code := 0
				if res, err := u.api.c.Do(req); err == nil {
					code = res.StatusCode
					_ = res.Body.Close()
				}
				mu.Lock()
				timings = append(timings, timing{method + " " + path, time.Since(t0), code})
				mu.Unlock()
			}
		}()
	}

	time.Sleep(time.Duration(seconds) * time.Second)
	elapsed := time.Since(started)
	cpu1, _ := procCPU(pid)
	rss2, hwm := procMem(pid)
	threads := procThreads(pid)

	if hold := os.Getenv("REGULUS_LOAD_HOLD"); hold != "" {
		d, err := time.ParseDuration(hold)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("HOLD %v: dashboard http://%s, user admin, password %s (%d agents connected)", d, hub.httpAddr, password, agents)
		time.Sleep(d)
	}
	close(stop)
	cancel()
	wg.Wait()

	per := map[string][]time.Duration{}
	failures := 0
	for _, s := range timings {
		if s.code != http.StatusOK && s.code != http.StatusNoContent {
			if failures == 0 {
				t.Logf("first failure: %s answered %d", s.path, s.code)
			}
			failures++
		}
		per[s.path] = append(per[s.path], s.d)
	}
	pct := func(d []time.Duration, p float64) time.Duration {
		if len(d) == 0 {
			return 0
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[min(len(d)-1, int(float64(len(d))*p))]
	}
	ms := func(d time.Duration) string { return fmt.Sprintf("%.1f", float64(d.Microseconds())/1000) }

	t.Logf("machine: %d CPUs, %s, %s; hub database: %s; hub binary: %s", runtime.NumCPU(), runtime.GOARCH, runtime.Version(), dbName, filepath.Base(bin))
	t.Logf("load: %d agents over mTLS, one report per %v each (%.1f reports/s), %d dashboards (a WebSocket and a layout save or an agent list per second each), %v",
		agents, interval, float64(agents)/interval.Seconds(), dashboards, elapsed.Round(time.Second))
	t.Logf("reports sent: %d", sent.Load())
	t.Logf("hub CPU (its own process): %.1f%% of one CPU", 100*(cpu1-cpu0)/elapsed.Seconds())
	// No figure per agent: the start-up of the hub peaks above what it holds
	// at rest, and the runtime gives that memory back over minutes, so the
	// difference of two readings says nothing about one connection.
	t.Logf("hub memory (RSS): %.1f MiB shortly after start (start-up peak), %.1f MiB with %d connected agents, %.1f MiB at the end, %.1f MiB highest; %d threads",
		float64(rss0)/1024, float64(rss1)/1024, agents, float64(rss2)/1024, float64(hwm)/1024, threads)
	t.Logf("WebSocket: %d events (%.1f MiB) to %d dashboards = %.0f events/s in total; metrics events arrived %s ms p50, %s ms p95, %s ms max after the report (%d events measured); %d connections dropped",
		events.Load(), float64(bytesIn.Load())/(1<<20), dashboards, float64(events.Load())/elapsed.Seconds(),
		ms(pct(latencies, 0.5)), ms(pct(latencies, 0.95)), ms(pct(latencies, 1)), len(latencies), wsErr.Load())
	for _, path := range []string{"PUT /api/layout", "GET /api/agents"} {
		d := per[path]
		t.Logf("%s: %d calls, %s / %s / %s ms (p50 / p95 / max)", path, len(d), ms(pct(d, 0.5)), ms(pct(d, 0.95)), ms(pct(d, 1)))
	}
	if failures > 0 {
		t.Errorf("%d calls did not answer 200", failures)
	}
	if wsErr.Load() > 0 {
		t.Errorf("%d dashboard connections were dropped", wsErr.Load())
	}
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

func waitAgentsConnected(t *testing.T, a *api, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var list []agentJSON
		a.getJSON("/api/agents", &list)
		n := 0
		for _, ag := range list {
			if ag.Status == "connected" {
				n++
			}
		}
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d agents connected after 2 minutes", n, want)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// settledRSS waits until the resident memory of the process stops falling (the
// Go runtime returns memory to the system some time after a burst, such as the
// start-up of the hub or the enrollment of agents) and returns it, in KiB.
func settledRSS(pid int) int64 {
	last, _ := procMem(pid)
	calm := 0
	for range 60 {
		time.Sleep(2 * time.Second)
		now, _ := procMem(pid)
		if last-now < 512 {
			if calm++; calm == 5 {
				return now
			}
		} else {
			calm = 0
		}
		last = now
	}
	return last
}

// procMem returns the resident memory and its peak, in KiB, of a process (Linux).
func procMem(pid int) (rss, hwm int64) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseInt(f[1], 10, 64)
		switch f[0] {
		case "VmRSS:":
			rss = v
		case "VmHWM:":
			hwm = v
		}
	}
	return rss, hwm
}

func procThreads(pid int) int {
	data, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "Threads:" {
			n, _ := strconv.Atoi(f[1])
			return n
		}
	}
	return 0
}

// procCPU returns the CPU seconds a process used (user and system, Linux, 100 ticks a second).
func procCPU(pid int) (float64, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	s := string(data)
	f := strings.Fields(s[strings.LastIndex(s, ")")+2:])
	if len(f) < 13 {
		return 0, false
	}
	u, _ := strconv.ParseFloat(f[11], 64)
	k, _ := strconv.ParseFloat(f[12], 64)
	return (u + k) / 100, true
}
