package hub

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"google.golang.org/grpc"

	"github.com/CodexCircle/Regulus/internal/config"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"github.com/CodexCircle/Regulus/internal/pki"
)

// fakeAgentStream is the server side of an agent stream whose context carries
// the identity a verified client certificate would give.
type fakeAgentStream struct {
	grpc.ServerStream
	ctx context.Context
	in  chan *agentv1.AgentMessage
}

func (f *fakeAgentStream) Context() context.Context { return f.ctx }
func (f *fakeAgentStream) Send(*agentv1.HubMessage) error {
	return nil
}
func (f *fakeAgentStream) Recv() (*agentv1.AgentMessage, error) {
	select {
	case m := <-f.in:
		return m, nil
	case <-f.ctx.Done():
		return nil, io.EOF
	}
}

// TestHubCountsAgentsOnALegacyCertificate is #322: a connected agent whose
// certificate has no kind is counted in GET /api/certificates and logged, and
// is no longer counted once it leaves.
func TestHubCountsAgentsOnALegacyCertificate(t *testing.T) {
	var logs syncBuffer
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	useTestDatabase(t, &cfg)
	cfg.Updates.Check = false
	h, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(&logs, nil)), fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	connect := func(id string, legacy bool) (cancel func(), done <-chan error) {
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), identityKey{}, pki.Identity{Kind: pki.KindAgent, ID: id, Legacy: legacy}))
		stream := &fakeAgentStream{ctx: ctx, in: make(chan *agentv1.AgentMessage, 1)}
		stream.in <- helloMsg(1, id)
		res := make(chan error, 1)
		go func() { res <- h.agents.Connect(stream) }()
		return cancel, res
	}
	stopOld, doneOld := connect("agent-old", true)
	stopNew, doneNew := connect("agent-new", false)
	t.Cleanup(func() { stopOld(); stopNew() })

	waitFor(t, "both agents to connect", func() bool { return len(h.Agents()) == 2 })
	c, err := h.Certificates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.LegacyAgents != 1 {
		t.Errorf("legacy agents = %d, want 1 (only agent-old)", c.LegacyAgents)
	}
	if j := toCertificatesJSON(c, time.Now()); j.LegacyAgents != 1 {
		t.Errorf("json legacy_agents = %d, want 1", j.LegacyAgents)
	}
	var legacyLines []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "legacy certificate") {
			legacyLines = append(legacyLines, line)
		}
	}
	if len(legacyLines) != 1 || !strings.Contains(legacyLines[0], "agent=agent-old") {
		t.Errorf("legacy log lines = %q, want one, for agent-old", legacyLines)
	}

	stopOld()
	t.Logf("agent-old stream ended: %v", <-doneOld)
	c, _ = h.Certificates(context.Background())
	if c.LegacyAgents != 0 {
		t.Errorf("legacy agents after it left = %d, want 0", c.LegacyAgents)
	}
	stopNew()
	t.Logf("agent-new stream ended: %v", <-doneNew)
}
