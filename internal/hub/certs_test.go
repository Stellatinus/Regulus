package hub

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/config"
)

func TestCertificateWarningsAndSoonest(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	c := Certificates{
		CA:       CertEnd{Kind: CertKindCA, NotAfter: now.Add(400 * day)},
		Listener: &CertEnd{Kind: CertKindListener, NotAfter: now.Add(10 * day)},
		Agents: []CertEnd{
			{Kind: CertKindAgent, AgentID: "a", NotAfter: now.Add(89 * day)},
			{Kind: CertKindAgent, AgentID: "b", NotAfter: now.Add(91 * day)},
			{Kind: CertKindAgent, AgentID: "c", NotAfter: now.Add(-day)},
		},
	}
	if s := c.Soonest(); s.AgentID != "c" {
		t.Errorf("soonest = %+v, want the agent c that has ended", s)
	}
	w := c.Warnings(now)
	// The listener renews itself, so it is never a warning even when it ends first.
	if len(w) != 2 || w[0].AgentID != "c" || w[1].AgentID != "a" {
		t.Fatalf("warnings = %+v, want c then a", w)
	}

	c.CA.NotAfter = now.Add(30 * day)
	if w := c.Warnings(now); len(w) != 3 || w[1].Kind != CertKindCA {
		t.Errorf("a CA ending in 30 days: warnings = %+v", w)
	}

	c.Agents = nil
	if s := c.Soonest(); s.Kind != CertKindListener {
		t.Errorf("soonest = %+v, want the listener", s)
	}
	c.Listener = nil
	if s := c.Soonest(); s.Kind != CertKindCA {
		t.Errorf("soonest = %+v, want the CA", s)
	}

	j := toCertificatesJSON(c, now)
	if j.Listener != nil || j.CA.DaysLeft != 30 || j.WarnDays != 90 || len(j.Warnings) != 1 {
		t.Errorf("json = %+v", j)
	}
	if d := daysLeft(now, now.Add(-time.Minute)); d != -1 {
		t.Errorf("daysLeft of a certificate that just ended = %d, want -1", d)
	}
}

// fleetTS is how package fleet stores times.
func fleetTS(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

func TestCertificatesAPIAndTheServerList(t *testing.T) {
	h := startRemoteHub(t)
	admin := newAPI(t, h, auth.RoleAdmin)
	creds := enrollNew(t, h, "web1")

	list := admin.agents("?all=true")
	if len(list) != 1 || list[0].CertNotAfter == nil {
		t.Fatalf("agents = %+v, want the end of the certificate", list)
	}
	if left := time.Until(*list[0].CertNotAfter); left < 4*365*24*time.Hour {
		t.Errorf("the agent certificate ends in %v, want about five years", left)
	}

	get := func() certificatesJSON {
		t.Helper()
		rec := admin.do("GET", "/api/certificates", nil)
		var c certificatesJSON
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &c) != nil {
			t.Fatalf("GET /api/certificates: status = %d, body = %s", rec.Code, rec.Body)
		}
		return c
	}
	c := get()
	if c.CA.Kind != CertKindCA || !c.CA.NotAfter.Equal(h.fleet.CA().NotAfter()) || c.CA.DaysLeft < 7000 {
		t.Errorf("ca = %+v", c.CA)
	}
	if c.Listener == nil || c.Listener.DaysLeft < 360 || c.Soonest.Kind != CertKindListener {
		t.Errorf("listener = %+v, soonest = %+v", c.Listener, c.Soonest)
	}
	if len(c.Warnings) != 0 || c.WarnDays != 90 || c.AgentsUnknown != 0 {
		t.Errorf("certificates = %+v", c)
	}

	// The agent certificate ends in 30 days: a warning, and the soonest.
	ends := time.Now().Add(30*24*time.Hour + time.Hour).UTC()
	if _, err := h.db.Exec(`UPDATE agents SET cert_not_after = ?`, fleetTS(ends)); err != nil {
		t.Fatal(err)
	}
	c = get()
	if len(c.Warnings) != 1 || c.Warnings[0].AgentID != creds.AgentID || c.Warnings[0].Name != "web1" || c.Warnings[0].DaysLeft != 30 || c.Soonest.AgentID != creds.AgentID {
		t.Errorf("warnings = %+v, soonest = %+v", c.Warnings, c.Soonest)
	}

	// An agent enrolled before the hub kept the date: unknown until it
	// connects, then read from its certificate.
	if _, err := h.db.Exec(`UPDATE agents SET cert_not_after = NULL`); err != nil {
		t.Fatal(err)
	}
	if c = get(); c.AgentsUnknown != 1 || len(c.Warnings) != 0 {
		t.Errorf("unknown date: %+v", c)
	}
	if list := admin.agents("?all=true"); list[0].CertNotAfter != nil {
		t.Errorf("cert_not_after = %v, want none", list[0].CertNotAfter)
	}
	leaf, err := x509.ParseCertificate(creds.CertDER)
	if err != nil {
		t.Fatal(err)
	}
	runAgent(t, h, creds)
	waitFor(t, "the end of the certificate to be read", func() bool {
		list := admin.agents("")
		return len(list) == 1 && list[0].CertNotAfter != nil && list[0].CertNotAfter.Equal(leaf.NotAfter)
	})
	rec := admin.do("GET", "/api/agents/"+creds.AgentID, nil)
	var one agentJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &one) != nil || one.CertNotAfter == nil {
		t.Errorf("GET one agent: status = %d, body = %s", rec.Code, rec.Body)
	}

	// A revoked agent is never warned about.
	if _, err := h.db.Exec(`UPDATE agents SET cert_not_after = ?`, fleetTS(ends)); err != nil {
		t.Fatal(err)
	}
	if err := h.RevokeAgent(context.Background(), "admin", creds.AgentID); err != nil {
		t.Fatal(err)
	}
	if c = get(); len(c.Warnings) != 0 || c.AgentsUnknown != 0 {
		t.Errorf("revoked: %+v", c)
	}
}

// syncBuffer is a log destination the test reads while the hub writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestCertificatesEndingSoonAreLoggedOnceADay(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	useTestDatabase(t, &cfg)
	cfg.Updates.Check = false
	var logs syncBuffer
	h, err := New(ctx, cfg, slog.New(slog.NewTextHandler(&logs, nil)), fs.FS(fstest.MapFS{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	now := time.Now()
	if h.logCertificates(ctx, now) {
		t.Fatal("logged with nothing ending soon")
	}
	if _, err := h.db.Exec(`INSERT INTO agents (id, name, hostname, created_at, cert_serial, cert_not_after) VALUES ('agent-1', 'db1', 'db1.lan', ?, 'ab', ?)`,
		fleetTS(now), fleetTS(now.Add(10*24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if !h.logCertificates(ctx, now) {
		t.Fatal("not logged with an agent certificate ending in 10 days")
	}
	line := logs.String()
	for _, want := range []string{"level=WARN", "certificates end within 90 days", "count=1", "agent agent-1 (db1)", "#128", "enroll it again"} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q does not contain %q", line, want)
		}
	}
	if strings.Contains(line, "a new CA") {
		t.Errorf("log %q gives the CA hint for an agent certificate", line)
	}
	if h.logCertificates(ctx, now.Add(23*time.Hour)) {
		t.Error("logged twice in a day")
	}
	if !h.logCertificates(ctx, now.Add(24*time.Hour)) {
		t.Error("not logged again the next day")
	}
}
