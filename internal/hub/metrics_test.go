package hub

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"testing"
	"testing/fstest"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CodexCircle/Regulus/internal/config"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
)

func report(uptime uint64) *agentv1.MetricsReport {
	return &agentv1.MetricsReport{CollectedAt: timestamppb.Now(), UptimeSeconds: uptime}
}

func TestMetricsStoreKeepsOrderAndPrunesOldSamples(t *testing.T) {
	now := time.Now()
	store := newMetricsStore(time.Hour)
	store.now = func() time.Time { return now }

	store.add("a", report(1))
	now = now.Add(30 * time.Minute)
	store.add("a", report(2))
	store.add("b", report(10))
	now = now.Add(45 * time.Minute) // the first sample of "a" is now 75 minutes old

	got := store.history("a")
	if len(got) != 1 || got[0].Report.GetUptimeSeconds() != 2 {
		t.Fatalf("history(a) = %+v, want only the second sample", got)
	}
	now = now.Add(time.Hour) // everything is older than the retention
	if got := store.history("a"); len(got) != 0 {
		t.Fatalf("history(a) = %+v, want it empty", got)
	}
	if store.known("a") || store.known("b") {
		t.Fatal("agents whose samples all expired must be forgotten")
	}
}

func TestMetricsStoreBoundsSamplesPerAgent(t *testing.T) {
	store := newMetricsStore(time.Hour)
	for i := range maxMetricsSamples + 10 {
		store.add("a", report(uint64(i)))
	}
	got := store.history("a")
	if len(got) != maxMetricsSamples {
		t.Fatalf("kept %d samples, want %d", len(got), maxMetricsSamples)
	}
	if got[0].Report.GetUptimeSeconds() != 10 {
		t.Fatalf("oldest kept sample has uptime %d, want 10", got[0].Report.GetUptimeSeconds())
	}
}

func TestMetricsStoreIgnoresNilReports(t *testing.T) {
	store := newMetricsStore(time.Hour)
	store.add("a", nil)
	if store.known("a") {
		t.Fatal("a nil report must not register the agent")
	}
}

func getMetrics(t *testing.T, src AgentSource, path string) (*http.Response, metricsJSON) {
	t.Helper()
	rec := get(t, NewHandler(Deps{UI: fstest.MapFS{}, Agents: src}), path)
	res := rec.Result()
	var out metricsJSON
	if res.StatusCode == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("invalid JSON %q: %v", rec.Body.String(), err)
		}
	}
	return res, out
}

func TestMetricsEndpointFieldsAndRates(t *testing.T) {
	t0 := time.Now().Add(-time.Minute)
	first := &agentv1.MetricsReport{
		Cpu:    &agentv1.CpuMetrics{UsagePercent: 12.5, Load_1: 0.5, Load_5: 0.4, Load_15: 0.3},
		Memory: &agentv1.MemoryMetrics{TotalBytes: 1000, UsedBytes: 400, AvailableBytes: 600, SwapTotalBytes: 100, SwapUsedBytes: 10},
		Disks:  []*agentv1.DiskMetrics{{MountPoint: "/", Filesystem: "ext4", TotalBytes: 500, UsedBytes: 200}},
		Networks: []*agentv1.NetworkMetrics{
			{Interface: "eth0", RxBytes: 1000, TxBytes: 2000},
			{Interface: "eth1", RxBytes: 5, TxBytes: 5},
		},
		UptimeSeconds: 99,
	}
	second := &agentv1.MetricsReport{
		Networks: []*agentv1.NetworkMetrics{
			{Interface: "eth0", RxBytes: 1500, TxBytes: 2100}, // 10 s later: 50 B/s and 10 B/s
			{Interface: "eth1", RxBytes: 1, TxBytes: 1},       // counter reset
			{Interface: "eth2", RxBytes: 9, TxBytes: 9},       // new interface
		},
	}
	src := fakeSource{metrics: map[string][]MetricsSample{
		"a": {{At: t0, Report: first}, {At: t0.Add(10 * time.Second), Report: second}},
		"b": nil,
	}}

	res, out := getMetrics(t, src, "/api/agents/a/metrics")
	if res.StatusCode != http.StatusOK || out.Agent != "a" || len(out.Points) != 2 {
		t.Fatalf("status = %d, body = %+v", res.StatusCode, out)
	}
	p := out.Points[0]
	if p.CPUPercent != 12.5 || p.Load1 != 0.5 || p.MemoryTotalBytes != 1000 || p.MemoryUsedBytes != 400 ||
		p.SwapUsedBytes != 10 || p.UptimeSeconds != 99 || len(p.Disks) != 1 || p.Disks[0].UsedBytes != 200 {
		t.Errorf("first point: %+v", p)
	}
	if p.Networks[0].RxBytesPerSec != nil {
		t.Errorf("the first point has no previous report, rates must be null: %+v", p.Networks[0])
	}

	nets := out.Points[1].Networks
	if nets[0].RxBytesPerSec == nil || math.Abs(*nets[0].RxBytesPerSec-50) > 1e-9 || math.Abs(*nets[0].TxBytesPerSec-10) > 1e-9 {
		t.Errorf("eth0 rates: %+v", nets[0])
	}
	if nets[1].RxBytesPerSec != nil || nets[2].RxBytesPerSec != nil {
		t.Errorf("a counter reset and a new interface must give null rates: %+v %+v", nets[1], nets[2])
	}

	// A known agent without reports yet is an empty list, not an error.
	res, out = getMetrics(t, src, "/api/agents/b/metrics")
	if res.StatusCode != http.StatusOK || out.Points == nil || len(out.Points) != 0 {
		t.Errorf("agent without reports: status = %d, body = %+v", res.StatusCode, out)
	}
}

