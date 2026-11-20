package hub

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/fleet"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"github.com/CodexCircle/Regulus/internal/pki"
	"github.com/CodexCircle/Regulus/internal/protocol"
)

// agentKeepalive is the keepalive of the agent listener (#222, #244).
type agentKeepalive struct {
	// MinTime: a client that pings more often is disconnected after two
	// warnings ("too_many_pings"). Pings with no stream open are allowed:
	// an agent pings between two sessions too.
	MinTime time.Duration
	// Time and Timeout: the hub pings a connection it has read nothing from
	// for Time and closes it when the answer does not come within Timeout.
	Time, Timeout time.Duration
}

func defaultAgentKeepalive() agentKeepalive {
	return agentKeepalive{
		MinTime: protocol.KeepaliveMinTime,
		Time:    protocol.HubKeepaliveTime,
		Timeout: protocol.HubKeepaliveTimeout,
	}
}

// agentServerOptions are the options of the gRPC server of the agent listener.
func (h *Hub) agentServerOptions(tlsConfig *tls.Config) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.Creds(credentials.NewTLS(tlsConfig)),
		grpc.StreamInterceptor(h.requireAgent),
		grpc.UnaryInterceptor(h.requireAgentUnary),
		grpc.MaxRecvMsgSize(maxAgentMessageBytes),
		grpc.MaxConcurrentStreams(maxAgentStreams),
		grpc.ConnectionTimeout(20 * time.Second), // the TLS handshake of a client that never finishes it
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             h.agentKeepalive.MinTime,
			PermitWithoutStream: true,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    h.agentKeepalive.Time,
			Timeout: h.agentKeepalive.Timeout,
		}),
	}
}

// identityKey carries the verified identity of a remote agent in the context
// of its stream. The in-process connection of the built-in agent has none.
type identityKey struct{}

func identityFrom(ctx context.Context) (pki.Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(pki.Identity)
	return id, ok
}

// peerIdentity returns the verified agent certificate of the caller. ok is
// false when there is none; err is pki.ErrNotAgent when the caller presented a
// certificate of the hub CA that is not an agent certificate (#316).
func peerIdentity(ctx context.Context) (id pki.Identity, ok bool, err error) {
	p, found := peer.FromContext(ctx)
	if !found {
		return pki.Identity{}, false, nil
	}
	info, found := p.AuthInfo.(credentials.TLSInfo)
	if !found {
		return pki.Identity{}, false, nil
	}
	return pki.AgentIdentity(info.State)
}

func peerHost(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return p.Addr.String()
	}
	return host
}

// wrappedStream replaces the context of a server stream.
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w wrappedStream) Context() context.Context { return w.ctx }

// authorizeTimeout bounds the check of an agent's certificate against the
// database when it connects.
const authorizeTimeout = 10 * time.Second

// authorizeAgent checks that the caller presents the certificate of an agent
// that was enrolled and not revoked, and returns the context with its
// identity. A certificate of another kind from the same CA is refused before
// its id is looked at (#316).
func (h *Hub) authorizeAgent(ctx context.Context) (context.Context, error) {
	id, ok, err := peerIdentity(ctx)
	if err != nil {
		h.log.Warn("refusing a certificate that is not an agent's", "from", peerHost(ctx), "err", err)
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "a client certificate is required")
	}
	// The one database read of a handshake; bounded, so a database that does
	// not answer refuses the agent (it retries) instead of holding it.
	actx, cancel := context.WithTimeout(ctx, authorizeTimeout)
	err = h.fleet.Authorize(actx, id.ID, id.Serial)
	cancel()
	if err != nil {
		switch {
		case errors.Is(err, fleet.ErrNotFound), errors.Is(err, fleet.ErrRevoked):
			h.log.Warn("refusing agent", "agent", id.ID, "err", err)
			return nil, status.Error(codes.PermissionDenied, "this agent is not enrolled or was revoked")
		default:
			h.log.Error("checking agent", "agent", id.ID, "err", err)
			return nil, status.Error(codes.Internal, "cannot check the agent")
		}
	}
	// Agents enrolled before the hub kept the end of their certificate get it
	// filled in (#245), by the presence writer, not here: a failure only
	// leaves the date unknown, and must not hold the connection.
	h.seen.put("cert/"+id.ID, func(ctx context.Context) error {
		return h.fleet.CertificateSeen(ctx, id.ID, id.Serial, id.NotAfter)
	})
	return context.WithValue(ctx, identityKey{}, id), nil
}

// requireAgent is the stream interceptor of the network listener: a stream
// needs the certificate of an agent that was enrolled and not revoked.
func (h *Hub) requireAgent(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	ctx, err := h.authorizeAgent(ss.Context())
	if err != nil {
		return err
	}
	return handler(srv, wrappedStream{ss, ctx})
}

// openUnaryMethods are the unary calls the network listener serves without a
// client certificate. Everything else is denied by default (#244): a unary
// call added to AgentService later needs an enrolled agent without anyone
// having to remember it.
var openUnaryMethods = map[string]bool{
	agentv1.EnrollmentService_Enroll_FullMethodName: true,
}

// requireAgentUnary is the unary interceptor of the network listener: only
// the calls in openUnaryMethods work without the certificate of an enrolled
// agent.
func (h *Hub) requireAgentUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if openUnaryMethods[info.FullMethod] {
		return handler(ctx, req)
	}
	ctx, err := h.authorizeAgent(ctx)
	if err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

// enrollServer implements the one call that works without a certificate.
type enrollServer struct {
	agentv1.UnimplementedEnrollmentServiceServer
	hub *Hub
}

func (e enrollServer) Enroll(ctx context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	remote := peerHost(ctx)
	ctx = auth.WithClient(ctx, auth.Client{Remote: remote})
	out, err := e.hub.fleet.Enroll(ctx, remote, req.GetToken(), req.GetCsrDer(), req.GetSystem().GetHostname())
	var reqErr *fleet.RequestError
	switch {
	case err == nil:
		e.hub.log.Info("agent enrolled", "agent", out.AgentID, "host", req.GetSystem().GetHostname(), "from", remote)
		return &agentv1.EnrollResponse{
			AgentId:          out.AgentID,
			CertificateDer:   out.CertificateDER,
			CaCertificateDer: out.CACertificateDER,
		}, nil
	case errors.Is(err, fleet.ErrInvalidToken):
		return nil, status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, fleet.ErrRateLimited):
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	case errors.As(err, &reqErr):
		return nil, status.Error(codes.InvalidArgument, err.Error())
	default:
		e.hub.log.Error("enrolling an agent", "err", err)
		return nil, status.Error(codes.Internal, "cannot enroll the agent")
	}
}
