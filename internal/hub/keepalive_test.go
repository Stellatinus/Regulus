package hub

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"github.com/CodexCircle/Regulus/internal/agent"
	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/pki"
	"github.com/CodexCircle/Regulus/internal/protocol"
)

// blackHole is a TCP proxy whose connections can be made to stop forwarding
// without closing: what an agent sees when the hub's host loses power or a
// NAT forgets the flow. The kernel still acknowledges what the agent sends,
// so only the keepalive can tell.
type blackHole struct {
	ln      net.Listener
	target  string
	mu      sync.Mutex
	dead    []*atomic.Bool
	accepts atomic.Int32
}

func newBlackHole(t *testing.T, target string) *blackHole {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &blackHole{ln: ln, target: target}
	t.Cleanup(func() { _ = ln.Close() })
	go p.serve(t)
	return p
}

func (p *blackHole) addr() string { return p.ln.Addr().String() }

func (p *blackHole) serve(t *testing.T) {
	for {
		in, err := p.ln.Accept()
		if err != nil {
			return
		}
		out, err := net.Dial("tcp", p.target)
		if err != nil {
			_ = in.Close()
			continue
		}
		t.Cleanup(func() { _ = in.Close(); _ = out.Close() })
		dead := new(atomic.Bool)
		p.mu.Lock()
		p.dead = append(p.dead, dead)
		p.mu.Unlock()
		p.accepts.Add(1)
		go forward(out, in, dead)
		go forward(in, out, dead)
	}
}

// forward copies src to dst until dead is set; after that it keeps reading
// and throws everything away.
func forward(dst io.Writer, src io.Reader, dead *atomic.Bool) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 && !dead.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// swallowAll black-holes every connection open so far; new ones work.
func (p *blackHole) swallowAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.dead {
		d.Store(true)
	}
}

func withKeepalive(k agentKeepalive) Option {
	return func(h *Hub) { h.agentKeepalive = k }
}

