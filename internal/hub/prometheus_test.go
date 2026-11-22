package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/fleet"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const testMetricsToken = "0123456789abcdef0123456789abcdef-test"

// promLine is one line of the text format: a comment, or name{labels} value.
var promLine = regexp.MustCompile(`^(# (HELP|TYPE) [a-zA-Z_:][a-zA-Z0-9_:]* .+|[a-zA-Z_:][a-zA-Z0-9_:]*(\{([a-zA-Z_][a-zA-Z0-9_]*="([^"\\\n]|\\.)*",?)*\})? (-?[0-9.]+(e[+-]?[0-9]+)?|NaN|[+-]Inf))$`)

func scrape(t *testing.T, h http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type registrySource2 struct {
	fakeSource
	enrolled []fleet.Agent
}

func (r registrySource2) Registry(context.Context) ([]fleet.Agent, error) { return r.enrolled, nil }

func promReport(at time.Time, rx, tx uint64) *agentv1.MetricsReport {
	return &agentv1.MetricsReport{
		CollectedAt:   timestamppb.New(at),
		Cpu:           &agentv1.CpuMetrics{UsagePercent: 12.5, Load_1: 0.5, Load_5: 0.25, Load_15: 0.125},
		Memory:        &agentv1.MemoryMetrics{TotalBytes: 1000, UsedBytes: 400, AvailableBytes: 600, SwapTotalBytes: 100, SwapUsedBytes: 10},
		Disks:         []*agentv1.DiskMetrics{{MountPoint: "/", Filesystem: "ext4", TotalBytes: 5000, UsedBytes: 2000}},
		Networks:      []*agentv1.NetworkMetrics{{Interface: "eth0", RxBytes: rx, TxBytes: tx}},
		UptimeSeconds: 99,
	}
}

func TestMetricsEndpointNeedsTheToken(t *testing.T) {
	h := NewHandler(Deps{UI: fstest.MapFS{}, MetricsToken: testMetricsToken})
	for name, token := range map[string]string{"none": "", "wrong": "not-the-token", "prefix": testMetricsToken[:20]} {
		rec := scrape(t, h, token)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s token: status = %d, want 401", name, rec.Code)
		}
		if rec.Header().Get("WWW-Authenticate") == "" || strings.Contains(rec.Body.String(), "regulus_") {
			t.Errorf("%s token: header %q, body %q", name, rec.Header().Get("WWW-Authenticate"), rec.Body)
		}
	}
	// A session cookie is not a token.
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.AddCookie(&http.Cookie{Name: "regulus_session", Value: "x"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("cookie: status = %d, want 401", rec.Code)
	}
	if rec := scrape(t, h, testMetricsToken); rec.Code != http.StatusOK {
		t.Errorf("right token: status = %d, want 200", rec.Code)
	}
}

func TestMetricsEndpointIsOffWithoutAToken(t *testing.T) {
	h := NewHandler(Deps{UI: fstest.MapFS{"index.html": {Data: []byte("<html>")}}})
	if rec := scrape(t, h, testMetricsToken); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 while prometheus.enabled is off", rec.Code)
	}
}

