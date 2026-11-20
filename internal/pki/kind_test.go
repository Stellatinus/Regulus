package pki

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net/url"
	"testing"
	"time"
)

// legacyAgentCert issues what every hub before #316 issued: an agent
// certificate with no kind URI.
func legacyAgentCert(t *testing.T, ca *CA, id string) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	return customClientCert(t, ca, pkix.Name{CommonName: id, Organization: []string{"Regulus agent"}}, nil)
}

func customClientCert(t *testing.T, ca *CA, subject pkix.Name, uris []*url.URL) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, csrDER, err := NewAgentKey()
	if err != nil {
		t.Fatal(err)
	}
	csr, _ := x509.ParseCertificateRequest(csrDER)
	serial, _ := newSerial()
	now := time.Now()
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: serial,
		Subject:      subject,
		NotBefore:    now.Add(-clockSkew),
		NotAfter:     now.Add(ClientValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         uris,
	}, ca.cert, csr.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, leaf
}

// stateFor is the connection state of a verified client certificate.
func stateFor(t *testing.T, ca *CA, leaf *x509.Certificate) tls.ConnectionState {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	chains, err := leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	if err != nil {
		t.Fatal(err)
	}
	return tls.ConnectionState{VerifiedChains: chains}
}

func mustURI(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func TestEveryIssuedCertificateCarriesItsKind(t *testing.T) {
	ca, _ := LoadOrCreate(t.TempDir(), time.Now())
	_, csr, _ := NewAgentKey()
	der, _, err := ca.SignClient(csr, "agent-0123456789abcdef", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != "regulus:agent:agent-0123456789abcdef" {
		t.Errorf("agent certificate URIs = %v", leaf.URIs)
	}
	srv, _ := ca.ServerCertificate(time.Now(), "hub.example")
	sleaf, _ := x509.ParseCertificate(srv.Certificate[0])
	if len(sleaf.URIs) != 1 || sleaf.URIs[0].String() != "regulus:hub" {
		t.Errorf("hub certificate URIs = %v", sleaf.URIs)
	}
	if k, legacy, ok := kindOf(sleaf); !ok || k != KindHub || legacy {
		t.Errorf("kindOf(hub) = %s %v %v", k, legacy, ok)
	}
}

func TestIssueClientRefusals(t *testing.T) {
	ca, _ := LoadOrCreate(t.TempDir(), time.Now())
	_, csr, _ := NewAgentKey()
	if _, _, err := ca.IssueClient(csr, KindHub, "x", time.Now()); err == nil {
		t.Error("the hub kind is not a client certificate")
	}
	if _, _, err := ca.IssueClient(csr, "phone", "x", time.Now()); err == nil {
		t.Error("an unknown kind must be refused")
	}
	for _, id := range []string{"", "a:b", "a/b", "a b", string(make([]byte, 65))} {
		if _, _, err := ca.IssueClient(csr, KindAgent, id, time.Now()); err == nil {
			t.Errorf("id %q must be refused", id)
		}
	}
}

func TestAgentIdentityAcceptsOnlyAgents(t *testing.T) {
	ca, _ := LoadOrCreate(t.TempDir(), time.Now())
	issue := func(kind Kind, id string) *x509.Certificate {
		t.Helper()
		_, csr, _ := NewAgentKey()
		der, _, err := ca.IssueClient(csr, kind, id, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		leaf, _ := x509.ParseCertificate(der)
		return leaf
	}

	id, ok, err := AgentIdentity(stateFor(t, ca, issue(KindAgent, "agent-1")))
	if err != nil || !ok || id.ID != "agent-1" || id.Kind != KindAgent || id.Legacy {
		t.Errorf("agent: %+v %v %v", id, ok, err)
	}

	// A device certificate from the same CA, even one named like an agent,
	// is not an agent.
	device := stateFor(t, ca, issue(KindDevice, "agent-1"))
	if _, ok, err := AgentIdentity(device); !ok || !errors.Is(err, ErrNotAgent) {
		t.Errorf("device: ok = %v, err = %v; want ErrNotAgent", ok, err)
	}
	if id, ok := ClientIdentity(device); !ok || id.Kind != KindDevice {
		t.Errorf("ClientIdentity(device) = %+v %v", id, ok)
	}

	// No certificate: not an error, just no identity.
	if _, ok, err := AgentIdentity(tls.ConnectionState{}); ok || err != nil {
		t.Errorf("anonymous: ok = %v, err = %v", ok, err)
	}
}

func TestLegacyAgentCertificatesStillWork(t *testing.T) {
	ca, _ := LoadOrCreate(t.TempDir(), time.Now())
	_, leaf := legacyAgentCert(t, ca, "agent-old")
	id, ok, err := AgentIdentity(stateFor(t, ca, leaf))
	if err != nil || !ok || id.ID != "agent-old" || id.Kind != KindAgent || !id.Legacy {
		t.Errorf("legacy agent: %+v %v %v", id, ok, err)
	}
}

func TestCertificatesWhoseKindCannotBeTold(t *testing.T) {
	ca, _ := LoadOrCreate(t.TempDir(), time.Now())
	cases := []struct {
		name    string
		subject pkix.Name
		uris    []*url.URL
	}{
		{"no kind and not an agent of an older hub", pkix.Name{CommonName: "x"}, nil},
		{"no kind, another organization", pkix.Name{CommonName: "x", Organization: []string{"Regulus device"}}, nil},
		{"unknown kind", pkix.Name{CommonName: "x", Organization: []string{"Regulus agent"}}, []*url.URL{mustURI("regulus:phone:x")}},
		{"id differs from the common name", pkix.Name{CommonName: "x"}, []*url.URL{mustURI("regulus:agent:y")}},
		{"no id", pkix.Name{CommonName: "x"}, []*url.URL{mustURI("regulus:agent")}},
		{"two kinds", pkix.Name{CommonName: "x"}, []*url.URL{mustURI("regulus:agent:x"), mustURI("regulus:device:x")}},
		{"hub kind with an id", pkix.Name{CommonName: "x"}, []*url.URL{mustURI("regulus:hub:x")}},
	}
	for _, c := range cases {
		_, leaf := customClientCert(t, ca, c.subject, c.uris)
		state := stateFor(t, ca, leaf)
		if id, ok := ClientIdentity(state); ok {
			t.Errorf("%s: ClientIdentity = %+v, want none", c.name, id)
		}
		if _, ok, err := AgentIdentity(state); !ok || !errors.Is(err, ErrNotAgent) {
			t.Errorf("%s: AgentIdentity ok = %v, err = %v; want ErrNotAgent", c.name, ok, err)
		}
	}
	// Other URIs do not count as a kind.
	_, leaf := customClientCert(t, ca, pkix.Name{CommonName: "agent-2", Organization: []string{"Regulus agent"}}, []*url.URL{mustURI("https://example.org/x")})
	if id, ok := ClientIdentity(stateFor(t, ca, leaf)); !ok || id.Kind != KindAgent || !id.Legacy {
		t.Errorf("legacy agent with another URI: %+v %v", id, ok)
	}
}

func TestAgentsRefuseAListenerCertificateOfAnotherKind(t *testing.T) {
	ca, _ := LoadOrCreate(t.TempDir(), time.Now())
	now := time.Now()
	issue := func(uris []*url.URL) [][]byte {
		_, csrDER, _ := NewAgentKey()
		csr, _ := x509.ParseCertificateRequest(csrDER)
		serial, _ := newSerial()
		der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			SerialNumber: serial,
			Subject:      pkix.Name{CommonName: "Regulus hub"},
			NotBefore:    now.Add(-clockSkew),
			NotAfter:     now.Add(serverValidity),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			URIs:         uris,
		}, ca.cert, csr.PublicKey, ca.key)
		if err != nil {
			t.Fatal(err)
		}
		return [][]byte{der, ca.CertDER()}
	}
	if err := verifyPinned(ca.Fingerprint(), issue(nil), now); err != nil {
		t.Errorf("a listener certificate of an older hub (no kind): %v", err)
	}
	if err := verifyPinned(ca.Fingerprint(), issue([]*url.URL{mustURI("regulus:hub")}), now); err != nil {
		t.Errorf("a hub certificate: %v", err)
	}
	if err := verifyPinned(ca.Fingerprint(), issue([]*url.URL{mustURI("regulus:device:x")}), now); err == nil {
		t.Error("a listener certificate of another kind must be refused")
	}
}
