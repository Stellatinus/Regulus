package hub

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/CodexCircle/Regulus/internal/agent"
	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/pki"
)

// TestUnaryCallsNeedAnAgentByDefault is #244: a unary call registered on the
// agent listener is denied to a caller without the certificate of an enrolled
// agent, unless it is listed in openUnaryMethods (only Enroll is). The health
// service stands in for a unary call added to AgentService one day.
func TestUnaryCallsNeedAnAgentByDefault(t *testing.T) {
	cfg := config.Default()
	cfg.BuiltinAgent = false
	cfg.AgentListen = "127.0.0.1:0"
	h := startHubWithConfig(t, cfg, func(h *Hub) {
		h.agentServerTune = func(s *grpc.Server) { healthpb.RegisterHealthServer(s, health.NewServer()) }
	})
	waitFor(t, "the agent listener", func() bool { return h.AgentAddr() != "" })
	creds := enrollNew(t, h, "")
	fp := pki.Fingerprint(creds.CADER)

	check := func(cfg *tls.Config) error {
		conn, err := grpc.NewClient(h.AgentAddr(), grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
		return err
	}

	anon, _ := pki.PinnedClientTLS(fp, nil)
	if err := check(anon); status.Code(err) != codes.Unauthenticated {
		t.Errorf("without a certificate: err = %v, want Unauthenticated", err)
	}
	good, _ := pki.PinnedClientTLS(fp, &tls.Certificate{Certificate: [][]byte{creds.CertDER}, PrivateKey: creds.Key})
	if err := check(good); err != nil {
		t.Errorf("an enrolled agent: err = %v, want the call to work", err)
	}
	if err := h.RevokeAgent(context.Background(), "admin", creds.AgentID); err != nil {
		t.Fatal(err)
	}
	if err := check(good); status.Code(err) != codes.PermissionDenied {
		t.Errorf("a revoked agent: err = %v, want PermissionDenied", err)
	}

	// Enroll is the one call open to everyone (the token is its check).
	grant, err := h.CreateEnrollment(context.Background(), "admin", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Enroll(context.Background(), h.AgentAddr(), grant.CAFingerprint, grant.Token, "9.9.9", t.TempDir()); err != nil {
		t.Errorf("Enroll without a certificate: %v", err)
	}
}
