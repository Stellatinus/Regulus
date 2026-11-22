package hub

// GET /metrics: the hub's own figures and the latest figures of each server in
// the Prometheus text format (#389), written by hand (no client library: the
// format is a few lines of text, see docs/metrics.md and docs/decisions.md).
// It is off unless prometheus.enabled is set, and it is not behind a login:
// a scraper cannot sign in, so it carries a bearer token (prometheus.token).

import (
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/CodexCircle/Regulus/internal/fleet"
)

const (
	// promMaxDisks and promMaxInterfaces bound the series one server adds: a
	// server with hundreds of mounts or interfaces (containers, bind mounts)
	// would otherwise multiply the series of the whole fleet. The first
	// entries of the report are kept; regulus_server_series_cut says when
	// some were left out.
	promMaxDisks      = 32
	promMaxInterfaces = 16
	// promContentType is the text format version 0.0.4.
	promContentType = "text/plain; version=0.0.4; charset=utf-8"
)

// Stats are the counters and the timings the hub keeps for GET /metrics. The
// zero value is ready, and a nil *Stats ignores everything, so code that
// counts need not check. Counters live as long as the process.
type Stats struct {
	reportsReceived atomic.Int64
	reportsRejected atomic.Int64
	streams         atomic.Int64
	// dbReadNs and dbWriteNs are the last measured duration in nanoseconds,
	// or 0 while none was measured (a duration of 0 does not happen).
	dbReadNs  atomic.Int64
	dbWriteNs atomic.Int64
}

func (s *Stats) reportReceived() {
	if s != nil {
		s.reportsReceived.Add(1)
	}
}

// reportRejected counts a report the hub dropped (sent faster than the
// interval).
func (s *Stats) reportRejected() {
	if s != nil {
		s.reportsRejected.Add(1)
	}
}

// streamOpened and streamClosed track the dashboard streams open now.
func (s *Stats) streamOpened() {
	if s != nil {
		s.streams.Add(1)
	}
}

func (s *Stats) streamClosed() {
	if s != nil {
		s.streams.Add(-1)
	}
}

func (s *Stats) dbRead(d time.Duration) {
	if s != nil {
		s.dbReadNs.Store(max(int64(d), 1))
	}
}

func (s *Stats) dbWrite(d time.Duration) {
	if s != nil {
		s.dbWriteNs.Store(max(int64(d), 1))
	}
}

// promSource is what the metrics endpoint reads. Every part may be nil (tests
// build only what they check); a missing part leaves its metrics out.
type promSource struct {
	token        string
	version      string
	agents       AgentSource
	certificates CertificateSource
	updates      UpdateControl
	rollout      RolloutControl
	dbProblem    func() string
	stats        *Stats
	now          func() time.Time
}

