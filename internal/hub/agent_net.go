package hub

import (
	"context"
	"errors"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/fleet"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"github.com/CodexCircle/Regulus/internal/pki"
)

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

// requireAgent is the stream interceptor of the network listener: a stream
// needs the certificate of an agent that was enrolled and not revoked. A
// certificate of another kind from the same CA is refused before its id is
// looked at.
func (h *Hub) requireAgent(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	ctx := ss.Context()
	id, ok, err := peerIdentity(ctx)
	if err != nil {
		h.log.Warn("refusing a certificate that is not an agent's", "from", peerHost(ctx), "err", err)
		return status.Error(codes.PermissionDenied, err.Error())
	}
	if !ok {
		return status.Error(codes.Unauthenticated, "a client certificate is required")
	}
	if err := h.fleet.Authorize(ctx, id.ID, id.Serial); err != nil {
		switch {
		case errors.Is(err, fleet.ErrNotFound), errors.Is(err, fleet.ErrRevoked):
			h.log.Warn("refusing agent", "agent", id.ID, "err", err)
			return status.Error(codes.PermissionDenied, "this agent is not enrolled or was revoked")
		default:
			h.log.Error("checking agent", "agent", id.ID, "err", err)
			return status.Error(codes.Internal, "cannot check the agent")
		}
	}
	// Agents enrolled before the hub kept the end of their certificate get it
	// filled in here (#245); a failure only leaves the date unknown.
	if err := h.fleet.CertificateSeen(ctx, id.ID, id.Serial, id.NotAfter); err != nil {
		h.log.Warn("recording the end of an agent certificate", "agent", id.ID, "err", err)
	}
	return handler(srv, wrappedStream{ss, context.WithValue(ctx, identityKey{}, id)})
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
