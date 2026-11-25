package hub

import (
	"context"
	"database/sql"
	"log/slog"
	"regexp"
	"sync"
	"time"
)

// The stored metrics history (#405, docs/architecture.md "Persistent metrics
// history"). The hub already averages reports into buckets for the charts
// (downsample); this keeps the buckets that are complete, in the database, so
// that a restart does not lose them:
//
//   - a 1-minute tier and a 15-minute tier, each with its own retention (0
//     turns a tier off);
//   - the minute being filled is kept in memory, per agent, and written when it
//     completes, the buckets of all agents in one transaction a minute;
//   - the 15-minute tier is made, in the same transaction, from the 1-minute
//     rows of the window that just completed, so a restart in the middle of
//     a window loses nothing;
//   - the rows older than the retention are deleted once an hour, a few
//     thousand at a time.
//
// The raw reports are not stored, and neither is anything per disk (#149).
// What it does not do: serve the history (the API with from and to, and the
// longer ranges of the dashboard, follow), and fill the past of an agent that
// replays reports it kept while the hub was away (#334).

const (
	tierMinute  = 1
	tierQuarter = 15

	// historyBatch is how many rows one DELETE of the pruning removes.
	historyBatch = 5000
	// historyPending bounds the completed buckets waiting for the database,
	// when it cannot be written for a long time: the oldest go first.
	historyPending = 200_000
	// historyPruneEvery is how often old rows are deleted.
	historyPruneEvery = time.Hour
	// historyFlushTimeout bounds one write.
	historyFlushTimeout = 30 * time.Second
	// minuteSource is how long the 1-minute rows are kept at least when the
	// 15-minute tier is on, whatever the retention of the 1-minute tier:
	// they are what a 15-minute bucket is made of.
	minuteSource = time.Hour
)

// metricsBucket is the average of the reports one agent sent in one minute.
type metricsBucket struct {
	agent string
	start int64 // Unix seconds, a multiple of 60
	n     int   // reports

	cpuSum              float64
	cpuN                int
	load1               float64
	memUsed, memTotal   float64
	swapUsed, swapTotal float64
	rxSum, txSum        float64
	rxN, txN            int
	diskPctSum          float64
	diskN               int
}

// virtualInterface matches the interfaces whose bytes are already counted on a
// physical one; it is the rule of the dashboard (dashboard/src/networkInterfaces.ts).
var virtualInterface = regexp.MustCompile(`^(veth|docker|br-|virbr|vnet|vmnet|tap|tun|cali|flannel|cni|cilium|weave|kube|lxc|lxd|podman|wg|tailscale|zt)|\.`)

// countedNetworks is what the total of a server adds up: the physical
// interfaces, or all of them when there is nothing else.
func countedNetworks(all []networkJSON) []networkJSON {
	var physical []networkJSON
	for _, n := range all {
		if !virtualInterface.MatchString(n.Interface) {
			physical = append(physical, n)
		}
	}
	if len(physical) > 0 {
		return physical
	}
	return all
}

// add takes one report into the bucket.
func (b *metricsBucket) add(p pointJSON) {
	b.n++
	if p.CPUPercent != nil {
		b.cpuSum += *p.CPUPercent
		b.cpuN++
	}
	b.load1 += p.Load1
	b.memUsed += float64(p.MemoryUsedBytes)
	b.memTotal += float64(p.MemoryTotalBytes)
	b.swapUsed += float64(p.SwapUsedBytes)
	b.swapTotal += float64(p.SwapTotalBytes)
	var rx, tx float64
	var haveRx, haveTx bool
	for _, n := range countedNetworks(p.Networks) {
		if n.RxBytesPerSec != nil {
			rx += *n.RxBytesPerSec
			haveRx = true
		}
		if n.TxBytesPerSec != nil {
			tx += *n.TxBytesPerSec
			haveTx = true
		}
	}
	if haveRx {
		b.rxSum += rx
		b.rxN++
	}
	if haveTx {
		b.txSum += tx
		b.txN++
	}
	top, have := 0.0, false
	for _, d := range p.Disks {
		if d.TotalBytes == 0 {
			continue
		}
		if pct := 100 * float64(d.UsedBytes) / float64(d.TotalBytes); !have || pct > top {
			top, have = pct, true
		}
	}
	if have {
		b.diskPctSum += top
		b.diskN++
	}
}