// metricsEndpointHandler serves GET /metrics for the bearer token. A request
// without it, or with another one, is refused with 401 and nothing else.
func metricsEndpointHandler(src promSource) http.Handler {
	want := sha256.Sum256([]byte(src.token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := sha256.Sum256([]byte(bearerToken(r)))
		// Compare the hashes: the length of the token is not leaked either.
		if src.token == "" || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="regulus-metrics"`)
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "a bearer token is needed", http.StatusUnauthorized)
			return
		}
		var out promWriter
		src.collect(r.Context(), &out)
		w.Header().Set("Content-Type", promContentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(out.bytes())
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (p promSource) collect(ctx context.Context, w *promWriter) {
	now := time.Now()
	if p.now != nil {
		now = p.now()
	}
	w.gauge("regulus_build_info", "The version of this hub; the value is always 1.", 1, "version", p.version)
	p.collectFleet(ctx, w)
	if p.stats != nil {
		w.counter("regulus_reports_received_total", "Metrics reports accepted from agents.", float64(p.stats.reportsReceived.Load()))
		w.counter("regulus_reports_rejected_total", "Metrics reports dropped because the agent sent them faster than the interval.", float64(p.stats.reportsRejected.Load()))
		w.gauge("regulus_stream_clients", "Dashboard streams (WebSockets) open now.", float64(max(p.stats.streams.Load(), 0)))
		if ns := p.stats.dbReadNs.Load(); ns > 0 {
			w.gauge("regulus_database_read_seconds", "Duration of the last database check (a ping), made every 15 seconds.", time.Duration(ns).Seconds())
		}
		if ns := p.stats.dbWriteNs.Load(); ns > 0 {
			w.gauge("regulus_database_write_seconds", "Duration of the last write of agent presence to the database (about once a minute per connected agent).", time.Duration(ns).Seconds())
		}
	}
	if p.dbProblem != nil {
		w.gauge("regulus_database_up", "1 while the database answers, 0 while it does not.", b2f(p.dbProblem() == ""))
	}
	p.collectUpdates(ctx, w)
	p.collectCertificates(ctx, w, now)
	p.collectServers(ctx, w)
}

func (p promSource) collectFleet(ctx context.Context, w *promWriter) {
	if p.agents == nil {
		return
	}
	connected := p.agents.Agents()
	w.gauge("regulus_agents_connected", "Agents connected to the hub now.", float64(len(connected)))
	reg, ok := p.agents.(registrySource)
	if !ok {
		return
	}
	enrolled, err := reg.Registry(ctx)
	if err != nil {
		return
	}
	var revoked int
	for _, e := range enrolled {
		if e.Revoked {
			revoked++
		}
	}
	w.gauge("regulus_agents_enrolled", "Agents enrolled, revoked ones not counted.", float64(len(enrolled)-revoked))
	w.gauge("regulus_agents_revoked", "Agents that were revoked.", float64(revoked))
}

func (p promSource) collectUpdates(ctx context.Context, w *promWriter) {
	if p.updates != nil {
		if st, err := p.updates.Status(ctx); err == nil {
			w.gauge("regulus_update_available", "1 when a newer release than this hub is known.", b2f(st.UpdateAvailable))
			w.gauge("regulus_update_staged", "1 when a downloaded and verified release waits to be installed.", b2f(st.Staged != nil))
			w.gauge("regulus_update_check_stale", "1 when the last successful check for releases is older than it should be.", b2f(st.CheckStale))
		}
	}
	if p.rollout != nil {
		if st, err := p.rollout.RolloutStatus(ctx); err == nil {
			for _, state := range []string{RolloutIdle, RolloutRunning, RolloutDone, RolloutFailed, RolloutStopped} {
				w.gauge("regulus_rollout_state", "1 for the state of the last update of the servers, 0 for the others.", b2f(st.State == state), "state", state)
			}
			w.gauge("regulus_rollout_candidates", "Connected servers that run an older version than the hub.", float64(len(st.Candidates)))
		}
	}
}

func (p promSource) collectCertificates(ctx context.Context, w *promWriter, now time.Time) {
	if p.certificates == nil {
		return
	}
	c, err := p.certificates.Certificates(ctx)
	if err != nil {
		return
	}
	days := func(t time.Time) float64 { return math.Floor(t.Sub(now).Hours()/24*100) / 100 }
	const help = "Days until a certificate ends (negative once it has ended). For agent: the agent whose certificate ends first."
	w.gauge("regulus_certificate_days_left", help, days(c.CA.NotAfter), "kind", "ca")
	if c.Listener != nil {
		w.gauge("regulus_certificate_days_left", help, days(c.Listener.NotAfter), "kind", "listener")
	}
	if len(c.Agents) > 0 { // sorted by end, soonest first
		w.gauge("regulus_certificate_days_left", help, days(c.Agents[0].NotAfter), "kind", "agent")
	}
	w.gauge("regulus_certificate_agents_unknown", "Agents whose certificate end the hub does not know yet.", float64(c.AgentsUnknown))
	w.gauge("regulus_certificate_legacy_agents", "Connected agents with a certificate that has no kind (issued before #316).", float64(c.LegacyAgents))
}

// collectServers writes the latest report of each connected server. The label
// "server" is the display name, else the host name, else the id; "id" never
// changes.
func (p promSource) collectServers(ctx context.Context, w *promWriter) {
	if p.agents == nil {
		return
	}
	names := map[string]string{}
	if reg, ok := p.agents.(registrySource); ok {
		if enrolled, err := reg.Registry(ctx); err == nil {
			for _, e := range enrolled {
				names[e.ID] = serverLabel(e)
			}
		}
	}
	now := time.Now()
	if p.now != nil {
		now = p.now()
	}
	staleAfter := p.agents.StaleAfter()
	for _, a := range p.agents.Agents() {
		name := cmp.Or(names[a.ID], a.ID)
		labels := []string{"id", a.ID, "server", name}
		stale := toAgentJSON(a, now, staleAfter).Status == statusStale
		w.gauge("regulus_server_up", "1 for a connected server, 0 for one that is connected but silent (stale).", b2f(!stale), labels...)
		if a.UpdateOnly || a.LastMetrics == nil {
			continue
		}
		r := a.LastMetrics
		unavailable := func(part string) bool { return slices.Contains(r.GetUnavailable(), part) }
		if !unavailable("cpu") {
			w.gauge("regulus_server_cpu_usage_percent", "CPU use over the last interval, 0 to 100 across all cores.", r.GetCpu().GetUsagePercent(), labels...)
		}
		if !unavailable("load") {
			w.gauge("regulus_server_load1", "Load average over 1 minute.", r.GetCpu().GetLoad_1(), labels...)
			w.gauge("regulus_server_load5", "Load average over 5 minutes.", r.GetCpu().GetLoad_5(), labels...)
			w.gauge("regulus_server_load15", "Load average over 15 minutes.", r.GetCpu().GetLoad_15(), labels...)
		}
		if !unavailable("memory") {
			m := r.GetMemory()
			w.gauge("regulus_server_memory_total_bytes", "Memory installed.", float64(m.GetTotalBytes()), labels...)
			w.gauge("regulus_server_memory_used_bytes", "Memory in use.", float64(m.GetUsedBytes()), labels...)
			w.gauge("regulus_server_memory_available_bytes", "Memory available to programs.", float64(m.GetAvailableBytes()), labels...)
			w.gauge("regulus_server_swap_total_bytes", "Swap space.", float64(m.GetSwapTotalBytes()), labels...)
			w.gauge("regulus_server_swap_used_bytes", "Swap in use.", float64(m.GetSwapUsedBytes()), labels...)
		}
		if !unavailable("uptime") {
			w.gauge("regulus_server_uptime_seconds", "Time since the server booted.", float64(r.GetUptimeSeconds()), labels...)
		}
		cut := 0
		if !unavailable("disks") {
			disks := r.GetDisks()
			if len(disks) > promMaxDisks {
				cut += len(disks) - promMaxDisks
				disks = disks[:promMaxDisks]
			}
			for _, d := range disks {
				dl := append(slices.Clone(labels), "mount", d.GetMountPoint(), "filesystem", d.GetFilesystem())
				w.gauge("regulus_server_disk_total_bytes", "Size of a mounted filesystem.", float64(d.GetTotalBytes()), dl...)
				w.gauge("regulus_server_disk_used_bytes", "Space used on a mounted filesystem.", float64(d.GetUsedBytes()), dl...)
			}
		}
		if !unavailable("network") {
			nets := r.GetNetworks()
			if len(nets) > promMaxInterfaces {
				cut += len(nets) - promMaxInterfaces
				nets = nets[:promMaxInterfaces]
			}
			rates := p.networkRates(a.ID)
			for _, n := range nets {
				nl := append(slices.Clone(labels), "interface", n.GetInterface())
				w.counter("regulus_server_network_receive_bytes_total", "Bytes received on an interface since the server booted.", float64(n.GetRxBytes()), nl...)
				w.counter("regulus_server_network_transmit_bytes_total", "Bytes sent on an interface since the server booted.", float64(n.GetTxBytes()), nl...)
				if rt, ok := rates[n.GetInterface()]; ok {
					w.gauge("regulus_server_network_receive_bytes_per_second", "Receive rate between the last two reports.", rt.rx, nl...)
					w.gauge("regulus_server_network_transmit_bytes_per_second", "Transmit rate between the last two reports.", rt.tx, nl...)
				}
			}
		}
		w.gauge("regulus_server_series_cut", "Disks and interfaces of the last report left out to bound the number of series (32 disks and 16 interfaces per server).", float64(cut), labels...)
	}
}

type netRate struct{ rx, tx float64 }

// networkRates reuses the point of the metrics API (toPoint): the rate is
// computed by the same code, from the last two reports.
func (p promSource) networkRates(agentID string) map[string]netRate {
	samples, ok := p.agents.Metrics(agentID)
	if !ok || len(samples) < 2 {
		return nil
	}
	last := toPoint(samples[len(samples)-1], &samples[len(samples)-2])
	out := map[string]netRate{}
	for _, n := range last.Networks {
		if n.RxBytesPerSec != nil && n.TxBytesPerSec != nil {
			out[n.Interface] = netRate{*n.RxBytesPerSec, *n.TxBytesPerSec}
		}
	}
	return out
}

func serverLabel(e fleet.Agent) string { return cmp.Or(e.Name, e.Hostname, e.ID) }

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// promWriter builds the text format: the samples of one metric are written
// together under one HELP and TYPE line, in the order the metrics first
// appeared.
type promWriter struct {
	order    []string
	families map[string]*promFamily
}

type promFamily struct {
	help, typ string
	samples   []string
}

func (w *promWriter) add(name, help, typ string, v float64, labels []string) {
	if w.families == nil {
		w.families = map[string]*promFamily{}
	}
	f := w.families[name]
	if f == nil {
		f = &promFamily{help: help, typ: typ}
		w.families[name] = f
		w.order = append(w.order, name)
	}
	var b strings.Builder
	b.WriteString(name)
	if len(labels) > 0 {
		b.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(labels[i])
			b.WriteString(`="`)
			b.WriteString(promEscape(labels[i+1]))
			b.WriteByte('"')
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(promFloat(v))
	f.samples = append(f.samples, b.String())
}

func (w *promWriter) gauge(name, help string, v float64, labels ...string) {
	w.add(name, help, "gauge", v, labels)
}

func (w *promWriter) counter(name, help string, v float64, labels ...string) {
	w.add(name, help, "counter", v, labels)
}

func (w *promWriter) bytes() []byte {
	var b strings.Builder
	for _, name := range w.order {
		f := w.families[name]
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(f.help), name, f.typ)
		sort.Strings(f.samples)
		for _, s := range f.samples {
			b.WriteString(s)
			b.WriteByte('\n')
		}
	}
	return []byte(b.String())
}

// promEscape escapes a label value: backslash, double quote and line feed.
func promEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(strings.ToValidUTF8(s, "�"))
}

func promFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