func TestMetricsEndpointSinceKeepsRates(t *testing.T) {
	t0 := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	mk := func(rx uint64) *agentv1.MetricsReport {
		return &agentv1.MetricsReport{Networks: []*agentv1.NetworkMetrics{{Interface: "eth0", RxBytes: rx}}}
	}
	src := fakeSource{metrics: map[string][]MetricsSample{"a": {
		{At: t0, Report: mk(0)},
		{At: t0.Add(10 * time.Second), Report: mk(100)},
		{At: t0.Add(20 * time.Second), Report: mk(300)},
	}}}

	_, out := getMetrics(t, src, "/api/agents/a/metrics?since="+t0.Add(10*time.Second).Format(time.RFC3339Nano))
	if len(out.Points) != 1 {
		t.Fatalf("got %d points after since, want 1", len(out.Points))
	}
	if r := out.Points[0].Networks[0].RxBytesPerSec; r == nil || math.Abs(*r-20) > 1e-9 {
		t.Errorf("rate of the newest point = %v, want 20 B/s (computed from the previous report)", r)
	}
}

func TestMetricsEndpointLimitKeepsNewestAndRates(t *testing.T) {
	t0 := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	mk := func(rx uint64) *agentv1.MetricsReport {
		return &agentv1.MetricsReport{Networks: []*agentv1.NetworkMetrics{{Interface: "eth0", RxBytes: rx}}}
	}
	src := fakeSource{metrics: map[string][]MetricsSample{"a": {
		{At: t0, Report: mk(0)},
		{At: t0.Add(10 * time.Second), Report: mk(100)},
		{At: t0.Add(20 * time.Second), Report: mk(300)},
	}}}

	_, out := getMetrics(t, src, "/api/agents/a/metrics?limit=2")
	if len(out.Points) != 2 || !out.Points[0].At.Equal(t0.Add(10*time.Second)) {
		t.Fatalf("limit=2: got %+v, want the two newest points", out.Points)
	}
	// The oldest returned point still has its rate: it is computed before the cut.
	if r := out.Points[0].Networks[0].RxBytesPerSec; r == nil || math.Abs(*r-10) > 1e-9 {
		t.Errorf("rate of the first returned point = %v, want 10 B/s", r)
	}
	if _, out := getMetrics(t, src, "/api/agents/a/metrics?limit=10"); len(out.Points) != 3 {
		t.Errorf("limit above the count: got %d points, want 3", len(out.Points))
	}
	// since and limit combine: newest points after since.
	_, out = getMetrics(t, src, "/api/agents/a/metrics?limit=1&since="+t0.Format(time.RFC3339Nano))
	if len(out.Points) != 1 || !out.Points[0].At.Equal(t0.Add(20*time.Second)) {
		t.Errorf("since+limit: got %+v, want only the newest point", out.Points)
	}
}

func TestMetricsEndpointErrors(t *testing.T) {
	src := fakeSource{metrics: map[string][]MetricsSample{"a": nil}}
	for path, want := range map[string]int{
		"/api/agents/nobody/metrics":        http.StatusNotFound,
		"/api/agents/a/metrics?since=today": http.StatusBadRequest,
		"/api/agents/a/metrics?limit=0":     http.StatusBadRequest,
		"/api/agents/a/metrics?limit=x":     http.StatusBadRequest,
		"/api/agents/a/metrics?step=x":      http.StatusBadRequest,
		"/api/agents/a/metrics?step=30":     http.StatusBadRequest,
		"/api/agents/a/metrics?step=500ms":  http.StatusBadRequest,
		"/api/agents/a/metrics?step=2h":     http.StatusBadRequest,
	} {
		if res, _ := getMetrics(t, src, path); res.StatusCode != want {
			t.Errorf("%s: status = %d, want %d", path, res.StatusCode, want)
		}
	}
	if res, _ := getMetrics(t, nil, "/api/agents/a/metrics"); res.StatusCode != http.StatusNotFound {
		t.Errorf("nil source: status = %d, want 404", res.StatusCode)
	}
}

