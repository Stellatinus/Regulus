package console

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// enrolledDir writes the credentials of an enrolled agent the way Enroll
// does, with a certificate that ends on 2027-02-01.
func enrolledDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "agent-data")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "agent-7f3a"},
		NotBefore:    fixedNow.Add(-time.Hour),
		NotAfter:     time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	write := func(name, typ string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("agent.key", "EC PRIVATE KEY", keyDER)
	write("agent.crt", "CERTIFICATE", der)
	write("ca.crt", "CERTIFICATE", der)
	return dir
}

func agentConfig(t *testing.T, dataDir string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte("hub: hub.example:8443\ndata_dir: "+dataDir+"\nupdates: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeAgentSystem(state string, logs []string) AgentSystem {
	return AgentSystem{
		Getenv:       func(string) string { return "" },
		Now:          func() time.Time { return fixedNow },
		Hostname:     func() (string, error) { return "web2", nil },
		ServiceState: func(context.Context) string { return state },
		Logs: func(context.Context, int) ([]string, error) {
			if logs == nil {
				return nil, errors.New("no journal")
			}
			return logs, nil
		},
	}
}

var connectedLog = []string{
	`time=2026-10-25T11:58:01Z level=INFO msg="starting agent" version=v0.3.0`,
	`time=2026-10-25T11:58:02Z level=INFO msg="connected to the hub" agent=agent-7f3a protocol_version=1`,
}

// stableAgentPaths replaces the temporary paths of a test in a screen or in
// the text output, so that the golden files do not depend on them.
func stableAgentPaths(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		switch {
		case strings.Contains(l, "agent-data"):
			lines[i] = l[:strings.Index(l, "/")] + "/var/lib/regulus-agent"
		case strings.HasSuffix(l, "agent.yaml"):
			lines[i] = l[:strings.Index(l, "/")] + "/etc/regulus/agent.yaml"
		}
	}
	return strings.Join(lines, "\n")
}