// metricsHistory keeps the open minute of every agent and writes the
// completed ones.
type metricsHistory struct {
	db  *sql.DB
	log *slog.Logger
	now func() time.Time

	retention1m, retention15m time.Duration

	mu      sync.Mutex
	open    map[string]*metricsBucket
	pending []*metricsBucket
	// nextWindow is the start of the first 15-minute window not yet made;
	// zero until it is known (loadWindow).
	nextWindow int64
	lastPrune  time.Time
}

func newMetricsHistory(db *sql.DB, log *slog.Logger, retention1m, retention15m time.Duration) *metricsHistory {
	return &metricsHistory{db: db, log: log, now: time.Now, retention1m: retention1m, retention15m: retention15m, open: map[string]*metricsBucket{}}
}

// enabled reports whether anything is stored.
func (m *metricsHistory) enabled() bool { return m.retention1m > 0 || m.retention15m > 0 }

// record takes a report received at time at into the open minute of the agent.
// It only touches memory.
func (m *metricsHistory) record(agentID string, at time.Time, p pointJSON) {
	if m == nil || !m.enabled() {
		return
	}
	start := at.Unix() / 60 * 60
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.open[agentID]
	switch {
	case b == nil || b.start < start:
		if b != nil {
			m.completeLocked(b)
		}
		b = &metricsBucket{agent: agentID, start: start}
		m.open[agentID] = b
	case b.start > start:
		// A report stamped before the open minute (the clock stepped back): it
		// belongs to a minute that was already closed, so it is dropped.
		return
	}
	b.add(p)
}

func (m *metricsHistory) completeLocked(b *metricsBucket) {
	m.pending = append(m.pending, b)
	if over := len(m.pending) - historyPending; over > 0 {
		m.pending = append([]*metricsBucket(nil), m.pending[over:]...)
	}
}

// flush writes the completed minutes, in one transaction, and makes the
// 15-minute buckets of the windows that completed. It is called once a minute.
func (m *metricsHistory) flush(ctx context.Context) error {
	if m == nil || !m.enabled() {
		return nil
	}
	nowMin := m.now().Unix() / 60 * 60
	m.mu.Lock()
	for id, b := range m.open {
		if b.start < nowMin {
			m.completeLocked(b)
			delete(m.open, id)
		}
	}
	batch := m.pending
	m.pending = nil
	m.mu.Unlock()

	err := m.write(ctx, batch, nowMin)
	if err != nil {
		// Keep them for the next minute; the oldest go first when the
		// database stays away.
		m.mu.Lock()
		m.pending = append(batch, m.pending...)
		if over := len(m.pending) - historyPending; over > 0 {
			m.pending = append([]*metricsBucket(nil), m.pending[over:]...)
		}
		m.mu.Unlock()
	}
	return err
}

