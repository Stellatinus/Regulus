package console

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/localstatus"
)

// systemWithCerts is the fake system whose hub reports these certificates.
func systemWithCerts(soonest *Certificate, warnings []Certificate) System {
	sys := fakeSystem()
	fetch := sys.Fetch
	sys.Fetch = func(ctx context.Context, dir string) (localstatus.Status, error) {
		st, err := fetch(ctx, dir)
		st.CertSoonest, st.CertWarnings = soonest, warnings
		return st, err
	}
	return sys
}

func certCheckOf(t *testing.T, snap Snapshot) (Check, bool) {
	t.Helper()
	for _, c := range snap.Health {
		if c.Name == "Certificates" {
			return c, true
		}
	}
	return Check{}, false
}

func TestCertificatesInStatus(t *testing.T) {
	defer func() { T = en }()
	cfgFile := copyConfig(t)
	day := 24 * time.Hour

	// An older hub reports nothing: no row, no check.
	snap := Collect(context.Background(), fakeSystem(), cfgFile, "v1")
	if _, ok := certCheckOf(t, snap); ok || snap.CertSoonest != nil {
		t.Errorf("a hub without certificate dates: %+v", snap.Health)
	}

	// Nothing ends soon: the check is fine and names the first to end.
	listener := &Certificate{Kind: "listener", NotAfter: fixedNow.Add(300*day + time.Hour)}
	snap = Collect(context.Background(), systemWithCerts(listener, nil), cfgFile, "v1")
	c, ok := certCheckOf(t, snap)
	if !ok || c.Status != OK || !strings.Contains(c.Detail, "agent listener certificate") || !strings.Contains(c.Detail, "in 300 days") {
		t.Errorf("check = %+v", c)
	}
	var out bytes.Buffer
	if err := WriteStatus(&out, snap, FormatText, fixedNow); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Certificates") || !strings.Contains(out.String(), "ends on 2027-08-21, in 300 days") {
		t.Errorf("status text:\n%s", out.String())
	}

	// An agent certificate ends in 20 days and another has ended: a warning
	// with the way to renew them.
	agent := Certificate{Kind: "agent", AgentID: "agent-1", Name: "web1", NotAfter: fixedNow.Add(20*day + time.Hour)}
	ended := Certificate{Kind: "agent", AgentID: "agent-2", NotAfter: fixedNow.Add(-day)}
	snap = Collect(context.Background(), systemWithCerts(&ended, []Certificate{ended, agent}), cfgFile, "v1")
	c, _ = certCheckOf(t, snap)
	for _, want := range []string{"certificate of agent agent-2: ended on 2026-10-24", "certificate of agent web1 (agent-1): ends on 2026-11-14, in 20 days", "#128", "enroll it again"} {
		if c.Status != Warn || !strings.Contains(c.Detail, want) {
			t.Errorf("check %+v does not say %q", c, want)
		}
	}
	if strings.Contains(c.Detail, "new CA") {
		t.Errorf("check %+v gives the CA hint for agent certificates", c)
	}
	out.Reset()
	if err := WriteStatus(&out, snap, FormatPlain, fixedNow); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "cert_soonest=agent agent-2 2026-10-24T12:00:00Z\n") || !strings.Contains(out.String(), "cert_warning=agent agent-1 2026-11-14T13:00:00Z\n") {
		t.Errorf("plain status:\n%s", out.String())
	}

	// The CA, in Italian.
	T = it
	ca := Certificate{Kind: "ca", NotAfter: fixedNow.Add(60*day + time.Hour)}
	snap = Collect(context.Background(), systemWithCerts(&ca, []Certificate{ca}), cfgFile, "v1")
	c, _ = certCheckOf(t, snap)
	if c.Status != Warn || !strings.Contains(c.Detail, "della CA: scade il 2026-12-24, tra 60 giorni") || !strings.Contains(c.Detail, "nuova CA") {
		t.Errorf("check = %+v", c)
	}
}