func TestBuiltinAgentMetricsAreServed(t *testing.T) {
	h := startHubWithConfig(t, config.Default())
	waitFor(t, "two built-in agent reports", func() bool {
		samples, _ := h.Metrics(BuiltinAgentID)
		return len(samples) >= 2
	})

	res, out := getMetrics(t, h, "/api/agents/"+BuiltinAgentID+"/metrics")
	if res.StatusCode != http.StatusOK || len(out.Points) < 2 {
		t.Fatalf("status = %d, points = %d", res.StatusCode, len(out.Points))
	}
	last := out.Points[len(out.Points)-1]
	if last.MemoryTotalBytes == 0 || last.UptimeSeconds == 0 || last.CPUPercent < 0 || last.CPUPercent > 100 {
		t.Errorf("implausible metrics from the real collector: %+v", last)
	}
}

func TestConfiguredMetricsIntervalReachesAgents(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	useTestDatabase(t, &cfg)
	cfg.MetricsInterval = 7 * time.Second
	h, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	if got := h.agents.metricsInterval; got != 7*time.Second {
		t.Fatalf("metrics interval = %v, want 7s", got)
	}
}

func TestMetricsEndpointStepAveragesIntoBuckets(t *testing.T) {
	// Six reports 10 s apart from a whole minute: buckets of 30 s hold three
	// each.
	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	var samples []MetricsSample
	for i := range 6 {
		samples = append(samples, MetricsSample{At: t0.Add(time.Duration(i) * 10 * time.Second), Report: &agentv1.MetricsReport{
			Cpu:      &agentv1.CpuMetrics{UsagePercent: float64(10 * (i + 1)), Load_1: float64(i)},
			Memory:   &agentv1.MemoryMetrics{TotalBytes: 1000, UsedBytes: uint64(100 * (i + 1))},
			Disks:    []*agentv1.DiskMetrics{{MountPoint: "/", UsedBytes: uint64(i)}},
			Networks: []*agentv1.NetworkMetrics{{Interface: "eth0", RxBytes: uint64(100 * i * i), TxBytes: uint64(10 * i)}},
			// The agent's own times: rates come from them.
			CollectedAt:   timestamppb.New(t0.Add(time.Duration(i) * 10 * time.Second)),
			UptimeSeconds: uint64(1000 + 10*i),
		}})
	}
	src := fakeSource{metrics: map[string][]MetricsSample{"a": samples}}

	res, out := getMetrics(t, src, "/api/agents/a/metrics?step=30s")
	if res.StatusCode != http.StatusOK || len(out.Points) != 2 {
		t.Fatalf("status = %d, %d points, want 2", res.StatusCode, len(out.Points))
	}
	first, second := out.Points[0], out.Points[1]
	if !first.At.Equal(t0.Add(20*time.Second)) || !second.At.Equal(t0.Add(50*time.Second)) {
		t.Errorf("times = %v, %v; want the newest report of each bucket", first.At, second.At)
	}
	if first.CPUPercent != 20 || second.CPUPercent != 50 || first.Load1 != 1 || first.MemoryUsedBytes != 200 {
		t.Errorf("means: cpu %v %v, load %v, memory %v", first.CPUPercent, second.CPUPercent, first.Load1, first.MemoryUsedBytes)
	}
	if first.UptimeSeconds != 1020 || first.MemoryTotalBytes != 1000 || first.Disks[0].UsedBytes != 2 || first.Networks[0].RxBytes != 400 {
		t.Errorf("the newest report's values were not kept: %+v", first)
	}
	// rx rates per report: null, 10, 30 | 50, 70, 90 B/s; tx: null, 1, 1 | 1, 1, 1.
	if r := first.Networks[0].RxBytesPerSec; r == nil || math.Abs(*r-20) > 1e-9 {
		t.Errorf("first bucket rx rate = %v, want 20 (the null rate left out)", r)
	}
	if r := second.Networks[0].RxBytesPerSec; r == nil || math.Abs(*r-70) > 1e-9 {
		t.Errorf("second bucket rx rate = %v, want 70", r)
	}
	if r := second.Networks[0].TxBytesPerSec; r == nil || math.Abs(*r-1) > 1e-9 {
		t.Errorf("second bucket tx rate = %v, want 1", r)
	}

	// Buckets do not move with since: the reports after it fill the same
	// bucket, and limit counts buckets.
	_, out = getMetrics(t, src, "/api/agents/a/metrics?step=30s&since="+t0.Add(30*time.Second).Format(time.RFC3339Nano))
	if len(out.Points) != 1 || !out.Points[0].At.Equal(t0.Add(50*time.Second)) || out.Points[0].CPUPercent != 55 {
		t.Errorf("step+since: %+v", out.Points)
	}
	_, out = getMetrics(t, src, "/api/agents/a/metrics?step=10s&limit=2")
	if len(out.Points) != 2 || out.Points[1].CPUPercent != 60 {
		t.Errorf("a step as long as the interval must not change the points: %+v", out.Points)
	}
	_, out = getMetrics(t, src, "/api/agents/a/metrics?step=1h")
	if len(out.Points) != 1 || out.Points[0].CPUPercent != 35 {
		t.Errorf("one bucket for the hour: %+v", out.Points)
	}
}