func TestMetricsEndpointFormatAndFigures(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	src := registrySource2{
		fakeSource: fakeSource{
			staleAfter: time.Minute,
			agents: []AgentInfo{{
				ID: "agent-1", LastHeartbeat: now, ConnectedAt: now,
				System:      &agentv1.SystemInfo{Hostname: "host \"one\"\n"},
				LastMetrics: promReport(now, 3000, 1000),
			}},
			metrics: map[string][]MetricsSample{"agent-1": {
				{At: now.Add(-10 * time.Second), Report: promReport(now.Add(-10*time.Second), 1000, 500)},
				{At: now, Report: promReport(now, 3000, 1000)},
			}},
		},
		enrolled: []fleet.Agent{{ID: "agent-1", Name: "web"}, {ID: "agent-2", Revoked: true}, {ID: "agent-3"}},
	}
	stats := &Stats{}
	stats.reportReceived()
	stats.reportReceived()
	stats.reportRejected()
	stats.streamOpened()
	stats.dbRead(2 * time.Millisecond)
	stats.dbWrite(5 * time.Millisecond)
	ps := promSource{
		token: testMetricsToken, version: "1.2.3", agents: src, stats: stats,
		dbProblem: func() string { return "" }, now: func() time.Time { return now },
		certificates: fakeCerts{Certificates{
			CA:            CertEnd{Kind: CertKindCA, NotAfter: now.Add(48 * time.Hour)},
			Agents:        []CertEnd{{Kind: CertKindAgent, AgentID: "agent-1", NotAfter: now.Add(24 * time.Hour)}},
			LegacyAgents:  1,
			AgentsUnknown: 2,
		}},
	}
	rec := scrape(t, metricsEndpointHandler(ps), testMetricsToken)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != promContentType {
		t.Fatalf("status %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if !promLine.MatchString(line) {
			t.Errorf("not a valid line of the text format: %q", line)
		}
	}
	// A family is written once, HELP and TYPE first, and its samples together.
	if n := strings.Count(body, "# TYPE regulus_server_up "); n != 1 {
		t.Errorf("regulus_server_up declared %d times", n)
	}
	for _, want := range []string{
		`regulus_build_info{version="1.2.3"} 1`,
		`regulus_agents_connected 1`,
		`regulus_agents_enrolled 2`,
		`regulus_agents_revoked 1`,
		`regulus_stream_clients 1`,
		`regulus_reports_received_total 2`,
		`regulus_reports_rejected_total 1`,
		`regulus_database_up 1`,
		`regulus_database_read_seconds 0.002`,
		`regulus_database_write_seconds 0.005`,
		`regulus_certificate_days_left{kind="ca"} 2`,
		`regulus_certificate_days_left{kind="agent"} 1`,
		`regulus_certificate_legacy_agents 1`,
		`regulus_certificate_agents_unknown 2`,
		`regulus_server_up{id="agent-1",server="web"} 1`,
		`regulus_server_cpu_usage_percent{id="agent-1",server="web"} 12.5`,
		`regulus_server_memory_used_bytes{id="agent-1",server="web"} 400`,
		`regulus_server_disk_used_bytes{id="agent-1",server="web",mount="/",filesystem="ext4"} 2000`,
		`regulus_server_network_receive_bytes_total{id="agent-1",server="web",interface="eth0"} 3000`,
		`regulus_server_network_receive_bytes_per_second{id="agent-1",server="web",interface="eth0"} 200`,
		`regulus_server_network_transmit_bytes_per_second{id="agent-1",server="web",interface="eth0"} 50`,
		`regulus_server_series_cut{id="agent-1",server="web"} 0`,
	} {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestMetricsEndpointEscapesLabelsAndBoundsSeries(t *testing.T) {
	now := time.Now()
	r := promReport(now, 1, 1)
	r.Disks, r.Networks = nil, nil
	for i := 0; i < promMaxDisks+5; i++ {
		r.Disks = append(r.Disks, &agentv1.DiskMetrics{MountPoint: "/m" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Filesystem: "ext4"})
	}
	for i := 0; i < promMaxInterfaces+3; i++ {
		r.Networks = append(r.Networks, &agentv1.NetworkMetrics{Interface: "veth" + string(rune('a'+i))})
	}
	src := fakeSource{staleAfter: time.Minute, agents: []AgentInfo{{ID: "agent-1", LastHeartbeat: now, ConnectedAt: now, LastMetrics: r,
		System: &agentv1.SystemInfo{Hostname: "a\"b\\c\nd"}}}}
	var w promWriter
	promSource{agents: registrySource2{fakeSource: src, enrolled: []fleet.Agent{{ID: "agent-1", Hostname: "a\"b\\c\nd"}}}}.collectServers(context.Background(), &w)
	body := string(w.bytes())
	if !strings.Contains(body, `server="a\"b\\c\nd"`) {
		t.Errorf("label not escaped:\n%s", body)
	}
	if n := strings.Count(body, "regulus_server_disk_total_bytes{"); n != promMaxDisks {
		t.Errorf("%d disk series, want %d", n, promMaxDisks)
	}
	if n := strings.Count(body, "regulus_server_network_receive_bytes_total{"); n != promMaxInterfaces {
		t.Errorf("%d interface series, want %d", n, promMaxInterfaces)
	}
	if !strings.Contains(body, `regulus_server_series_cut{id="agent-1",server="a\"b\\c\nd"} 8`) {
		t.Errorf("cut count missing:\n%s", body)
	}
}

type fakeCerts struct{ c Certificates }

func (f fakeCerts) Certificates(context.Context) (Certificates, error) { return f.c, nil }

// TestMetricsEndpointOnARealHub scrapes a running hub: the built-in agent is
// connected, the figures of the hub and of that server are there.
func TestMetricsEndpointOnARealHub(t *testing.T) {
	cfg := config.Default()
	cfg.Prometheus = config.Prometheus{Enabled: true, Token: testMetricsToken}
	h := startHubWithConfig(t, cfg)
	waitFor(t, "built-in agent metrics", func() bool {
		a := h.Agents()
		return len(a) == 1 && a[0].LastMetrics != nil
	})
	handler := NewHandler(Deps{UI: fstest.MapFS{}, Agents: h, Certificates: h, Updates: h.updates, Rollout: h, DatabaseProblem: h.dbState.get,
		Version: h.version, Stats: h.stats, MetricsToken: h.cfg.Prometheus.Token})
	if rec := scrape(t, handler, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status = %d", rec.Code)
	}
	rec := scrape(t, handler, testMetricsToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{`regulus_build_info{version="9.9.9"} 1`, "regulus_agents_connected 1\n", "regulus_database_up 1\n",
		`regulus_server_up{id="` + BuiltinAgentID + `"`, "regulus_server_memory_total_bytes{", "regulus_reports_received_total ", "regulus_certificate_days_left{kind=\"ca\"} ", `regulus_rollout_state{state="idle"} 1`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if !promLine.MatchString(line) {
			t.Errorf("not a valid line of the text format: %q", line)
		}
	}
}
