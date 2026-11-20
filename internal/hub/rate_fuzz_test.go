package hub

import (
	"math"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
)

// FuzzNetworkRate checks the network rates for any two counter readings and
// any time between them: a rate is either left out (a counter went back,
// which is a reset or a wrap; or the interval cannot be trusted) or is a
// finite, non-negative number of bytes per second that gives back the
// difference over the interval. See collector_fuzz_test.go in internal/agent
// for how the fuzz targets are run.
func FuzzNetworkRate(f *testing.F) {
	f.Add(uint64(0), uint64(10_000), uint64(0), uint64(5_000), int64(10*time.Second))
	f.Add(uint64(10_000), uint64(5), uint64(0), uint64(5), int64(10*time.Second))                       // counter reset
	f.Add(uint64(math.MaxUint64-10), uint64(20), uint64(0), uint64(0), int64(10*time.Second))           // 64-bit wrap
	f.Add(uint64(0), uint64(math.MaxUint64), uint64(0), uint64(math.MaxUint64), int64(time.Nanosecond)) // huge jump in no time
	f.Add(uint64(1), uint64(2), uint64(3), uint64(4), int64(0))                                         // same instant
	f.Add(uint64(1), uint64(2), uint64(3), uint64(4), int64(-time.Second))                              // clock went back
	f.Add(uint64(1), uint64(2), uint64(3), uint64(4), int64(maxRateGap+1))                              // too long a gap
	f.Fuzz(func(t *testing.T, rx0, rx1, tx0, tx1 uint64, gap int64) {
		t0 := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
		report := func(at time.Time, rx, tx uint64) *agentv1.MetricsReport {
			return &agentv1.MetricsReport{
				CollectedAt: timestamppb.New(at),
				Networks:    []*agentv1.NetworkMetrics{{Interface: "eth0", RxBytes: rx, TxBytes: tx}},
			}
		}
		prev := MetricsSample{At: t0, Report: report(t0, rx0, tx0)}
		now := MetricsSample{At: t0.Add(time.Second), Report: report(t0.Add(time.Duration(gap)), rx1, tx1)}
		n := toPoint(now, &prev).Networks[0]

		trusted := gap > 0 && time.Duration(gap) <= maxRateGap
		forward := rx1 >= rx0 && tx1 >= tx0
		if (n.RxBytesPerSec != nil) != (n.TxBytesPerSec != nil) {
			t.Fatalf("only one of the two rates is set: rx %v, tx %v", n.RxBytesPerSec, n.TxBytesPerSec)
		}
		if n.RxBytesPerSec == nil {
			if trusted && forward {
				t.Fatalf("no rate for counters %d->%d, %d->%d over %v", rx0, rx1, tx0, tx1, time.Duration(gap))
			}
			return
		}
		if !trusted || !forward {
			t.Fatalf("a rate for counters %d->%d, %d->%d over %v", rx0, rx1, tx0, tx1, time.Duration(gap))
		}
		seconds := time.Duration(gap).Seconds()
		for _, c := range []struct {
			rate     float64
			from, to uint64
		}{{*n.RxBytesPerSec, rx0, rx1}, {*n.TxBytesPerSec, tx0, tx1}} {
			if math.IsNaN(c.rate) || math.IsInf(c.rate, 0) || c.rate < 0 {
				t.Fatalf("rate %v for %d->%d over %v", c.rate, c.from, c.to, time.Duration(gap))
			}
			want := float64(c.to-c.from) / seconds
			if math.Abs(c.rate-want) > 1e-9*max(want, 1) {
				t.Fatalf("rate %v, want %v", c.rate, want)
			}
		}
	})
}