// reconnectAfterBlackHole connects an agent through a black-hole proxy, cuts
// the flow and returns how long the agent took to be connected again.
func reconnectAfterBlackHole(t *testing.T, client keepalive.ClientParameters, hubPolicy agentKeepalive, limit time.Duration) time.Duration {
	t.Helper()
	cfg := config.Default()
	cfg.BuiltinAgent = false
	cfg.AgentListen = "127.0.0.1:0"
	h := startHubWithConfig(t, cfg, withKeepalive(hubPolicy))
	waitFor(t, "the agent listener", func() bool { return h.AgentAddr() != "" })
	creds := enrollNew(t, h, "")
	proxy := newBlackHole(t, h.AgentAddr())

	var opts []grpc.DialOption
	if client != (keepalive.ClientParameters{}) {
		opts = append(opts, grpc.WithKeepaliveParams(client))
	}
	conn, err := creds.Dial(proxy.addr(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = agent.Run(ctx, conn, agent.Options{
			Version: "9.9.9", ID: creds.AgentID, Log: quiet(),
			MinBackoff: 20 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
		})
	}()
	t.Cleanup(func() { cancel(); <-done; _ = conn.Close() })

	connectedAt := func() time.Time {
		for _, a := range h.Agents() {
			if a.ID == creds.AgentID {
				return a.ConnectedAt
			}
		}
		return time.Time{}
	}
	waitFor(t, "the agent to connect", func() bool { return !connectedAt().IsZero() })
	first := connectedAt()

	proxy.swallowAll()
	cut := time.Now()
	deadline := cut.Add(limit)
	for {
		if at := connectedAt(); at.After(first) && proxy.accepts.Load() >= 2 {
			return time.Since(cut)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent was not connected again %v after the hub went silent", limit)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestAgentNoticesADeadHubQuickly is #222: the connection stops forwarding
// without closing, and the agent reconnects as soon as its keepalive ping is
// not answered. The keepalive is shortened to gRPC's smallest ping interval
// (10 s) with a 1 s timeout so the test stays short; the production numbers
// are checked by TestAgentNoticesADeadHubWithTheProductionKeepalive.
func TestAgentNoticesADeadHubQuickly(t *testing.T) {
	took := reconnectAfterBlackHole(t,
		keepalive.ClientParameters{Time: 10 * time.Second, Timeout: time.Second, PermitWithoutStream: true},
		agentKeepalive{MinTime: 5 * time.Second, Time: protocol.HubKeepaliveTime, Timeout: protocol.HubKeepaliveTimeout},
		20*time.Second)
	t.Logf("reconnected %v after the black hole", took)
}

// TestAgentNoticesADeadHubWithTheProductionKeepalive runs the same with the
// numbers agents use (30 s + 10 s): the agent is back within about 45 s. It
// takes that long, so it runs only with REGULUS_SLOW_TESTS=1.
func TestAgentNoticesADeadHubWithTheProductionKeepalive(t *testing.T) {
	if os.Getenv("REGULUS_SLOW_TESTS") == "" {
		t.Skip("takes about 45 s; set REGULUS_SLOW_TESTS=1")
	}
	took := reconnectAfterBlackHole(t, keepalive.ClientParameters{}, defaultAgentKeepalive(), 50*time.Second)
	t.Logf("reconnected %v after the black hole", took)
	if took > 45*time.Second {
		t.Errorf("reconnected after %v, want about 45 s at most", took)
	}
}

// pinger speaks raw HTTP/2 to the agent listener, which gRPC's own client
// cannot do: it never pings more often than every 10 s.
type pinger struct {
	conn   *tls.Conn
	fr     *http2.Framer
	acks   chan struct{}
	goAway chan http2.ErrCode
	write  func(func() error) error // writes one frame at a time
}

func newPinger(t *testing.T, h *Hub) *pinger {
	t.Helper()
	creds := enrollNew(t, h, "")
	cfg, err := pki.PinnedClientTLS(pki.Fingerprint(creds.CADER), &tls.Certificate{Certificate: [][]byte{creds.CertDER}, PrivateKey: creds.Key})
	if err != nil {
		t.Fatal(err)
	}
	cfg.NextProtos = []string{"h2"}
	conn, err := tls.Dial("tcp", h.AgentAddr(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := io.WriteString(conn, http2.ClientPreface); err != nil {
		t.Fatal(err)
	}
	p := &pinger{conn: conn, fr: http2.NewFramer(conn, conn), acks: make(chan struct{}, 64), goAway: make(chan http2.ErrCode, 1)}
	if err := p.fr.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	var writeMu sync.Mutex
	go func() {
		for {
			f, err := p.fr.ReadFrame()
			if err != nil {
				return
			}
			switch f := f.(type) {
			case *http2.SettingsFrame:
				if !f.IsAck() {
					writeMu.Lock()
					_ = p.fr.WriteSettingsAck()
					writeMu.Unlock()
				}
			case *http2.PingFrame:
				if f.IsAck() {
					p.acks <- struct{}{}
				}
			case *http2.GoAwayFrame:
				p.goAway <- f.ErrCode
				return
			}
		}
	}()
	p.write = func(fn func() error) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return fn()
	}
	return p
}

func (p *pinger) ping(t *testing.T) {
	t.Helper()
	if err := p.write(func() error { return p.fr.WritePing(false, [8]byte{1}) }); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

// TestKeepalivePolicyRefusesFastPingers is the hub's half of #244: a client
// that pings more often than the policy allows is disconnected with gRPC's
// "too_many_pings", one that keeps to it is not.
func TestKeepalivePolicyRefusesFastPingers(t *testing.T) {
	cfg := config.Default()
	cfg.BuiltinAgent = false
	cfg.AgentListen = "127.0.0.1:0"
	policy := agentKeepalive{MinTime: 150 * time.Millisecond, Time: protocol.HubKeepaliveTime, Timeout: protocol.HubKeepaliveTimeout}
	h := startHubWithConfig(t, cfg, withKeepalive(policy))
	waitFor(t, "the agent listener", func() bool { return h.AgentAddr() != "" })

	t.Run("within the policy", func(t *testing.T) {
		p := newPinger(t, h)
		for range 6 {
			p.ping(t)
			select {
			case <-p.acks:
			case code := <-p.goAway:
				t.Fatalf("a client within the policy got GOAWAY %v", code)
			case <-time.After(5 * time.Second):
				t.Fatal("no answer to a ping")
			}
			time.Sleep(2 * policy.MinTime)
		}
		select {
		case code := <-p.goAway:
			t.Fatalf("a client within the policy got GOAWAY %v", code)
		case <-time.After(3 * policy.MinTime):
		}
	})

	t.Run("too fast", func(t *testing.T) {
		p := newPinger(t, h)
		for range 6 {
			p.ping(t)
		}
		select {
		case code := <-p.goAway:
			if code != http2.ErrCodeEnhanceYourCalm {
				t.Fatalf("GOAWAY %v, want ENHANCE_YOUR_CALM", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a client pinging six times in a row was not disconnected")
		}
	})
}
