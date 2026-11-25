package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/CodexCircle/Regulus/internal/fleet"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
)

// AgentSource provides the agent data served by the HTTP API. *Hub implements
// it.
type AgentSource interface {
	Agents() []AgentInfo
	// StaleAfter is how long an agent may stay silent before it is reported as
	// stale.
	StaleAfter() time.Duration
	// Metrics returns the recent metrics of an agent, oldest first; known is
	// false for an agent the hub never saw.
	Metrics(agentID string) (samples []MetricsSample, known bool)
	// Subscribe returns a channel of JSON events (see events.go) and a function
	// that ends the subscription. The channel is closed if the subscriber falls
	// too far behind.
	Subscribe() (events <-chan []byte, cancel func())
}

// Agent statuses reported by GET /api/agents. The last two only appear with
// ?all=true, for enrolled agents that are not connected.
const (
	statusConnected = "connected"
	statusStale     = "stale"
	statusOffline   = "offline"
	statusRevoked   = "revoked"
	// statusIncompatible: an enrolled agent the hub refused because its
	// version cannot work with this hub; see incompatible_reason.
	statusIncompatible = "incompatible"
)

// agentJSON is the JSON form of an agent in GET /api/agents.
type agentJSON struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Role is the node role of the server (docs/glossary.md): "hub" for the
	// hub's own server (the built-in agent) and "agent" for every other one.
	// Node types such as a redundant hub will refine it later; a client must
	// show an unknown value as it is.
	Role             string     `json:"role"`
	Version          string     `json:"version"`
	Status           string     `json:"status"`
	Hostname         string     `json:"hostname,omitempty"`
	OS               string     `json:"os,omitempty"`
	OSVersion        string     `json:"os_version,omitempty"`
	Kernel           string     `json:"kernel,omitempty"`
	Arch             string     `json:"arch,omitempty"`
	CPUCount         uint32     `json:"cpu_count,omitempty"`
	MemoryTotalBytes uint64     `json:"memory_total_bytes,omitempty"`
	ConnectedAt      *time.Time `json:"connected_at"`
	LastSeenAt       *time.Time `json:"last_seen_at,omitempty"`
	LastHeartbeat    *time.Time `json:"last_heartbeat"`
	MetricsAt        *time.Time `json:"metrics_at"`
	UptimeSeconds    *uint64    `json:"uptime_seconds"`
	// Compatibility says how the agent's release relates to the hub's:
	// current, behind, ahead, too_old (only with update_only) or unknown (see
	// update.Compat).
	Compatibility   string `json:"compatibility,omitempty"`
	ProtocolVersion uint32 `json:"protocol_version,omitempty"`
	// IncompatibleReason is why the hub refused the agent, with the status
	// incompatible.
	IncompatibleReason string `json:"incompatible_reason,omitempty"`
	// UpdateOnly: the agent is too old for this hub and is connected only to
	// be updated (compatibility "too_old"); it sends no metrics.
	UpdateOnly bool `json:"update_only,omitempty"`
	// CertNotAfter is when the agent's client certificate ends; absent for the
	// built-in agent and while the hub does not know it (#245).
	CertNotAfter *time.Time `json:"cert_not_after,omitempty"`
	// Container is the container the agent says it runs in ("lxc", "docker",
	// ...), absent on a machine of its own; CPU and memory may then be the
	// host's (#230).
	Container string `json:"container,omitempty"`
	// Collector and Unavailable come from the last metrics report: which
	// collector the agent uses ("linux" or "basic") and the parts it could not
	// read ("cpu", "load", "memory", "disks", "network", "uptime"). Both are
	// absent before the first report and for an agent older than them.
	Collector   string   `json:"collector,omitempty"`
	Unavailable []string `json:"unavailable,omitempty"`
}

// withRegistry adds what the registry knows of an enrolled agent to its JSON.
func (j *agentJSON) withRegistry(e fleet.Agent) {
	j.Name = e.Name
	if !e.CertNotAfter.IsZero() {
		t := e.CertNotAfter.UTC()
		j.CertNotAfter = &t
	}
}

// Node roles in the API (docs/glossary.md).
const (
	roleHub   = "hub"
	roleAgent = "agent"
)

// roleOf is the role of the server with this id: the hub's own server is the
// built-in agent, and every enrolled one is a remote agent.
func roleOf(id string) string {
	if id == BuiltinAgentID {
		return roleHub
	}
	return roleAgent
}

