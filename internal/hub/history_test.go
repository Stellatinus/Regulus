package hub

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"testing/fstest"
	"time"

	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

// A fixed point of time that is the start of a 15-minute window.
var historyT0 = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

func testHistory(t *testing.T, ret1, ret15 time.Duration) (*metricsHistory, *time.Time) {
	t.Helper()
	db := storagetest.Open(t, t.TempDir())
	clock := historyT0
	m := newMetricsHistory(db, slog.New(slog.NewTextHandler(io.Discard, nil)), ret1, ret15)
	m.now = func() time.Time { return clock }
	return m, &clock
}

func pt(cpu float64, used uint64) pointJSON {
	rx, tx := 100.0, 50.0
	return pointJSON{
		CPUPercent: &cpu, Load1: 1, MemoryTotalBytes: 1000, MemoryUsedBytes: used, SwapTotalBytes: 10, SwapUsedBytes: 1,
		Disks:    []diskJSON{{TotalBytes: 100, UsedBytes: 20}, {TotalBytes: 100, UsedBytes: 80}},
		Networks: []networkJSON{{Interface: "eth0", RxBytesPerSec: &rx, TxBytesPerSec: &tx}, {Interface: "docker0", RxBytesPerSec: &rx, TxBytesPerSec: &tx}},
	}
}

type row struct {
	samples                     int64
	cpu, rx, disk               *float64
	memUsed, memTotal, swapUsed int64
}

func rows(t *testing.T, m *metricsHistory, agent string, tier int) map[int64]row {
	t.Helper()
	r, err := m.db.Query(`SELECT start, samples, cpu, rx_bps, disk_pct, mem_used, mem_total, swap_used FROM metric_buckets WHERE agent_id = ? AND tier = ?`, agent, tier)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	out := map[int64]row{}
	for r.Next() {
		var s int64
		var x row
		if err := r.Scan(&s, &x.samples, &x.cpu, &x.rx, &x.disk, &x.memUsed, &x.memTotal, &x.swapUsed); err != nil {
			t.Fatal(err)
		}
		out[s] = x
	}
	return out
}

func TestHistoryWritesCompletedMinutesInOneBatch(t *testing.T) {
	m, clock := testHistory(t, 72*time.Hour, 90*24*time.Hour)
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		m.record(id, historyT0.Add(10*time.Second), pt(10, 400))
		m.record(id, historyT0.Add(40*time.Second), pt(30, 600))
	}
	// The minute is still open: nothing is written.
	*clock = historyT0.Add(50 * time.Second)
	if err := m.flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(rows(t, m, "a", 1)); n != 0 {
		t.Fatalf("an open minute was written: %d rows", n)
	}
	*clock = historyT0.Add(61 * time.Second)
	if err := m.flush(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		got := rows(t, m, id, 1)
		r, ok := got[historyT0.Unix()]
		if !ok || len(got) != 1 {
			t.Fatalf("%s rows = %v", id, got)
		}
		if r.samples != 2 || r.cpu == nil || *r.cpu != 20 || r.memUsed != 500 || r.memTotal != 1000 || r.swapUsed != 1 {
			t.Errorf("%s row = %+v", id, r)
		}
		// Only the physical interface counts, and the fullest disk.
		if r.rx == nil || *r.rx != 100 || r.disk == nil || *r.disk != 80 {
			t.Errorf("%s rates/disk = %v %v", id, r.rx, r.disk)
		}
	}
	// Written once: flushing again adds nothing.
	if err := m.flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(rows(t, m, "a", 1)); n != 1 {
		t.Errorf("rows after a second flush = %d", n)
	}
}

