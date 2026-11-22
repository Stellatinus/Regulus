package hub

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CodexCircle/Regulus/internal/config"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"github.com/CodexCircle/Regulus/internal/protocol"
)

// TestHubUnderLoad is #234: a repeatable load run. N simulated agents (the real
// agent service over the in-process transport) send a metrics report each
// interval, M dashboards poll GET /api/agents and one agent's metrics, and the
// test prints the hub's CPU time, heap, goroutines and the latency of those
// calls. It measures the hub's own work: no network between the agents and the
// hub, no TLS, no browser, and the dashboards poll the HTTP API instead of
// holding a WebSocket (the stream's fan-out is not covered).
//
// It is skipped unless REGULUS_LOAD=1, because it takes about a minute:
//
//	REGULUS_LOAD=1 go test -run TestHubUnderLoad -v -count=1 ./internal/hub/
//
// Knobs: REGULUS_LOAD_AGENTS (200), REGULUS_LOAD_DASHBOARDS (20),
// REGULUS_LOAD_INTERVAL (1s, the real default is 10s, so the default run is ten
// times the real load), REGULUS_LOAD_SECONDS (20). The numbers depend on the
// machine; docs/development.md records one run with the hardware.
func TestHubUnderLoad(t *testing.T) {
	if os.Getenv("REGULUS_LOAD") != "1" {
		t.Skip("set REGULUS_LOAD=1 to run the load test (about a minute)")
	}
	agents := envInt("REGULUS_LOAD_AGENTS", 200)
	dashboards := envInt("REGULUS_LOAD_DASHBOARDS", 20)
	seconds := envInt("REGULUS_LOAD_SECONDS", 20)
	interval := time.Second
	if v := os.Getenv("REGULUS_LOAD_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatal(err)
		}
		interval = d
	}

	h := startLoadHub(t)
	handler := NewHandler(Deps{UI: fstest.MapFS{}, Agents: h, Layouts: h, Auth: h.auth})
	body := `{"username":"admin","password":"correct horse battery","setup_token":"` + h.auth.SetupToken() + `"}`
	r := httptest.NewRequest("POST", "/api/auth/setup", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK || len(rec.Result().Cookies()) == 0 {
		t.Fatalf("setup: status %d %s", rec.Code, rec.Body)
	}
	session := rec.Result().Cookies()[0]

	heapNow := func() (heap uint64, goroutines int) {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc, runtime.NumGoroutine()
	}
	heap0, gor0 := heapNow()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var sent atomic.Int64
	ids := make([]string, agents)
	for i := range agents {
		id := fmt.Sprintf("load-%04d", i)
		ids[i] = id
		stream := loadRegister(t, h, id)
		wg.Add(1)
		go func() {
			defer wg.Done()
			tick := time.NewTicker(interval)
			defer tick.Stop()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				case <-tick.C:
				}
				// A heartbeat with each report, as a real agent sends them.
				if err := stream.Send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Heartbeat{Heartbeat: &agentv1.Heartbeat{SentAt: timestamppb.Now()}}}); err != nil {
					return
				}
				err := stream.Send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Metrics{Metrics: &agentv1.MetricsReport{
					CollectedAt: timestamppb.Now(),
					Collector:   "linux",
					Cpu:         &agentv1.CpuMetrics{UsagePercent: float64(n % 100), Load_1: 0.5},
					Memory:      &agentv1.MemoryMetrics{TotalBytes: 16 << 30, UsedBytes: 4 << 30, AvailableBytes: 12 << 30},
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
	waitFor(t, "all agents to connect", func() bool { return len(h.Agents()) == agents })
	heap1, gor1 := heapNow()

	var cpuStart syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &cpuStart)
	started := time.Now()

	type sample struct {
		path string
		d    time.Duration
		code int
	}
	var mu sync.Mutex
	var samples []sample
	for i := range dashboards {
		wg.Add(1)
		go func() {
			defer wg.Done()
			paths := []string{"/api/agents", "/api/agents/" + ids[i%agents] + "/metrics"}
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				case <-tick.C:
				}
				path := paths[n%2]
				req := httptest.NewRequest("GET", path, nil)
				req.AddCookie(session)
				w := httptest.NewRecorder()
				t0 := time.Now()
				handler.ServeHTTP(w, req)
				mu.Lock()
				samples = append(samples, sample{path: strings.Split(path, "/")[len(strings.Split(path, "/"))-1], d: time.Since(t0), code: w.Code})
				mu.Unlock()
			}
		}()
	}

	time.Sleep(time.Duration(seconds) * time.Second)
	elapsed := time.Since(started)
	var cpuEnd syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &cpuEnd)
	heap2, gor2 := heapNow()
	close(stop)
	wg.Wait()

	cpu := func(r syscall.Rusage) time.Duration {
		return time.Duration(r.Utime.Nano()) + time.Duration(r.Stime.Nano())
	}
	used := cpu(cpuEnd) - cpu(cpuStart)

	perPath := map[string][]time.Duration{}
	failures := 0
	for _, s := range samples {
		if s.code != http.StatusOK {
			failures++
		}
		perPath[s.path] = append(perPath[s.path], s.d)
	}
	pct := func(d []time.Duration, p float64) time.Duration {
		if len(d) == 0 {
			return 0
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[min(len(d)-1, int(float64(len(d))*p))]
	}
	t.Logf("machine: %d CPUs, %s, %s", runtime.NumCPU(), runtime.GOARCH, runtime.Version())
	t.Logf("load: %d agents, one report per %v each (%.0f reports/s; the real default is %.0f/s), %d dashboards polling once a second, %v",
		agents, interval, float64(agents)/interval.Seconds(), float64(agents)/10, dashboards, elapsed.Round(time.Second))
	t.Logf("reports sent: %d", sent.Load())
	t.Logf("hub CPU: %v over %v = %.1f%% of one CPU (this process also runs the simulated agents and dashboards)",
		used.Round(time.Millisecond), elapsed.Round(time.Second), 100*used.Seconds()/elapsed.Seconds())
	t.Logf("metrics history held by the hub: %.1f MiB (its own estimate, the figure the 256 MiB budget is checked against), %.1f KiB per agent after %v",
		float64(h.agents.metrics.size())/(1<<20), float64(h.agents.metrics.size())/float64(agents)/1024, elapsed.Round(time.Second))
	t.Logf("process heap: %.1f MiB with no agent, %.1f MiB with %d connected, %.1f MiB at the end. Not the hub's share: the simulated agents run in this process, and the in-process transport gives each connection 2 MiB of buffers (internal/inproc), which a real TCP connection does not",
		float64(heap0)/(1<<20), float64(heap1)/(1<<20), agents, float64(heap2)/(1<<20))
	t.Logf("goroutines: %d before, %d connected, %d after", gor0, gor1, gor2)
	for path, d := range perPath {
		t.Logf("GET .../%s: %d calls, p50 %v, p95 %v, max %v", path, len(d), pct(d, 0.5).Round(time.Microsecond), pct(d, 0.95).Round(time.Microsecond), pct(d, 1).Round(time.Microsecond))
	}
	if failures > 0 {
		t.Errorf("%d of %d dashboard calls did not answer 200", failures, len(samples))
	}
	if got := len(h.Agents()); got != agents {
		t.Errorf("%d agents connected at the end, want %d", got, agents)
	}
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// loadRegister is register without the 20 s limit that connect puts on a
// stream: the load run lasts longer than that.
func loadRegister(t *testing.T, h *Hub, id string) agentv1.AgentService_ConnectClient {
	t.Helper()
	conn, err := h.AgentConn()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = conn.Close() })
	stream, err := agentv1.NewAgentServiceClient(conn).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(helloMsg(protocol.Version, id)); err != nil {
		t.Fatal(err)
	}
	msg, err := stream.Recv()
	if err != nil || msg.GetWelcome() == nil {
		t.Fatalf("no Welcome for %s: %v %v", id, msg, err)
	}
	return stream
}

// startLoadHub is startHub with the agent intervals a real hub uses (10 s), so
// the hub does not drop agents that report every few seconds.
func startLoadHub(t *testing.T) *Hub {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	useTestDatabase(t, &cfg)
	cfg.Updates.Check = false
	cfg.BuiltinAgent = false
	h, err := New(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), fstest.MapFS{}, WithAgentIntervals(10*time.Second, 10*time.Second))
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
		<-done //nolint:errcheck // the error of Serve is not what this run measures
		_ = h.Close()
	})
	return h
}
