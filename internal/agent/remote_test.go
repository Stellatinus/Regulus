package agent

import (
	"crypto/tls"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/pki"
)

func TestLoadCredentialsWithoutEnrollment(t *testing.T) {
	if _, err := LoadCredentials(t.TempDir()); !errors.Is(err, ErrNotEnrolled) {
		t.Errorf("err = %v, want ErrNotEnrolled", err)
	}
}

func TestCredentialsRoundTripAndAreKeptPrivate(t *testing.T) {
	ca, err := pki.LoadOrCreate(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	key, csr, _ := pki.NewAgentKey()
	der, _, _ := ca.SignClient(csr, "agent-abc", time.Now())
	dir := filepath.Join(t.TempDir(), "agent")

	c := &Credentials{AgentID: "agent-abc", Key: key, CertDER: der, CADER: ca.CertDER()}
	if err := c.save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentID != "agent-abc" || !got.Key.Equal(key) || string(got.CADER) != string(ca.CertDER()) {
		t.Errorf("loaded %+v", got)
	}
	for file, want := range map[string]os.FileMode{keyFile: 0o600} {
		info, err := os.Stat(filepath.Join(dir, file))
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: mode %v, %v; want %v", file, info.Mode().Perm(), err, want)
		}
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %v, want 0700", info.Mode().Perm())
	}
}

func TestDamagedCredentialsAreReported(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, keyFile), []byte("junk"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, certFile), []byte("junk"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, caFile), []byte("junk"), 0o600)
	if _, err := LoadCredentials(dir); err == nil || errors.Is(err, ErrNotEnrolled) {
		t.Errorf("err = %v, want a damage error", err)
	}
}

// tlsHubAt starts a TLS listener with a certificate of ca and returns its address.
func tlsHubAt(t *testing.T, ca *pki.CA) string {
	t.Helper()
	cert, err := ca.ServerCertificate(time.Now(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", ca.ServerTLS(cert))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = c.Read(make([]byte, 1)); _ = c.Close() }()
		}
	}()
	return ln.Addr().String()
}

// CheckHub accepts an address whose certificate is signed by the pinned CA,
// whatever the address, and refuses another CA and a closed port.
func TestCheckHubPinsTheCA(t *testing.T) {
	ca, err := pki.LoadOrCreate(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	other, err := pki.LoadOrCreate(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	key, csr, _ := pki.NewAgentKey()
	der, _, _ := ca.SignClient(csr, "agent-abc", time.Now())
	c := &Credentials{AgentID: "agent-abc", Key: key, CertDER: der, CADER: ca.CertDER()}

	if err := c.CheckHub(t.Context(), tlsHubAt(t, ca)); err != nil {
		t.Errorf("the hub with the pinned CA was refused: %v", err)
	}
	if err := c.CheckHub(t.Context(), tlsHubAt(t, other)); err == nil {
		t.Error("a hub with another CA was accepted")
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().String()
	_ = ln.Close()
	if err := c.CheckHub(t.Context(), closed); err == nil {
		t.Error("a closed port was accepted")
	}
}