func toAgentJSON(a AgentInfo, now time.Time, staleAfter time.Duration) agentJSON {
	connected := a.ConnectedAt.UTC()
	out := agentJSON{
		ID:          a.ID,
		Role:        roleOf(a.ID),
		Version:     a.Version,
		Status:      statusConnected,
		ConnectedAt: &connected,

		Compatibility:   string(a.Compat),
		ProtocolVersion: a.ProtocolVersion,
		UpdateOnly:      a.UpdateOnly,
	}
	if s := a.System; s != nil {
		out.Hostname = s.Hostname
		out.OS = s.Os
		out.OSVersion = s.OsVersion
		out.Kernel = s.Kernel
		out.Arch = s.Arch
		out.CPUCount = s.CpuCount
		out.MemoryTotalBytes = s.MemoryTotalBytes
		out.Container = s.Container
	}

	lastSeen := a.ConnectedAt
	if !a.LastHeartbeat.IsZero() {
		t := a.LastHeartbeat.UTC()
		out.LastHeartbeat = &t
		lastSeen = a.LastHeartbeat
	}
	if now.Sub(lastSeen) > staleAfter {
		out.Status = statusStale
	}

	if m := a.LastMetrics; m != nil {
		if m.CollectedAt != nil {
			t := m.CollectedAt.AsTime().UTC()
			out.MetricsAt = &t
		}
		uptime := m.UptimeSeconds
		out.UptimeSeconds = &uptime
		out.Collector = m.GetCollector()
		out.Unavailable = m.GetUnavailable()
	}
	return out
}

// registrySource is implemented by sources that know the enrolled agents,
// including those that are not connected.
type registrySource interface {
	Registry(ctx context.Context) ([]fleet.Agent, error)
}

// agentsHandler serves the agents as a JSON array, ordered by id: the
// connected ones, or with ?all=true every enrolled agent too, with the status
// "offline" or "revoked" for those that are not connected.
func agentsHandler(src AgentSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agents := []agentJSON{}
		if src != nil {
			names := map[string]fleet.Agent{}
			var enrolled []fleet.Agent
			if reg, ok := src.(registrySource); ok {
				var err error
				if enrolled, err = reg.Registry(r.Context()); err != nil {
					writeInternal(w)
					return
				}
			}
			for _, e := range enrolled {
				names[e.ID] = e
			}

			now := time.Now()
			staleAfter := src.StaleAfter()
			seen := map[string]bool{}
			for _, a := range src.Agents() {
				j := toAgentJSON(a, now, staleAfter)
				j.withRegistry(names[a.ID])
				agents = append(agents, j)
				seen[a.ID] = true
			}
			if r.URL.Query().Get("all") == "true" {
				for _, e := range enrolled {
					if !seen[e.ID] {
						agents = append(agents, offlineJSON(e, src))
					}
				}
				slices.SortFunc(agents, func(a, b agentJSON) int { return strings.Compare(a.ID, b.ID) })
			}
		}
		writeJSONBody(w, http.StatusOK, agents)
	}
}

// rejectionSource is implemented by sources that remember why they refused an
// enrolled agent.
type rejectionSource interface {
	Rejection(id string) (Rejection, bool)
}

// offlineJSON is the JSON form of an enrolled agent that is not connected.
func offlineJSON(e fleet.Agent, src AgentSource) agentJSON {
	out := agentJSON{ID: e.ID, Role: roleOf(e.ID), Version: e.Version, Status: statusOffline, Hostname: e.Hostname, OSVersion: e.OSVersion}
	out.withRegistry(e)
	if e.Revoked {
		out.Status = statusRevoked
	} else if rs, ok := src.(rejectionSource); ok {
		if r, ok := rs.Rejection(e.ID); ok {
			out.Status = statusIncompatible
			out.IncompatibleReason = r.Message
			if r.Version != "" {
				out.Version = r.Version
			}
		}
	}
	if !e.LastSeen.IsZero() {
		t := e.LastSeen.UTC()
		out.LastSeenAt = &t
	}
	return out
}

// metricsJSON is the JSON form of GET /api/agents/{id}/metrics.
type metricsJSON struct {
	Agent  string      `json:"agent"`
	Points []pointJSON `json:"points"`
}

