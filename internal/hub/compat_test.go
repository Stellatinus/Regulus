package hub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/agent"
	"github.com/CodexCircle/Regulus/internal/auth"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"github.com/CodexCircle/Regulus/internal/protocol"
	"github.com/CodexCircle/Regulus/internal/update"
)

// runAgentVersion is runAgent for an agent that reports another release.
func runAgentVersion(t *testing.T, h *Hub, creds *agent.Credentials, version string) (stop func()) {
	t.Helper()
	conn, err := creds.Dial(h.AgentAddr())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = agent.Run(ctx, conn, agent.Options{
			Version: version, ID: creds.AgentID, Log: quiet(),
			MinBackoff: 20 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
		})
		_ = conn.Close()
	}()
	stop = func() { cancel(); <-done }
	t.Cleanup(stop)
	return stop
}

// The hub (9.9.9 in these tests) takes agents of its own series and the
// previous one and tells the dashboard which are behind. An enrolled agent
// that is further behind is not refused any more: it is connected only to be
// updated (#212), and the dashboard says so.
func TestAgentsOfOtherReleasesAreSortedByCompatibility(t *testing.T) {
	h := startRemoteHub(t)
	admin := newAPI(t, h, auth.RoleAdmin)

	current := enrollNew(t, h, "current")
	behind := enrollNew(t, h, "behind")
	ahead := enrollNew(t, h, "ahead")
	tooOld := enrollNew(t, h, "old")
	runAgentVersion(t, h, current, "9.9.9")
	runAgentVersion(t, h, behind, "9.8.1")
	runAgentVersion(t, h, ahead, "9.10.0")
	stopOld := runAgentVersion(t, h, tooOld, "9.7.0")

	waitFor(t, "four agents to connect", func() bool { return len(h.Agents()) == 4 })

	byName := map[string]agentJSON{}
	for _, a := range admin.agents("?all=true") {
		byName[a.Name] = a
	}
	for name, want := range map[string]string{"current": "current", "behind": "behind", "ahead": "ahead"} {
		a := byName[name]
		if a.Status != statusConnected || a.Compatibility != want || a.ProtocolVersion == 0 || a.UpdateOnly {
			t.Errorf("%s: %+v, want connected and %s", name, a, want)
		}
	}
	old := byName["old"]
	if old.Status != statusConnected || old.Version != "9.7.0" || old.Compatibility != string(update.CompatTooOld) || !old.UpdateOnly {
		t.Errorf("too old: %+v, want connected, too_old, update only", old)
	}
	if _, refused := h.Rejection(tooOld.AgentID); refused {
		t.Error("an enrolled agent that is too old was refused")
	}

	// Once updated it connects normally.
	stopOld()
	runAgentVersion(t, h, tooOld, "9.9.0")
	waitFor(t, "the updated agent to connect normally", func() bool {
		for _, a := range h.Agents() {
			if a.ID == tooOld.AgentID {
				return a.Version == "9.9.0" && !a.UpdateOnly
			}
		}
		return false
	})
}

// A session that exists only for the update carries nothing else: no
// metrics are kept, and no command but Ping and ApplyUpdate is sent.
func TestAnUpdateOnlySessionCarriesNothingButTheUpdate(t *testing.T) {
	h := startRemoteHub(t)
	old := enrollNew(t, h, "old")
	runAgentVersion(t, h, old, "9.7.0")
	waitFor(t, "the old agent", func() bool { return len(h.Agents()) == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := h.SendCommand(ctx, old.AgentID, &agentv1.Command{Kind: &agentv1.Command_SetIntervals{SetIntervals: &agentv1.SetIntervals{}}}); !errors.Is(err, ErrUpdateOnly) {
		t.Errorf("SetIntervals to an update-only agent: %v, want ErrUpdateOnly", err)
	}
	res, err := h.SendCommand(ctx, old.AgentID, &agentv1.Command{Kind: &agentv1.Command_Ping{Ping: &agentv1.Ping{}}})
	if err != nil || !res.GetSuccess() {
		t.Errorf("Ping: %v, %v", res, err)
	}
	// The agent sends no reports in this mode, and the hub would drop them.
	time.Sleep(300 * time.Millisecond)
	if samples, _ := h.agents.metricsHistory(old.AgentID); len(samples) != 0 {
		t.Errorf("%d metrics samples kept for an update-only agent", len(samples))
	}
}

// An agent that is newer than the hub is not let in by the update path: it is
// the hub that needs updating.
func TestAnAgentNewerThanTheHubIsNotAdmittedForAnUpdate(t *testing.T) {
	h := startRemoteHub(t)
	creds := enrollNew(t, h, "new")
	conn, err := creds.Dial(h.AgentAddr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	stream, err := agentv1.NewAgentServiceClient(conn).Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Hello{Hello: &agentv1.Hello{
		ProtocolVersion: protocol.Version + 5, MinProtocolVersion: protocol.Version + 3, AgentVersion: "9.7.0", AgentId: creds.AgentID,
	}}}); err != nil {
		t.Fatal(err)
	}
	msg, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if r := msg.GetReject(); r == nil || r.GetReason() != agentv1.Reject_REASON_UNSUPPORTED_PROTOCOL_VERSION {
		t.Errorf("got %v, want a refusal for the protocol", msg)
	}
}