func (m *metricsHistory) write(ctx context.Context, batch []*metricsBucket, nowMin int64) error {
	if len(batch) == 0 && m.nextWindow != 0 && nowMin < m.nextWindow+15*60 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, historyFlushTimeout)
	defer cancel()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if len(batch) > 0 {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO metric_buckets
			(agent_id, tier, start, samples, cpu, load1, mem_used, mem_total, swap_used, swap_total, rx_bps, tx_bps, disk_pct)
			VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (agent_id, tier, start) DO NOTHING`)
		if err != nil {
			return err
		}
		defer stmt.Close() //nolint:errcheck // the transaction ends it
		for _, b := range batch {
			n := float64(b.n)
			if _, err := stmt.ExecContext(ctx, b.agent, b.start, b.n,
				mean(b.cpuSum, b.cpuN), b.load1/n,
				int64(b.memUsed/n), int64(b.memTotal/n), int64(b.swapUsed/n), int64(b.swapTotal/n),
				mean(b.rxSum, b.rxN), mean(b.txSum, b.txN), mean(b.diskPctSum, b.diskN)); err != nil {
				return err
			}
		}
	}
	next, err := m.deriveQuarters(ctx, tx, nowMin)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	m.nextWindow = next
	return nil
}

// mean is nil (a database NULL) when there were no values.
func mean(sum float64, n int) any {
	if n == 0 {
		return nil
	}
	return sum / float64(n)
}

// deriveQuarters makes the 15-minute buckets of every window that completed
// before nowMin and has none, from the 1-minute rows, and returns the start of
// the first window not made. The mean is weighted by the reports each minute
// holds, and over the minutes that have the value at all.
func (m *metricsHistory) deriveQuarters(ctx context.Context, tx *sql.Tx, nowMin int64) (int64, error) {
	if m.retention15m <= 0 {
		return m.nextWindow, nil
	}
	const win = 15 * 60
	next := m.nextWindow
	if next == 0 {
		// After a start: continue after the last window made, or start with
		// the oldest minute there is.
		var last, first sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MAX(start) FROM metric_buckets WHERE tier = 15`).Scan(&last); err != nil {
			return 0, err
		}
		if last.Valid {
			next = last.Int64 + win
		} else {
			if err := tx.QueryRowContext(ctx, `SELECT MIN(start) FROM metric_buckets WHERE tier = 1`).Scan(&first); err != nil {
				return 0, err
			}
			if !first.Valid {
				return 0, nil
			}
			next = first.Int64 / win * win
		}
	}
	end := nowMin / win * win // windows before this one are complete
	if next >= end {
		return next, nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO metric_buckets
		(agent_id, tier, start, samples, cpu, load1, mem_used, mem_total, swap_used, swap_total, rx_bps, tx_bps, disk_pct)
		SELECT agent_id, 15, (start / 900) * 900, SUM(samples),
			SUM(cpu * samples) / CAST(SUM(CASE WHEN cpu IS NOT NULL THEN samples END) AS DOUBLE PRECISION),
			SUM(load1 * samples) / CAST(SUM(samples) AS DOUBLE PRECISION),
			CAST(SUM(CAST(mem_used AS DOUBLE PRECISION) * samples) / SUM(samples) AS BIGINT),
			CAST(SUM(CAST(mem_total AS DOUBLE PRECISION) * samples) / SUM(samples) AS BIGINT),
			CAST(SUM(CAST(swap_used AS DOUBLE PRECISION) * samples) / SUM(samples) AS BIGINT),
			CAST(SUM(CAST(swap_total AS DOUBLE PRECISION) * samples) / SUM(samples) AS BIGINT),
			SUM(rx_bps * samples) / CAST(SUM(CASE WHEN rx_bps IS NOT NULL THEN samples END) AS DOUBLE PRECISION),
			SUM(tx_bps * samples) / CAST(SUM(CASE WHEN tx_bps IS NOT NULL THEN samples END) AS DOUBLE PRECISION),
			SUM(disk_pct * samples) / CAST(SUM(CASE WHEN disk_pct IS NOT NULL THEN samples END) AS DOUBLE PRECISION)
		FROM metric_buckets
		WHERE tier = 1 AND start >= ? AND start < ?
		GROUP BY agent_id, (start / 900) * 900
		ON CONFLICT (agent_id, tier, start) DO NOTHING`, next, end)
	if err != nil {
		return 0, err
	}
	return end, nil
}

// prune deletes the rows older than the retention of their tier, a few
// thousand at a time. The 1-minute rows are kept for at least minuteSource
// while the 15-minute tier is on: they are what it is made of.
func (m *metricsHistory) prune(ctx context.Context) error {
	if m == nil {
		return nil
	}
	now := m.now()
	keep1 := m.retention1m
	if m.retention15m > 0 && keep1 < minuteSource {
		keep1 = minuteSource
	}
	for _, t := range []struct {
		tier int
		keep time.Duration
	}{{tierMinute, keep1}, {tierQuarter, m.retention15m}} {
		cutoff := now.Add(-t.keep).Unix()
		if t.keep <= 0 {
			// The tier is off: whatever an earlier setting left goes.
			cutoff = now.Add(time.Hour).Unix()
		}
		for {
			res, err := m.db.ExecContext(ctx, `DELETE FROM metric_buckets WHERE tier = ? AND start IN (
				SELECT start FROM metric_buckets WHERE tier = ? AND start < ? LIMIT ?) AND start < ?`,
				t.tier, t.tier, cutoff, historyBatch, cutoff)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n < historyBatch {
				break
			}
		}
	}
	return nil
}

// run flushes every minute and prunes every hour, until ctx ends. Called in
// the hub that holds the lock (startTimers). It prunes also when both tiers are
// off, so that what an earlier setting stored goes.
func (m *metricsHistory) run(ctx context.Context) {
	if m == nil {
		return
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			// The minutes that completed are written; the one still open is lost.
			fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := m.flush(fctx); err != nil {
				m.log.Warn("writing the metrics history", "err", err)
			}
			cancel()
			return
		case <-tick.C:
			if err := m.flush(ctx); err != nil && ctx.Err() == nil {
				m.log.Warn("writing the metrics history", "err", err)
			}
			if now := m.now(); now.Sub(m.lastPrune) >= historyPruneEvery {
				m.lastPrune = now
				if err := m.prune(ctx); err != nil && ctx.Err() == nil {
					m.log.Warn("pruning the metrics history", "err", err)
				}
			}
		}
	}
}
