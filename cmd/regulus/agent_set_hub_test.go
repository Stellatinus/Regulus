package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/pki"
)

// enrolledAgent writes the credentials of an agent of ca, as Enroll saves
// them, and a configuration file that points at oldHub.
func enrolledAgent(t *testing.T, ca *pki.CA, oldHub string) (cfgFile, dataDir string) {
	t.Helper()
	dir := t.TempDir()
	dataDir = filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	key, csr, err := pki.NewAgentKey()
	if err != nil {
		t.Fatal(err)
	}
	der, _, err := ca.SignClient(csr, "agent-abc", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	for name, block := range map[string]*pem.Block{
		"agent.key": {Type: "EC PRIVATE KEY", Bytes: keyDER},
		"agent.crt": {Type: "CERTIFICATE", Bytes: der},
		"ca.crt":    {Type: "CERTIFICATE", Bytes: ca.CertDER()},
	} {
		if err := os.WriteFile(filepath.Join(dataDir, name), pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfgFile = filepath.Join(dir, "agent.yaml")
	text := "# the agent\nhub: \"" + oldHub + "\"\ndata_dir: " + dataDir + "\n"
	if err := os.WriteFile(cfgFile, []byte(text), 0o640); err != nil {
		t.Fatal(err)
	}
	return cfgFile, dataDir
}

// hubOf starts a TLS listener signed by ca and returns its address.
func hubOf(t *testing.T, ca *pki.CA) string {
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

func newCA(t *testing.T) *pki.CA {
	t.Helper()
	ca, err := pki.LoadOrCreate(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func setHubRun(t *testing.T, root bool, restartErr error, args ...string) (code int, out, errOut string, restarts int) {
	t.Helper()
	var so, se bytes.Buffer
	env := setHubEnv{
		getenv: func(string) string { return "" }, stdout: &so, stderr: &se,
		isRoot:  func() bool { return root },
		restart: func(context.Context) error { restarts++; return restartErr },
	}
	code = agentSetHub(t.Context(), args, env)
	return code, so.String(), se.String(), restarts
}

func TestAgentSetHubWritesTheNewAddressAndRestarts(t *testing.T) {
	ca := newCA(t)
	file, _ := enrolledAgent(t, ca, "192.168.1.20:8443")
	newAddr := hubOf(t, ca)

	code, out, errOut, restarts := setHubRun(t, true, nil, "--config", file, newAddr)
	if code != 0 || restarts != 1 || !strings.Contains(out, "restarted") {
		t.Fatalf("code = %d, restarts = %d\nout: %s\nerr: %s", code, restarts, out, errOut)
	}
	data, _ := os.ReadFile(file)
	if !strings.Contains(string(data), `hub: "`+newAddr+`"`) || !strings.HasPrefix(string(data), "# the agent\n") {
		t.Errorf("configuration:\n%s", data)
	}
}

func TestAgentSetHubRefusesAnotherHub(t *testing.T) {
	file, _ := enrolledAgent(t, newCA(t), "192.168.1.20:8443")
	impostor := hubOf(t, newCA(t))
	code, _, errOut, restarts := setHubRun(t, true, nil, "--config", file, impostor)
	if code != 1 || restarts != 0 || !strings.Contains(errOut, "Nothing was changed") {
		t.Fatalf("code = %d, restarts = %d, err: %s", code, restarts, errOut)
	}
	if data, _ := os.ReadFile(file); !strings.Contains(string(data), "192.168.1.20:8443") {
		t.Errorf("the configuration changed:\n%s", data)
	}
}

func TestAgentSetHubOptions(t *testing.T) {
	ca := newCA(t)
	file, _ := enrolledAgent(t, ca, "192.168.1.20:8443")

	// A hub that is switched off now: --no-check writes it anyway, and a
	// user who is not root is told the command instead of a restart.
	code, out, errOut, restarts := setHubRun(t, false, nil, "--config", file, "--no-check", "hub.lan:8443")
	if code != 0 || restarts != 0 || !strings.Contains(out, "sudo systemctl restart regulus-agent.service") {
		t.Fatalf("code = %d, restarts = %d\nout: %s\nerr: %s", code, restarts, out, errOut)
	}
	// --no-restart as root does not restart either.
	code, _, _, restarts = setHubRun(t, true, nil, "--config", file, "--no-check", "--no-restart", "other.lan:8443")
	if code != 0 || restarts != 0 {
		t.Errorf("--no-restart: code = %d, restarts = %d", code, restarts)
	}
	// A restart that fails is reported with the command to run by hand.
	code, _, errOut, _ = setHubRun(t, true, errors.New("boom"), "--config", file, "--no-check", "last.lan:8443")
	if code != 1 || !strings.Contains(errOut, "systemctl restart regulus-agent.service") {
		t.Errorf("failed restart: code = %d, err: %s", code, errOut)
	}
	// A bad address and a missing argument.
	if code, _, _, _ := setHubRun(t, true, nil, "--config", file, "no-port"); code == 0 {
		t.Error("an address without a port was accepted")
	}
	if code, _, _, _ := setHubRun(t, true, nil, "--config", file); code != 2 {
		t.Errorf("no address: code = %d, want 2", code)
	}
}

func TestAgentSetHubNeedsAnEnrolledAgent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "agent.yaml")
	text := "hub: \"192.168.1.20:8443\"\ndata_dir: " + filepath.Join(dir, "empty") + "\n"
	if err := os.WriteFile(file, []byte(text), 0o640); err != nil {
		t.Fatal(err)
	}
	code, _, errOut, _ := setHubRun(t, true, nil, "--config", file, "--no-check", "hub.lan:8443")
	if code != 1 || !strings.Contains(errOut, "not enrolled") {
		t.Errorf("code = %d, err: %s", code, errOut)
	}
}
