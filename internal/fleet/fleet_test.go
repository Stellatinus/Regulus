package fleet

import (
	"context"
	"crypto/x509"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/pki"
	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

type audit struct {
	mu     sync.Mutex
	events []string
}

func (a *audit) Record(_ context.Context, actor, event, target, detail string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, event+" "+actor+" "+target+" "+detail)
}

func (a *audit) text() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Join(a.events, "\n")
}

func newService(t *testing.T) (*Service, *audit) {
	t.Helper()
	dir := t.TempDir()
	db := storagetest.Open(t, dir)
	t.Cleanup(func() { _ = db.Close() })
	ca, err := pki.LoadOrCreate(dir+"/pki", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a := &audit{}
	return New(db, ca, a), a
}

func csr(t *testing.T) []byte {
	t.Helper()
	_, der, err := pki.NewAgentKey()
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestEnrollmentUsesTheTokenUp(t *testing.T) {
	s, a := newService(t)
	ctx := context.Background()
	token, expires, err := s.CreateToken(ctx, "admin", " Web 1 ", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := expires.Sub(s.now()); got < TokenTTL-time.Minute || got > TokenTTL+time.Minute {
		t.Errorf("token valid for %v, want about %v", got, TokenTTL)
	}

	got, err := s.Enroll(ctx, "10.0.0.5", token, csr(t), "web1.example")
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	cert, err := x509.ParseCertificate(got.CertificateDER)
	if err != nil || cert.Subject.CommonName != got.AgentID || !strings.HasPrefix(got.AgentID, "agent-") {
		t.Fatalf("certificate = %v, %v; agent id = %q", cert, err, got.AgentID)
	}
	if string(got.CACertificateDER) != string(s.CA().CertDER()) {
		t.Error("the response does not carry the CA certificate")
	}

	list, _ := s.Agents(ctx)
	if len(list) != 1 || list[0].ID != got.AgentID || list[0].Name != "Web 1" || list[0].Hostname != "web1.example" || list[0].Revoked {
		t.Fatalf("agents = %+v", list)
	}

	// A token works once.
	if _, err := s.Enroll(ctx, "10.0.0.5", token, csr(t), "again"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("second use: err = %v, want ErrInvalidToken", err)
	}
	if !strings.Contains(a.text(), EventTokenCreated+" admin Web 1") || !strings.Contains(a.text(), EventAgentEnrolled+"  "+got.AgentID) {
		t.Errorf("audit =\n%s", a.text())
	}
	if strings.Contains(a.text(), token) {
		t.Error("the audit log holds the token")
	}
}

func TestTokensExpireAndAreNotStoredInTheClear(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	token, _, _ := s.CreateToken(ctx, "admin", "", 0)
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM enrollment_tokens WHERE token_hash = ?`, token).Scan(&n); err != nil || n != 0 {
		t.Errorf("the raw token is stored: n = %d, err = %v", n, err)
	}
	now := s.now()
	s.now = func() time.Time { return now.Add(TokenTTL + time.Second) }
	if _, err := s.Enroll(ctx, "r", token, csr(t), ""); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expired token: err = %v, want ErrInvalidToken", err)
	}
}

func TestBadNamesAreRefused(t *testing.T) {
	s, _ := newService(t)
	for _, name := range []string{strings.Repeat("x", 65), "a\nb"} {
		var ne *NameError
		if _, _, err := s.CreateToken(context.Background(), "admin", name, 0); !errors.As(err, &ne) {
			t.Errorf("name %q: err = %v, want a NameError", name, err)
		}
	}
}

func TestABadRequestEnrollsNobodyAndKeepsTheToken(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	token, _, _ := s.CreateToken(ctx, "admin", "", 0)
	var re *RequestError
	if _, err := s.Enroll(ctx, "r", token, []byte("not a csr"), ""); !errors.As(err, &re) {
		t.Fatalf("err = %v, want a RequestError", err)
	}
	if list, _ := s.Agents(ctx); len(list) != 0 {
		t.Errorf("agents = %+v", list)
	}
	if _, err := s.Enroll(ctx, "r", token, csr(t), ""); err != nil {
		t.Errorf("a malformed request must not burn the token: %v", err)
	}
	if _, err := s.Enroll(ctx, "r", token, csr(t), ""); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("reuse after a good enrollment: err = %v", err)
	}
}

func TestAFailedDatabaseWriteKeepsTheToken(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	token, _, _ := s.CreateToken(ctx, "admin", "web", 0)
	// Make the insert of the agent fail after the token was checked.
	if _, err := s.db.Exec(`ALTER TABLE agents RENAME TO agents_away`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "r", token, csr(t), ""); err == nil || errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want a database error", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE agents_away RENAME TO agents`); err != nil {
		t.Fatal(err)
	}
	if pending, _ := s.PendingTokens(ctx); len(pending) != 1 {
		t.Fatalf("the token was lost with the failed enrollment: %+v", pending)
	}
	if _, err := s.Enroll(ctx, "r", token, csr(t), ""); err != nil {
		t.Fatalf("the token must still work: %v", err)
	}
}

func TestATokenCanBeUsedOnlyOnceEvenInParallel(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	token, _, _ := s.CreateToken(ctx, "admin", "", 0)
	const tries = 8
	results := make(chan error, tries)
	for range tries {
		go func() {
			_, err := s.Enroll(ctx, "r", token, csr(t), "")
			results <- err
		}()
	}
	ok := 0
	for range tries {
		if err := <-results; err == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d enrollments succeeded with one token, want 1", ok)
	}
	if list, _ := s.Agents(ctx); len(list) != 1 {
		t.Fatalf("agents = %d, want 1", len(list))
	}
}

func TestPendingTokensAreListedWithoutTheSecretAndCanBeRevoked(t *testing.T) {
	s, a := newService(t)
	ctx := context.Background()
	token, _, _ := s.CreateToken(ctx, "admin", "web", 0)
	other, _, _ := s.CreateToken(ctx, "ana", "db", 2*time.Hour)

	pending, err := s.PendingTokens(ctx)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	first, second := pending[0], pending[1]
	if first.Name != "web" || first.CreatedBy != "admin" || second.Name != "db" || second.CreatedBy != "ana" {
		t.Errorf("pending = %+v", pending)
	}
	if got := second.ExpiresAt.Sub(second.CreatedAt); got != 2*time.Hour {
		t.Errorf("ttl = %v, want 2h", got)
	}
	for _, p := range pending {
		if strings.Contains(token, p.ID) || strings.Contains(other, p.ID) {
			t.Errorf("the id %q must not be part of a token", p.ID)
		}
	}

	if err := s.RevokeToken(ctx, "admin", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "r", token, csr(t), ""); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("a revoked token must not enroll: %v", err)
	}
	if err := s.RevokeToken(ctx, "admin", first.ID); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("revoking twice: %v", err)
	}
	for _, bad := range []string{"", "x", strings.Repeat("g", 16), strings.Repeat("0", 17)} {
		if err := s.RevokeToken(ctx, "admin", bad); !errors.Is(err, ErrTokenNotFound) {
			t.Errorf("id %q: %v", bad, err)
		}
	}
	if _, err := s.Enroll(ctx, "r", other, csr(t), ""); err != nil {
		t.Errorf("the other token must still work: %v", err)
	}
	if !strings.Contains(a.text(), EventTokenRevoked) {
		t.Error("revoking a token is not audited")
	}
	if strings.Contains(a.text(), token) {
		t.Error("the audit log holds a token")
	}
}

