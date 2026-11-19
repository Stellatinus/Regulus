package hub

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/CodexCircle/Regulus/internal/config"
)

// writeCert writes a self-signed certificate for 127.0.0.1 with the given
// common name, and its key, to dir.
func writeCert(t *testing.T, dir, name string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func commonName(t *testing.T, c *tls.Certificate) string {
	t.Helper()
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf.Subject.CommonName
}

func TestCertFilesRefuseAHalfOrBrokenPair(t *testing.T) {
	dir := t.TempDir()
	cert, key := writeCert(t, dir, "one")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := newCertFiles(cert, "", log); err == nil || !strings.Contains(err.Error(), "together") {
		t.Errorf("a certificate without a key: %v", err)
	}
	if _, err := newCertFiles("", key, log); err == nil {
		t.Error("a key without a certificate was accepted")
	}
	if _, err := newCertFiles(filepath.Join(dir, "missing.pem"), key, log); err == nil {
		t.Error("a missing certificate file was accepted")
	}
	other := t.TempDir()
	_, otherKey := writeCert(t, other, "two")
	if _, err := newCertFiles(cert, otherKey, log); err == nil {
		t.Error("a key that does not match the certificate was accepted")
	}
	if _, err := newCertFiles(cert, key, log); err != nil {
		t.Errorf("a good pair: %v", err)
	}
}

func TestCertFilesReloadWhenTheFilesChange(t *testing.T) {
	dir := t.TempDir()
	cert, key := writeCert(t, dir, "first")
	files, err := newCertFiles(cert, key, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	files.now = func() time.Time { return now }

	writeCert(t, dir, "second")
	// Make sure the modification time differs on file systems with a coarse clock.
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(cert, later, later)
	_ = os.Chtimes(key, later, later)

	got, _ := files.get(nil)
	if commonName(t, got) != "first" {
		t.Error("the files were read again before certRecheck passed")
	}
	now = now.Add(certRecheck)
	got, _ = files.get(nil)
	if commonName(t, got) != "second" {
		t.Error("the renewed certificate was not loaded")
	}

	// A broken file keeps the certificate already loaded.
	if err := os.WriteFile(key, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	evenLater := later.Add(2 * time.Second)
	_ = os.Chtimes(key, evenLater, evenLater)
	now = now.Add(certRecheck)
	got, err = files.get(nil)
	if err != nil || commonName(t, got) != "second" {
		t.Errorf("a broken key replaced the loaded certificate: %v", err)
	}
}

// serveOn starts a hub on ln with cfg and returns its log; the hub stops at
// the end of the test.
func serveOn(t *testing.T, cfg config.Config, ln net.Listener) *syncBuffer {
	t.Helper()
	cfg.DataDir = t.TempDir()
	cfg.BuiltinAgent = false
	cfg.Updates.Check = false
	useTestDatabase(t, &cfg)
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	h, err := New(ctx, cfg, slog.New(slog.NewTextHandler(logs, nil)), fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- h.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
		_ = h.Close()
	})
	return logs
}

func waitForLog(t *testing.T, logs *syncBuffer, text string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), text) {
		if time.Now().After(deadline) {
			t.Fatalf("the log never said %q:\n%s", text, logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServeOverHTTPSWithTheConfiguredCertificate(t *testing.T) {
	cfg := config.Default()
	cfg.TLSCert, cfg.TLSKey = writeCert(t, t.TempDir(), "hub")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	logs := serveOn(t, cfg, ln)
	waitForLog(t, logs, "hub listening")

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // a self-signed test certificate
	res, err := client.Get("https://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatalf("HTTPS request: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || res.TLS == nil || commonName(t, &tls.Certificate{Certificate: [][]byte{res.TLS.PeerCertificates[0].Raw}}) != "hub" {
		t.Errorf("status %d, TLS %v", res.StatusCode, res.TLS != nil)
	}
	if strings.Contains(logs.String(), "plain HTTP") {
		t.Error("a hub with TLS warned about plain HTTP")
	}

	// Plain HTTP on the same port gets no page.
	plain := &http.Client{Timeout: 5 * time.Second}
	if res, err := plain.Get("http://" + ln.Addr().String() + "/healthz"); err == nil {
		_ = res.Body.Close()
		if res.StatusCode == http.StatusOK {
			t.Error("the HTTPS port answered plain HTTP")
		}
	}
}

func TestServeRefusesAHalfTLSConfiguration(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.BuiltinAgent = false
	cfg.Updates.Check = false
	useTestDatabase(t, &cfg)
	cfg.TLSCert, _ = writeCert(t, t.TempDir(), "hub")
	h, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Serve(context.Background(), ln); err == nil || !strings.Contains(err.Error(), "together") {
		t.Errorf("Serve with tls_cert alone: %v", err)
	}
}

func TestServeWarnsAboutPlainHTTPBeyondLoopback(t *testing.T) {
	cases := []struct {
		name string
		addr net.Addr
		warn bool
	}{
		{"loopback", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}, false},
		{"IPv6 loopback", &net.TCPAddr{IP: net.IPv6loopback, Port: 8080}, false},
		{"every interface", &net.TCPAddr{IP: net.IPv6unspecified, Port: 8080}, true},
		{"a LAN address", &net.TCPAddr{IP: net.IPv4(192, 168, 1, 10), Port: 8080}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			logs := serveOn(t, config.Default(), addrListener{Listener: ln, addr: tc.addr})
			waitForLog(t, logs, "hub listening")
			if got := strings.Contains(logs.String(), "plain HTTP"); got != tc.warn {
				t.Errorf("warned = %v, want %v:\n%s", got, tc.warn, logs.String())
			}
		})
	}
}

// addrListener reports another address than the one it is bound to, so the
// warning can be tested without binding to every interface.
type addrListener struct {
	net.Listener
	addr net.Addr
}

func (l addrListener) Addr() net.Addr { return l.addr }

func TestDefaultListenIsLoopback(t *testing.T) {
	if !config.LoopbackOnly(config.Default().Listen) {
		t.Errorf("the built-in listen address %q reaches other machines", config.Default().Listen)
	}
}
