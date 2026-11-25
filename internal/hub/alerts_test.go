package hub

import (
	"context"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/alerts"
	"github.com/CodexCircle/Regulus/internal/config"
)

func openAlerts(t *testing.T, h *Hub) map[alerts.Condition][]alerts.Alert {
	t.Helper()
	list, err := h.Alerts().List(context.Background(), []alerts.State{alerts.StateOpen}, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := map[alerts.Condition][]alerts.Alert{}
	for _, a := range list {
		out[a.Condition] = append(out[a.Condition], a)
	}
	return out
}

// The snapshot is built from the registry and the live agents: an enrolled
// server with no connection is offline, one that reports is not, and the
// quiet defaults raise nothing for a healthy hub.
func TestAlertsSeeAnEnrolledServerThatIsNotConnected(t *testing.T) {
	h := startRemoteHub(t)
	creds := enrollNew(t, h, "db1") // enrolled, never connected
	ctx := context.Background()

	h.evaluateAlerts(ctx)
	if got := openAlerts(t, h); len(got) != 0 {
		t.Fatalf("a quiet hub raised %v", got)
	}
	snap, err := h.alertSnapshot(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range snap.Agents {
		if a.ID == creds.AgentID {
			found = !a.Connected
		}
	}
	if !found || len(snap.Certificates) < 2 { // the agent's, and the CA's
		t.Fatalf("snapshot = %+v", snap)
	}

	// The default waits five minutes; asked to wait none, it speaks.
	rules, _ := h.Alerts().Rules(ctx)
	for _, r := range rules {
		if r.Condition == alerts.AgentOffline {
			r.Duration = 0
			if err := h.Alerts().SetRule(ctx, r, "test"); err != nil {
				t.Fatal(err)
			}
		}
	}
	h.evaluateAlerts(ctx)
	if got := openAlerts(t, h)[alerts.AgentOffline]; len(got) != 1 || got[0].Subject != "agent:"+creds.AgentID || got[0].Severity != alerts.Critical {
		t.Fatalf("alerts = %+v", got)
	}
	// Once it connects and reports, the condition is false and the alert
	// resolves after its recovery time (a minute at least).
	runAgentVersion(t, h, creds, "9.9.9")
	waitFor(t, "the agent", func() bool { return len(h.Agents()) == 1 })
	h.evaluateAlerts(ctx)
	if got := openAlerts(t, h)[alerts.AgentOffline]; len(got) != 1 {
		t.Errorf("the alert resolved at once: %+v", got)
	}
}

// Losing the active-hub lock is an alert at once, from the hub that lost it.
func TestAlertForALostLock(t *testing.T) {
	h := startHubWithConfig(t, config.Default())
	if !h.demote() {
		t.Fatal("demote did nothing")
	}
	waitFor(t, "the alert", func() bool { return len(openAlerts(t, h)[alerts.HubLockLost]) == 1 })
	if a := openAlerts(t, h)[alerts.HubLockLost][0]; a.Severity != alerts.Critical || a.Subject != "hub" {
		t.Errorf("alert = %+v", a)
	}
}