func TestTokenLifetime(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	_, expires, err := s.CreateToken(ctx, "admin", "", 15*time.Minute)
	if got := expires.Sub(s.now()); err != nil || got > 15*time.Minute || got < 14*time.Minute {
		t.Fatalf("expires = %v, %v", expires, err)
	}
	for _, ttl := range []time.Duration{time.Second, -time.Hour, MaxTokenTTL + time.Second} {
		var te *TTLError
		if _, _, err := s.CreateToken(ctx, "admin", "", ttl); !errors.As(err, &te) {
			t.Errorf("ttl %v: err = %v, want a TTLError", ttl, err)
		}
	}
}

func TestGuessingTokensIsRateLimited(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	good, _, _ := s.CreateToken(ctx, "admin", "", 0)
	for range maxFailures {
		if _, err := s.Enroll(ctx, "9.9.9.9", "guess", csr(t), ""); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("guess: err = %v", err)
		}
	}
	if _, err := s.Enroll(ctx, "9.9.9.9", good, csr(t), ""); !errors.Is(err, ErrRateLimited) {
		t.Errorf("after the guesses: err = %v, want ErrRateLimited", err)
	}
	if _, err := s.Enroll(ctx, "8.8.8.8", good, csr(t), ""); err != nil {
		t.Errorf("another address: %v", err)
	}
}

func TestAuthorizeAndRevoke(t *testing.T) {
	s, a := newService(t)
	ctx := context.Background()
	token, _, _ := s.CreateToken(ctx, "admin", "", 0)
	got, _ := s.Enroll(ctx, "r", token, csr(t), "h")
	cert, _ := x509.ParseCertificate(got.CertificateDER)
	serial := cert.SerialNumber.Text(16)

	if err := s.Authorize(ctx, got.AgentID, serial); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if err := s.Authorize(ctx, got.AgentID, "deadbeef"); !errors.Is(err, ErrRevoked) {
		t.Errorf("another certificate: err = %v, want ErrRevoked", err)
	}
	if err := s.Authorize(ctx, "agent-unknown", serial); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown agent: err = %v, want ErrNotFound", err)
	}

	if err := s.Revoke(ctx, "admin", got.AgentID); err != nil {
		t.Fatal(err)
	}
	if err := s.Authorize(ctx, got.AgentID, serial); !errors.Is(err, ErrRevoked) {
		t.Errorf("revoked: err = %v, want ErrRevoked", err)
	}
	if err := s.Revoke(ctx, "admin", got.AgentID); err != nil { // again: no change, no error
		t.Errorf("revoking twice: %v", err)
	}
	if err := s.Revoke(ctx, "admin", "agent-unknown"); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoking an unknown agent: err = %v, want ErrNotFound", err)
	}
	if n := strings.Count(a.text(), EventAgentRevoked); n != 1 {
		t.Errorf("revocation audited %d times, want 1:\n%s", n, a.text())
	}
	if list, _ := s.Agents(ctx); !list[0].Revoked || list[0].RevokedAt.IsZero() {
		t.Errorf("agents = %+v", list)
	}
}
