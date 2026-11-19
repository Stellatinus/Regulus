package protocol_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/CodexCircle/Regulus/internal/agent"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"github.com/CodexCircle/Regulus/internal/inproc"
	"github.com/CodexCircle/Regulus/internal/protocol"
)

// scriptedHub answers Hello with a Welcome chosen by the test, then sends the
// commands it is given, and reports everything the real agent sends back.
type scriptedHub struct {
	agentv1.UnimplementedAgentServiceServer
	welcome  *agentv1.Welcome
	commands []*agentv1.Command
	results  chan *agentv1.CommandResult
	metrics  chan struct{}
	streams  chan struct{}
}

func (h *scriptedHub) Connect(stream grpc.BidiStreamingServer[agentv1.AgentMessage, agentv1.HubMessage]) error {
	h.streams <- struct{}{}
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&agentv1.HubMessage{Payload: &agentv1.HubMessage_Welcome{Welcome: h.welcome}}); err != nil {
		return err
	}
	for _, c := range h.commands {
		if err := stream.Send(&agentv1.HubMessage{Payload: &agentv1.HubMessage_Command{Command: c}}); err != nil {
			return err
		}
	}
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch p := msg.GetPayload().(type) {
		case *agentv1.AgentMessage_CommandResult:
			h.results <- p.CommandResult
		case *agentv1.AgentMessage_Metrics:
			select {
			case h.metrics <- struct{}{}:
			default:
			}
		}
	}
}

type oneReport struct{}

func (oneReport) Collect(context.Context) (*agentv1.MetricsReport, error) {
	return &agentv1.MetricsReport{UptimeSeconds: 1}, nil
}

// runRealAgent runs the agent of this build against the scripted hub.
func runRealAgent(t *testing.T, h *scriptedHub) {
	t.Helper()
	tr := inproc.New()
	srv := grpc.NewServer()
	agentv1.RegisterAgentServiceServer(srv, h)
	go func() { _ = srv.Serve(tr.Listener()) }()
	t.Cleanup(srv.Stop)
	conn, err := tr.Dial()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = agent.Run(ctx, conn, agent.Options{
			Version: "0.2.0", Collector: oneReport{}, Log: slog.New(slog.DiscardHandler),
			MinBackoff: 10 * time.Millisecond, MaxBackoff: 20 * time.Millisecond,
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
}

func newScriptedHub(w *agentv1.Welcome, cmds ...*agentv1.Command) *scriptedHub {
	if w.HeartbeatInterval == nil {
		w.HeartbeatInterval = durationpb.New(time.Second)
		w.MetricsInterval = durationpb.New(time.Second)
	}
	return &scriptedHub{welcome: w, commands: cmds, results: make(chan *agentv1.CommandResult, 8), metrics: make(chan struct{}, 1), streams: make(chan struct{}, 16)}
}

// A newer hub may send a command this agent does not know (a oneof case
// added later). The agent decodes it as a command with no kind and answers
// "unsupported command": it neither crashes nor drops the connection.
func TestAnAgentAnswersACommandItDoesNotKnow(t *testing.T) {
	unknown := &agentv1.Command{Id: "cmd-new"}
	// Field 99 of Command, a message: what a future command kind looks like
	// on the wire to this build.
	raw := protowire.AppendTag(nil, 99, protowire.BytesType)
	raw = protowire.AppendBytes(raw, []byte{0x0a, 0x01, 'x'})
	unknown.ProtoReflect().SetUnknown(raw)
	ping := &agentv1.Command{Id: "cmd-ping", Kind: &agentv1.Command_Ping{Ping: &agentv1.Ping{}}}

	h := newScriptedHub(&agentv1.Welcome{ProtocolVersion: protocol.Version, AgentId: "a"}, unknown, ping)
	runRealAgent(t, h)
	got := map[string]*agentv1.CommandResult{}
	for len(got) < 2 {
		select {
		case r := <-h.results:
			got[r.GetCommandId()] = r
		case <-time.After(5 * time.Second):
			t.Fatalf("results so far: %v", got)
		}
	}
	if r := got["cmd-new"]; r.GetSuccess() || r.GetError() != "unsupported command" {
		t.Errorf("unknown command: %v", r)
	}
	if r := got["cmd-ping"]; !r.GetSuccess() {
		t.Errorf("the ping after it: %v", r)
	}
}

// The hub must choose a version the agent speaks. One that chooses another is
// not followed: the agent drops the stream and tries again later.
func TestAnAgentRefusesAProtocolVersionItDoesNotSpeak(t *testing.T) {
	for _, v := range []uint32{0, protocol.Version + 1} {
		h := newScriptedHub(&agentv1.Welcome{ProtocolVersion: v, AgentId: "a"}, &agentv1.Command{Id: "c", Kind: &agentv1.Command_Ping{Ping: &agentv1.Ping{}}})
		runRealAgent(t, h)
		// It reconnects (a second stream) without having answered anything.
		for i := 0; i < 2; i++ {
			select {
			case <-h.streams:
			case <-time.After(5 * time.Second):
				t.Fatalf("version %d: the agent did not reconnect", v)
			}
		}
		select {
		case r := <-h.results:
			t.Errorf("version %d: the agent answered %v", v, r)
		default:
		}
	}
}

// In an update-only session the agent sends no metrics, and still answers the
// commands of the update contract.
func TestAnAgentInAnUpdateOnlySessionSendsNoMetrics(t *testing.T) {
	h := newScriptedHub(&agentv1.Welcome{ProtocolVersion: protocol.UpdateContract, AgentId: "a", UpdateOnly: true, Notice: "too old"},
		&agentv1.Command{Id: "p", Kind: &agentv1.Command_Ping{Ping: &agentv1.Ping{}}})
	runRealAgent(t, h)
	select {
	case r := <-h.results:
		if !r.GetSuccess() {
			t.Errorf("ping: %v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no answer to the ping")
	}
	select {
	case <-h.metrics:
		t.Error("the agent sent metrics in an update-only session")
	case <-time.After(1500 * time.Millisecond):
	}
}
