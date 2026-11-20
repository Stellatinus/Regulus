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

// peerIdentity returns the verified client certificate of the caller.
func peerIdentity(ctx context.Context) (pki.Identity, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return pki.Identity{}, false
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return pki.Identity{}, false
	}
	return pki.ClientIdentity(info.State)
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

// authorizeAgent checks that the caller presents the certificate of an agent
// that was enrolled and not revoked, and returns the context with its
// identity.
func (h *Hub) authorizeAgent(ctx context.Context) (context.Context, error) {
	id, ok := peerIdentity(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "a client certificate is required")
	}
	if err := h.fleet.Authorize(ctx, id.AgentID, id.Serial); err != nil {
		switch {
		case errors.Is(err, fleet.ErrNotFound), errors.Is(err, fleet.ErrRevoked):
			h.log.Warn("refusing agent", "agent", id.AgentID, "err", err)
			return nil, status.Error(codes.PermissionDenied, "this agent is not enrolled or was revoked")
		default:
			h.log.Error("checking agent", "agent", id.AgentID, "err", err)
			return nil, status.Error(codes.Internal, "cannot check the agent")
		}
	}
	// Agents enrolled before the hub kept the end of their certificate get it
	// filled in here (#245); a failure only leaves the date unknown.
	if err := h.fleet.CertificateSeen(ctx, id.AgentID, id.Serial, id.NotAfter); err != nil {
		h.log.Warn("recording the end of an agent certificate", "agent", id.AgentID, "err", err)
	}
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
