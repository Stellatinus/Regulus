package hub

import "time"

// Limits of the step parameter of GET /api/agents/{id}/metrics. The hub keeps
// an hour of reports, so a longer bucket would hold all of them.
const (
	minMetricsStep = time.Second
	maxMetricsStep = time.Hour
)

// downsample averages points (oldest first) into buckets of step, aligned to
// multiples of step since the Unix epoch so that the same report always falls
// in the same bucket, whatever the request. A bucket becomes one point:
//
//   - at is the time of its newest report, so a client can poll with since
//     set to the last point it has, as without step;
//   - CPU, load, used and available memory and used swap are the mean;
//   - totals, uptime, disks and network counters are those of the newest
//     report (they grow or stay put; a mean says nothing more);
//   - each interface's rates are the mean of the rates the bucket has for it,
//     or null when it has none.
//
// The newest bucket is usually still filling: a later request returns it
// again with more reports in it.
func downsample(points []pointJSON, step time.Duration) []pointJSON {
	if step <= 0 || len(points) == 0 {
		return points
	}
	out := make([]pointJSON, 0, len(points))
	start := 0
	for i := 1; i <= len(points); i++ {
		if i < len(points) && bucketOf(points[i].At, step) == bucketOf(points[start].At, step) {
			continue
		}
		out = append(out, mergeBucket(points[start:i]))
		start = i
	}
	return out
}

func bucketOf(t time.Time, step time.Duration) int64 {
	n := t.UnixNano()
	b := n / int64(step)
	if n < 0 && n%int64(step) != 0 {
		b-- // floor, for times before 1970 (never in practice)
	}
	return b
}

// mergeBucket makes one point of the reports of a bucket (at least one).
func mergeBucket(bucket []pointJSON) pointJSON {
	last := bucket[len(bucket)-1]
	if len(bucket) == 1 {
		return last
	}
	n := float64(len(bucket))
	var cpu, l1, l5, l15, memUsed, memAvail, swapUsed float64
	type rateSum struct {
		rx, tx   float64
		nrx, ntx int
	}
	rates := map[string]*rateSum{}
	for _, p := range bucket {
		cpu += p.CPUPercent
		l1 += p.Load1
		l5 += p.Load5
		l15 += p.Load15
		memUsed += float64(p.MemoryUsedBytes)
		memAvail += float64(p.MemoryAvailableBytes)
		swapUsed += float64(p.SwapUsedBytes)
		for _, nw := range p.Networks {
			r := rates[nw.Interface]
			if r == nil {
				r = &rateSum{}
				rates[nw.Interface] = r
			}
			if nw.RxBytesPerSec != nil {
				r.rx += *nw.RxBytesPerSec
				r.nrx++
			}
			if nw.TxBytesPerSec != nil {
				r.tx += *nw.TxBytesPerSec
				r.ntx++
			}
		}
	}
	out := last
	out.CPUPercent = cpu / n
	out.Load1, out.Load5, out.Load15 = l1/n, l5/n, l15/n
	out.MemoryUsedBytes = uint64(memUsed / n)
	out.MemoryAvailableBytes = uint64(memAvail / n)
	out.SwapUsedBytes = uint64(swapUsed / n)
	out.Networks = make([]networkJSON, len(last.Networks))
	for i, nw := range last.Networks {
		nw.RxBytesPerSec, nw.TxBytesPerSec = nil, nil
		if r := rates[nw.Interface]; r != nil {
			if r.nrx > 0 {
				v := r.rx / float64(r.nrx)
				nw.RxBytesPerSec = &v
			}
			if r.ntx > 0 {
				v := r.tx / float64(r.ntx)
				nw.TxBytesPerSec = &v
			}
		}
		out.Networks[i] = nw
	}
	return out
}
