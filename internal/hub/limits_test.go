package hub

import (
	"strings"
	"testing"
	"time"

	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
)

func TestCleanText(t *testing.T) {
	long := strings.Repeat("é", maxTextBytes) // two bytes each: the cut must not split one
	for name, tc := range map[string]struct{ in, want string }{
		"plain":         {"host-1", "host-1"},
		"control chars": {"a\x1b[31mb\nc\x00", "a[31mbc"},
		"bad utf-8":     {"ok\xff\xfeok", "okok"},
		"empty":         {"", ""},
	} {
		if got := cleanText(tc.in); got != tc.want {
			t.Errorf("%s: cleanText(%q) = %q, want %q", name, tc.in, got, tc.want)
		}
	}
	got := cleanText(long)
	if len(got) > maxTextBytes || !strings.HasPrefix(long, got) || got == "" {
		t.Errorf("a long text was cut to %d bytes: %.20q", len(got), got)
	}
}

func TestSanitizeHello(t *testing.T) {
	h := &agentv1.Hello{
		AgentVersion: strings.Repeat("v", 5000),
		System:       &agentv1.SystemInfo{Hostname: strings.Repeat("h", 1<<20) + "\n", OsVersion: "Ubuntu\x1b]0;x\x07"},
	}
	sanitizeHello(h)
	if len(h.AgentVersion) != maxTextBytes || len(h.System.Hostname) != maxTextBytes {
		t.Errorf("texts not cut: %d and %d bytes", len(h.AgentVersion), len(h.System.Hostname))
	}
	if h.System.OsVersion != "Ubuntu]0;x" {
		t.Errorf("control characters kept: %q", h.System.OsVersion)
	}
	sanitizeHello(&agentv1.Hello{}) // no System: must not panic
}

func TestSanitizeReport(t *testing.T) {
	r := &agentv1.MetricsReport{}
	for i := 0; i < 10_000; i++ {
		r.Disks = append(r.Disks, &agentv1.DiskMetrics{MountPoint: strings.Repeat("/m", 1000)})
		r.Networks = append(r.Networks, &agentv1.NetworkMetrics{Interface: "eth\n0"})
	}
	if !sanitizeReport(r) {
		t.Fatal("a report with 10000 disks was not reported as cut")
	}
	if len(r.Disks) != maxListEntries || len(r.Networks) != maxListEntries {
		t.Errorf("lists not cut: %d disks, %d interfaces", len(r.Disks), len(r.Networks))
	}
	if len(r.Disks[0].MountPoint) > maxTextBytes || r.Networks[0].Interface != "eth0" {
		t.Errorf("texts not cleaned: %d bytes, %q", len(r.Disks[0].MountPoint), r.Networks[0].Interface)
	}
	u := &agentv1.MetricsReport{Collector: "basic\x1b", Unavailable: make([]string, 1000)}
	u.Unavailable[0] = "cpu\n"
	if !sanitizeReport(u) || len(u.Unavailable) != maxListEntries || u.Unavailable[0] != "cpu" || u.Collector != "basic" {
		t.Errorf("unavailable list not cut or cleaned: %d entries, %q, collector %q", len(u.Unavailable), u.Unavailable[0], u.Collector)
	}
	if sanitizeReport(&agentv1.MetricsReport{Disks: []*agentv1.DiskMetrics{{MountPoint: "/"}}}) {
		t.Error("a normal report was reported as cut")
	}
}

func TestReportTooSoon(t *testing.T) {
	now := time.Now()
	interval := 10 * time.Second
	for name, tc := range map[string]struct {
		last time.Time
		want bool
	}{
		"the first report":             {time.Time{}, false},
		"on time":                      {now.Add(-interval), false},
		"a little early (jitter)":      {now.Add(-6 * time.Second), false},
		"half the interval":            {now.Add(-5 * time.Second), false},
		"a flood":                      {now.Add(-time.Millisecond), true},
		"just under half the interval": {now.Add(-4900 * time.Millisecond), true},
	} {
		if got := reportTooSoon(tc.last, now, interval); got != tc.want {
			t.Errorf("%s: reportTooSoon = %v, want %v", name, got, tc.want)
		}
	}
	if reportTooSoon(now, now, 0) {
		t.Error("with no interval nothing must be dropped")
	}
}

// The update helper's result in Hello comes from a remote machine: an outcome
// the helper never writes drops it, and the texts are cut (#337).
func TestSanitizeUpdateResult(t *testing.T) {
	h := &agentv1.Hello{UpdateResult: &agentv1.UpdateResult{Outcome: "bogus", Message: "x"}}
	sanitizeHello(h)
	if h.UpdateResult != nil {
		t.Errorf("an unknown outcome was kept: %v", h.UpdateResult)
	}
	long := strings.Repeat("a", 10*maxTextBytes)
	h = &agentv1.Hello{UpdateResult: &agentv1.UpdateResult{Outcome: "failed", Message: long + "\x00", Args: map[string]string{"k": long}}}
	sanitizeHello(h)
	if len(h.UpdateResult.Message) > maxTextBytes || strings.ContainsRune(h.UpdateResult.Message, 0) || len(h.UpdateResult.Args["k"]) > maxTextBytes {
		t.Errorf("texts were not limited: %d, %d", len(h.UpdateResult.Message), len(h.UpdateResult.Args["k"]))
	}
}