func TestHistoryMakesTheQuarterFromTheMinutesAndSurvivesARestart(t *testing.T) {
	m, clock := testHistory(t, 72*time.Hour, 90*24*time.Hour)
	ctx := context.Background()
	// 15 minutes with one report each: cpu 0..14, one minute (7) with no cpu.
	for i := range 15 {
		p := pt(float64(i), 500)
		if i == 7 {
			p.CPUPercent = nil
		}
		m.record("a", historyT0.Add(time.Duration(i)*time.Minute+5*time.Second), p)
		// Half way through a restart: a new instance, same database.
		if i == 9 {
			*clock = historyT0.Add(time.Duration(i)*time.Minute + 61*time.Second)
			if err := m.flush(ctx); err != nil {
				t.Fatal(err)
			}
			m = &metricsHistory{db: m.db, log: m.log, now: m.now, retention1m: m.retention1m, retention15m: m.retention15m, open: map[string]*metricsBucket{}}
		}
	}
	*clock = historyT0.Add(16 * time.Minute)
	if err := m.flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(rows(t, m, "a", 1)); n != 15 {
		t.Fatalf("minute rows = %d, want 15", n)
	}
	q := rows(t, m, "a", 15)
	r, ok := q[historyT0.Unix()]
	if !ok || len(q) != 1 {
		t.Fatalf("quarter rows = %v", q)
	}
	// The mean of 0..14 without 7 is (105-7)/14 = 7.
	if r.samples != 15 || r.cpu == nil || *r.cpu != 7 || r.memUsed != 500 {
		t.Errorf("quarter = %+v (cpu %v)", r, r.cpu)
	}
	// The window is not made twice.
	*clock = historyT0.Add(40 * time.Minute)
	if err := m.flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(rows(t, m, "a", 15)); n != 1 {
		t.Errorf("quarter rows after another flush = %d", n)
	}
}

func TestHistoryPrunesByTier(t *testing.T) {
	m, clock := testHistory(t, 2*time.Hour, 24*time.Hour)
	ctx := context.Background()
	old := historyT0
	for _, tier := range []int{1, 15} {
		for _, age := range []time.Duration{time.Hour, 5 * time.Hour, 30 * time.Hour} {
			at := old.Add(-age).Unix() / 900 * 900
			if _, err := m.db.Exec(`INSERT INTO metric_buckets (agent_id, tier, start, samples, load1, mem_used, mem_total, swap_used, swap_total) VALUES ('a', ?, ?, 1, 0, 0, 0, 0, 0)`, tier, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	*clock = old
	if err := m.prune(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(rows(t, m, "a", 1)); n != 1 { // only the one an hour old
		t.Errorf("1-minute rows = %d, want 1", n)
	}
	if n := len(rows(t, m, "a", 15)); n != 2 { // 1 h and 5 h, not 30 h
		t.Errorf("15-minute rows = %d, want 2", n)
	}
	// A tier set to 0 is emptied; the minutes stay for an hour while the
	// 15-minute tier is on.
	m.retention15m = 0
	if err := m.prune(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(rows(t, m, "a", 15)); n != 0 {
		t.Errorf("15-minute rows with the tier off = %d", n)
	}
}

func TestHistoryPrunesInBatches(t *testing.T) {
	m, clock := testHistory(t, time.Hour, 0)
	for i := range historyBatch + 10 {
		if _, err := m.db.Exec(`INSERT INTO metric_buckets (agent_id, tier, start, samples, load1, mem_used, mem_total, swap_used, swap_total) VALUES (?, 1, ?, 1, 0, 0, 0, 0, 0)`, "a", int64(1000+i)); err != nil {
			t.Fatal(err)
		}
	}
	*clock = historyT0
	if err := m.prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(rows(t, m, "a", 1)); n != 0 {
		t.Errorf("rows left = %d", n)
	}
}

func TestHistoryOffWritesNothing(t *testing.T) {
	m, clock := testHistory(t, 0, 0)
	m.record("a", historyT0, pt(1, 1))
	*clock = historyT0.Add(2 * time.Minute)
	if err := m.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(rows(t, m, "a", 1)); n != 0 {
		t.Errorf("rows = %d", n)
	}
}

// A hub that stops and starts again on the same data keeps the buckets of the
// minutes that completed (#405): here the reports of the built-in agent, and
// the migration on whichever backend the tests run.
func TestHubKeepsTheHistoryAcrossARestart(t *testing.T) {
	h := startHubWithConfig(t, config.Default())
	waitFor(t, "a report", func() bool {
		samples, _ := h.Metrics(BuiltinAgentID)
		return len(samples) > 0
	})
	h.history.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if err := h.history.flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	count := func(h *Hub) (n int) {
		t.Helper()
		if err := h.db.QueryRow(`SELECT COUNT(*) FROM metric_buckets WHERE agent_id = ? AND tier = 1`, BuiltinAgentID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count(h)
	if before == 0 {
		t.Fatal("no bucket was written")
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	h2, err := New(t.Context(), h.cfg, quiet(), fstest.MapFS{}, WithVersion("9.9.9"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h2.Close() })
	if got := count(h2); got != before {
		t.Errorf("buckets after a restart = %d, want %d", got, before)
	}
}
