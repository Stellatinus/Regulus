package hub

import (
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
)

const (
	// defaultMetricsRetention is how long the hub keeps the metrics of an agent.
	defaultMetricsRetention = time.Hour
	// maxMetricsSamples bounds the memory used per agent whatever the interval.
	maxMetricsSamples = 3600
	// defaultMetricsBudget bounds the memory of the whole store, across all
	// agents (#244), in the estimate of sampleCost. A typical report (a few
	// disks and interfaces) costs about 1.3 KB, so this keeps a full hour at
	// the 10 s default for about 500 such agents; beyond that the oldest
	// samples of all agents go first.
	defaultMetricsBudget = 256 << 20
	// costPerWireByte and costPerSample estimate the memory of a stored report
	// from its encoded size (sampleCost): a decoded message takes about four
	// times its wire size, plus a fixed part for the message structs and the
	// slice entry (measured with Go 1.26 on reports with 0, 4 and 64 disks and
	// interfaces).
	costPerWireByte = 4
	costPerSample   = 256
)

// MetricsSample is one metrics report with the time the hub received it.
type MetricsSample struct {
	At     time.Time
	Report *agentv1.MetricsReport
}

// storedSample is a sample with its estimated memory, kept so that the total
// can be updated when it is dropped.
type storedSample struct {
	MetricsSample
	cost int64
}

func sampleCost(r *agentv1.MetricsReport) int64 {
	return int64(costPerWireByte*proto.Size(r) + costPerSample)
}

// metricsStore keeps the recent metrics of every agent in memory. The history
// of an agent survives its reconnections and is dropped once it is older than
// the retention; it is lost when the hub restarts. Memory is bounded per agent
// (maxMetricsSamples) and for the whole store (budget): when the estimated
// total goes over the budget, the oldest samples of any agent are dropped
// first.
type metricsStore struct {
	retention time.Duration
	budget    int64
	now       func() time.Time

	mu      sync.Mutex
	samples map[string][]storedSample
	used    int64 // the sum of the costs of all samples
}

func newMetricsStore(retention time.Duration) *metricsStore {
	return &metricsStore{retention: retention, budget: defaultMetricsBudget, now: time.Now, samples: map[string][]storedSample{}}
}

// add stores a report and returns it as a sample together with the previous
// sample of the agent, if any (needed to compute rates).
func (m *metricsStore) add(agentID string, report *agentv1.MetricsReport) (sample MetricsSample, prev *MetricsSample) {
	if report == nil {
		return MetricsSample{}, nil
	}
	cost := sampleCost(report)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	sample = MetricsSample{At: now, Report: report}
	if old := m.samples[agentID]; len(old) > 0 {
		last := old[len(old)-1].MetricsSample
		prev = &last
	}
	list := append(m.samples[agentID], storedSample{MetricsSample: sample, cost: cost})
	m.used += cost
	if over := len(list) - maxMetricsSamples; over > 0 {
		list = m.dropFirstLocked(list, over)
	}
	m.samples[agentID] = list
	m.pruneLocked(now)
	m.enforceBudgetLocked()
	return sample, prev
}

// dropFirstLocked removes the first n samples of list and returns the rest.
// The removed entries are cleared, so that the array behind the slice does
// not keep their reports alive.
func (m *metricsStore) dropFirstLocked(list []storedSample, n int) []storedSample {
	for i := range n {
		m.used -= list[i].cost
		list[i] = storedSample{}
	}
	return list[n:]
}

// pruneLocked drops the samples older than the retention, for every agent, so
// that agents that never come back do not keep memory forever.
func (m *metricsStore) pruneLocked(now time.Time) {
	cutoff := now.Add(-m.retention)
	for id, list := range m.samples {
		i := 0
		for i < len(list) && list[i].At.Before(cutoff) {
			i++
		}
		switch {
		case i == len(list):
			m.dropFirstLocked(list, i)
			delete(m.samples, id)
		case i > 0:
			m.samples[id] = append([]storedSample(nil), m.dropFirstLocked(list, i)...)
		}
	}
}

// enforceBudgetLocked drops the oldest samples, whichever agent they belong
// to, until the store is within its budget. The newest sample of each agent
// goes last of all: it is what the dashboard shows as the agent's current
// state. Each round costs one pass over the agents, which pruneLocked already
// makes on every report.
func (m *metricsStore) enforceBudgetLocked() {
	for m.used > m.budget {
		oldestID := ""
		var oldest time.Time
		for id, list := range m.samples {
			if len(list) < 2 {
				continue
			}
			if oldestID == "" || list[0].At.Before(oldest) {
				oldestID, oldest = id, list[0].At
			}
		}
		if oldestID == "" {
			// Only one sample per agent is left: one report is at most
			// maxAgentMessageBytes, so this is bounded by the number of agents.
			return
		}
		m.samples[oldestID] = m.dropFirstLocked(m.samples[oldestID], 1)
	}
}

// size returns the estimated memory of the store.
func (m *metricsStore) size() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.used
}

// history returns a copy of the samples of the agent, oldest first.
func (m *metricsStore) history(agentID string) []MetricsSample {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(m.now())
	list := m.samples[agentID]
	out := make([]MetricsSample, len(list))
	for i, s := range list {
		out[i] = s.MetricsSample
	}
	return out
}

func (m *metricsStore) known(agentID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.samples[agentID]
	return ok
}