func TestRole(t *testing.T) {
	read := func(s string, err error) func(string) ([]byte, error) {
		return func(p string) ([]byte, error) {
			if p != RoleFile {
				t.Errorf("read %s, want %s", p, RoleFile)
			}
			return []byte(s), err
		}
	}
	for _, tc := range []struct {
		name string
		read func(string) ([]byte, error)
		want string
	}{
		{"agent", read("agent\n", nil), RoleAgent},
		{"no file is a hub", read("", os.ErrNotExist), RoleHub},
		{"another word is a hub", read("hub\n", nil), RoleHub},
	} {
		if got := Role(tc.read); got != tc.want {
			t.Errorf("%s: role %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestLinkFromLog(t *testing.T) {
	with := func(extra string) []string { return append(append([]string{}, connectedLog...), extra) }
	for _, tc := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"connected", connectedLog, LinkConnected},
		{"lost after connected", with(`level=WARN msg="connection to the hub lost" err="eof"`), LinkRetrying},
		{"connected again after a loss", append([]string{`level=WARN msg="connection to the hub lost"`}, connectedLog...), LinkConnected},
		{"cloned identity", with(`level=WARN msg="the hub has another connection with this agent's identity (a cloned machine?); waiting"`), LinkRetrying},
		{"rejected", with(`level=ERROR msg="the hub does not accept this agent: it was revoked"`), LinkRejected},
		{"nothing to say", []string{`level=INFO msg="starting agent"`}, LinkUnknown},
	} {
		if got := linkFromLog(tc.lines); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCollectAgent(t *testing.T) {
	dir := enrolledDir(t)
	cfg := agentConfig(t, dir)

	snap := CollectAgent(t.Context(), fakeAgentSystem("active", connectedLog), cfg, "v0.3.0")
	if !snap.Running || snap.Link != LinkConnected || !snap.Enrolled || snap.AgentID != "agent-7f3a" || snap.Hub != "hub.example:8443" || snap.Problem != "" {
		t.Errorf("working agent: %+v", snap)
	}
	if got := snap.CertNotAfter.UTC().Format(time.DateOnly); got != "2027-02-01" {
		t.Errorf("certificate end %s", got)
	}

	snap = CollectAgent(t.Context(), fakeAgentSystem("inactive", connectedLog), cfg, "v0.3.0")
	if snap.Running || snap.Link != LinkStopped {
		t.Errorf("stopped agent: %+v", snap)
	}

	snap = CollectAgent(t.Context(), fakeAgentSystem("active", nil), cfg, "v0.3.0")
	if snap.Link != LinkUnknown {
		t.Errorf("an unreadable log leaves the link unknown, got %q", snap.Link)
	}

	snap = CollectAgent(t.Context(), fakeAgentSystem("inactive", nil), agentConfig(t, t.TempDir()), "v0.3.0")
	if snap.Enrolled || snap.Problem != "" {
		t.Errorf("an agent without credentials is not enrolled, and that is no problem: %+v", snap)
	}

	snap = CollectAgent(t.Context(), fakeAgentSystem("inactive", nil), filepath.Join(t.TempDir(), "missing.yaml"), "v0.3.0")
	if snap.Problem == "" {
		t.Error("a missing configuration file is reported")
	}
}

func TestAgentStatusOutput(t *testing.T) {
	cfg := agentConfig(t, enrolledDir(t))
	for _, tc := range []struct {
		name   string
		italic bool
		f      Format
	}{
		{"agent-status", false, FormatText},
		{"agent-status-it", true, FormatText},
		{"agent-status-plain", false, FormatPlain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.italic {
				italian(t)
			}
			snap := CollectAgent(t.Context(), fakeAgentSystem("active", connectedLog), cfg, "v0.3.0")
			var b bytes.Buffer
			if err := WriteAgentStatus(&b, snap, tc.f, fixedNow); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(b.String(), "inactive") {
				t.Error("a working agent is not reported as inactive")
			}
			golden(t, tc.name, stableAgentPaths(b.String()))
		})
	}
	snap := CollectAgent(t.Context(), fakeAgentSystem("active", connectedLog), cfg, "v0.3.0")
	var b bytes.Buffer
	if err := WriteAgentStatus(&b, snap, FormatJSON, fixedNow); err != nil || !strings.Contains(b.String(), `"link": "connected"`) {
		t.Errorf("json: %v %s", err, b.String())
	}
}

func TestAgentScreens(t *testing.T) {
	cfg := agentConfig(t, enrolledDir(t))
	for _, tc := range []struct {
		name  string
		it    bool
		state string
		keys  []tea.KeyMsg
	}{
		{"agent-screen", false, "active", nil},
		{"agent-screen-it", true, "active", nil},
		{"agent-screen-logs", false, "active", []tea.KeyMsg{rune1('2')}},
		{"agent-screen-restart-confirm", false, "active", []tea.KeyMsg{rune1('r')}},
		{"agent-screen-stopped", false, "inactive", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.it {
				italian(t)
			}
			m := NewAgent(AgentOptions{System: fakeAgentSystem(tc.state, connectedLog), ConfigFile: cfg, Version: "v0.3.0", Unicode: true,
				Restart: func(context.Context) error { return nil }})
			next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
			m = settleAgent(t, next.(AgentModel), next.(AgentModel).collect())
			for _, k := range tc.keys {
				next, cmd := m.Update(k)
				m = settleAgent(t, next.(AgentModel), cmd)
			}
			golden(t, tc.name, stableAgentPaths(trimLines(m.View())))
		})
	}
}

func TestAgentRestartAsksFirst(t *testing.T) {
	restarted := 0
	m := NewAgent(AgentOptions{System: fakeAgentSystem("active", connectedLog), ConfigFile: agentConfig(t, enrolledDir(t)), Version: "v0.3.0",
		Restart: func(context.Context) error { restarted++; return nil }})
	m = settleAgent(t, m, m.collect())
	for _, k := range []tea.KeyMsg{rune1('r'), rune1('n')} {
		next, cmd := m.Update(k)
		m = settleAgent(t, next.(AgentModel), cmd)
	}
	if restarted != 0 {
		t.Fatal("n must not restart the agent")
	}
	for _, k := range []tea.KeyMsg{rune1('r'), rune1('y')} {
		next, cmd := m.Update(k)
		m = settleAgent(t, next.(AgentModel), cmd)
	}
	if restarted != 1 || m.notice != T.AgentMsgRestarted {
		t.Errorf("restarted %d times, notice %q", restarted, m.notice)
	}
}

func settleAgent(t *testing.T, m AgentModel, cmd tea.Cmd) AgentModel {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = settleAgent(t, m, c)
		}
		return m
	}
	if _, quit := msg.(tea.QuitMsg); quit || msg == nil {
		return m
	}
	next, cmd := m.Update(msg)
	return settleAgent(t, next.(AgentModel), cmd)
}