// pointJSON is one metrics report. Network rates are computed by the hub from
// the counters of two consecutive reports and are null for the first one and
// after a counter reset.
type pointJSON struct {
	At time.Time `json:"at"`
	// CPUPercent is null when the agent could not read the CPU times (#340):
	// an agent that cannot read /proc/stat is not idle.
	CPUPercent           *float64      `json:"cpu_percent"`
	Load1                float64       `json:"load_1"`
	Load5                float64       `json:"load_5"`
	Load15               float64       `json:"load_15"`
	MemoryTotalBytes     uint64        `json:"memory_total_bytes"`
	MemoryUsedBytes      uint64        `json:"memory_used_bytes"`
	MemoryAvailableBytes uint64        `json:"memory_available_bytes"`
	SwapTotalBytes       uint64        `json:"swap_total_bytes"`
	SwapUsedBytes        uint64        `json:"swap_used_bytes"`
	UptimeSeconds        uint64        `json:"uptime_seconds"`
	Disks                []diskJSON    `json:"disks"`
	Networks             []networkJSON `json:"networks"`
	// Unavailable names the parts of the report the agent could not read; their
	// numbers are zero and mean nothing (#230). Absent when all was read.
	Unavailable []string `json:"unavailable,omitempty"`
}

type diskJSON struct {
	MountPoint string `json:"mount_point"`
	Filesystem string `json:"filesystem"`
	TotalBytes uint64 `json:"total_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
}

type networkJSON struct {
	Interface     string   `json:"interface"`
	RxBytes       uint64   `json:"rx_bytes"`
	TxBytes       uint64   `json:"tx_bytes"`
	RxBytesPerSec *float64 `json:"rx_bytes_per_sec"`
	TxBytesPerSec *float64 `json:"tx_bytes_per_sec"`
}

// maxRateGap is the longest time between two reports over which a rate is
// still computed. A longer one (the agent was away, the machine slept) says
// nothing about the speed now: the point has no rate rather than a made-up one.
const maxRateGap = 5 * time.Minute

// rateInterval is the time, in seconds, that the counters of two reports
// cover, or 0 when no rate can be computed. It is measured between the times
// the agent took the two reports, not between the times the hub received
// them: a stall on the way delivers two reports a millisecond apart, and
// dividing ten seconds of traffic by that is a spike a thousand times too
// high. Reports without a time (an older agent) fall back to the receive
// times.
func rateInterval(s, prev MetricsSample) float64 {
	d := s.At.Sub(prev.At)
	if a, b := s.Report.GetCollectedAt(), prev.Report.GetCollectedAt(); a.IsValid() && b.IsValid() {
		d = a.AsTime().Sub(b.AsTime())
	}
	if d <= 0 || d > maxRateGap {
		return 0
	}
	return d.Seconds()
}

func toPoints(samples []MetricsSample) []pointJSON {
	points := make([]pointJSON, 0, len(samples))
	var prev *MetricsSample
	for i := range samples {
		points = append(points, toPoint(samples[i], prev))
		prev = &samples[i]
	}
	return points
}

// cpuPercent is the CPU use of a report, or nil when it is unknown (#340).
// An agent that cannot read /proc/stat sends 0 and lists "cpu" in unavailable;
// the 0 is not a measurement, so it becomes null here. A field with explicit
// presence would say the same, but changing usage_percent to optional is a
// breaking change for buf, and unavailable already exists (docs/versioning.md).
func cpuPercent(r *agentv1.MetricsReport) *float64 {
	if slices.Contains(r.GetUnavailable(), "cpu") {
		return nil
	}
	v := r.GetCpu().GetUsagePercent()
	return &v
}

func toPoint(s MetricsSample, prev *MetricsSample) pointJSON {
	r := s.Report
	p := pointJSON{
		At:                   s.At.UTC(),
		CPUPercent:           cpuPercent(r),
		Load1:                r.GetCpu().GetLoad_1(),
		Load5:                r.GetCpu().GetLoad_5(),
		Load15:               r.GetCpu().GetLoad_15(),
		MemoryTotalBytes:     r.GetMemory().GetTotalBytes(),
		MemoryUsedBytes:      r.GetMemory().GetUsedBytes(),
		MemoryAvailableBytes: r.GetMemory().GetAvailableBytes(),
		SwapTotalBytes:       r.GetMemory().GetSwapTotalBytes(),
		SwapUsedBytes:        r.GetMemory().GetSwapUsedBytes(),
		UptimeSeconds:        r.GetUptimeSeconds(),
		Disks:                []diskJSON{},
		Networks:             []networkJSON{},
		Unavailable:          r.GetUnavailable(),
	}
	for _, d := range r.GetDisks() {
		p.Disks = append(p.Disks, diskJSON{d.GetMountPoint(), d.GetFilesystem(), d.GetTotalBytes(), d.GetUsedBytes()})
	}

	var before map[string]*agentv1.NetworkMetrics
	var seconds float64
	if prev != nil {
		seconds = rateInterval(s, *prev)
		before = map[string]*agentv1.NetworkMetrics{}
		for _, n := range prev.Report.GetNetworks() {
			before[n.GetInterface()] = n
		}
	}
	for _, n := range r.GetNetworks() {
		out := networkJSON{Interface: n.GetInterface(), RxBytes: n.GetRxBytes(), TxBytes: n.GetTxBytes()}
		if old := before[n.GetInterface()]; old != nil && seconds > 0 &&
			n.GetRxBytes() >= old.GetRxBytes() && n.GetTxBytes() >= old.GetTxBytes() {
			rx := float64(n.GetRxBytes()-old.GetRxBytes()) / seconds
			tx := float64(n.GetTxBytes()-old.GetTxBytes()) / seconds
			out.RxBytesPerSec, out.TxBytesPerSec = &rx, &tx
		}
		p.Networks = append(p.Networks, out)
	}
	return p
}

// metricsHandler serves the recent metrics of an agent, oldest first. The
// optional query parameter since (RFC 3339) returns only points received after
// it, which lets a client poll cheaply; step (a Go duration from 1s to 1h)
// averages the reports into buckets of that length (see downsample), so a
// chart of the whole hour does not download every report; limit (a positive
// integer) returns only the newest that many points, so a chart of the last
// minutes does not download the whole hour. They apply in that order.
func metricsHandler(src AgentSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var since time.Time
		if v := r.URL.Query().Get("since"); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				http.Error(w, "since must be an RFC 3339 timestamp", http.StatusBadRequest)
				return
			}
			since = t
		}
		var step time.Duration
		if v := r.URL.Query().Get("step"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d < minMetricsStep || d > maxMetricsStep {
				http.Error(w, "step must be a duration from 1s to 1h, such as 30s or 1m", http.StatusBadRequest)
				return
			}
			step = d
		}
		limit := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
				return
			}
			limit = n
		}
		if src == nil {
			http.Error(w, "unknown agent", http.StatusNotFound)
			return
		}
		samples, known := src.Metrics(id)
		if !known {
			http.Error(w, "unknown agent", http.StatusNotFound)
			return
		}
		// Rates need the previous report, so they are computed before filtering.
		points := toPoints(samples)
		if !since.IsZero() {
			points = slices.DeleteFunc(points, func(p pointJSON) bool { return !p.At.After(since) })
		}
		points = downsample(points, step)
		if limit > 0 && len(points) > limit {
			points = points[len(points)-limit:]
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(metricsJSON{Agent: id, Points: points})
	}
}

// agentGetHandler serves one agent: a connected one, or an enrolled one that
// is offline or revoked.
func agentGetHandler(src AgentSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if src != nil {
			var enrolled []fleet.Agent
			if reg, ok := src.(registrySource); ok {
				var err error
				if enrolled, err = reg.Registry(r.Context()); err != nil {
					writeInternal(w)
					return
				}
			}
			now, staleAfter := time.Now(), src.StaleAfter()
			for _, a := range src.Agents() {
				if a.ID == id {
					j := toAgentJSON(a, now, staleAfter)
					for _, e := range enrolled {
						if e.ID == id {
							j.withRegistry(e)
						}
					}
					writeJSONBody(w, http.StatusOK, j)
					return
				}
			}
			for _, e := range enrolled {
				if e.ID == id {
					writeJSONBody(w, http.StatusOK, offlineJSON(e, src))
					return
				}
			}
		}
		writeJSONErrorCode(w, http.StatusNotFound, "unknown agent", codeAgentNotFound)
	}
}

func writeJSONBody(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSONBody(w, status, map[string]string{"error": msg})
}

// writeJSONErrorCode is writeJSONError with a code the dashboard translates
// (#354). The text stays English for scripts and for clients that do not know
// the code; every code used here is listed in errorCodes (errorcodes.go), and
// a test fails when one has no text in the catalogs.
func writeJSONErrorCode(w http.ResponseWriter, status int, msg, code string) {
	writeJSONBody(w, status, map[string]string{"error": msg, "code": code})
}

// writeInternal answers 500 without saying what went wrong (it is in the log).
func writeInternal(w http.ResponseWriter) {
	writeJSONErrorCode(w, http.StatusInternalServerError, "internal error", codeInternal)
}
