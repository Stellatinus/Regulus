package console

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/localstatus"
)

func lanSnap(t *testing.T, listen string, sys System) Check {
	t.Helper()
	cfgFile := filepath.Join(t.TempDir(), "config.yaml")
	body := "listen: \"" + listen + "\"\ndata_dir: \"" + t.TempDir() + "\"\n"
	if err := os.WriteFile(cfgFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := Collect(context.Background(), sys, cfgFile, "v1")
	for _, c := range snap.Health {
		if c.Key == "lan" {
			return c
		}
	}
	t.Fatalf("no lan check: %+v", snap.Health)
	return Check{}
}

// #369: the Health tab says whether the dashboard can be opened from the
// network, as far as this machine can tell.
func TestLANCheck(t *testing.T) {
	var dialed []string
	sys := fakeSystem()
	sys.Dial = func(_ context.Context, addr string) error { dialed = append(dialed, addr); return nil }

	if c := lanSnap(t, "127.0.0.1:8080", sys); c.Status != Info || !strings.Contains(c.Detail, "only this machine") || !strings.Contains(c.Detail, "0.0.0.0:8080") {
		t.Errorf("loopback: %+v", c)
	}
	if c := lanSnap(t, ":8080", sys); c.Status != OK || !strings.Contains(c.Detail, "http://192.168.1.20:8080") || !strings.Contains(c.Detail, "firewall") {
		t.Errorf("wildcard answering: %+v", c)
	}
	if len(dialed) != 1 || dialed[0] != "192.168.1.20:8080" {
		t.Errorf("dialed %v", dialed)
	}

	sys.Dial = func(context.Context, string) error { return errors.New("refused") }
	if c := lanSnap(t, ":8080", sys); c.Status != Warn || !strings.Contains(c.Detail, "192.168.1.20:8080") {
		t.Errorf("wildcard not answering: %+v", c)
	}

	stopped := sys
	stopped.Fetch = func(context.Context, string) (localstatus.Status, error) { return localstatus.Status{}, errors.New("refused") }
	if c := lanSnap(t, ":8080", stopped); c.Status != Info || !strings.Contains(c.Detail, "start the hub") {
		t.Errorf("stopped: %+v", c)
	}

	noAddr := sys
	noAddr.Addrs = func() []string { return nil }
	if c := lanSnap(t, ":8080", noAddr); c.Status != Info || !strings.Contains(c.Detail, "no network address") {
		t.Errorf("no address: %+v", c)
	}
}

// The log tab follows a stream instead of reloading, shows what arrives, and
// stops the stream when the tab is left.
func TestLogsFollowAStream(t *testing.T) {
	ch := make(chan string, 8)
	cancelled := make(chan struct{}, 4)
	sys := fakeSystem()
	sys.Follow = func(ctx context.Context, n int) (<-chan string, error) {
		go func() { <-ctx.Done(); cancelled <- struct{}{} }()
		return ch, nil
	}
	m := New(Options{System: sys, Version: "v1", Unicode: true})

	next, cmd := m.switchTo(tabLogs)
	m = next.(Model)
	if m.follow == nil || cmd == nil {
		t.Fatal("no stream started")
	}
	ch <- `time=2026-10-25T11:58:01Z level=INFO msg="first"`
	ch <- `time=2026-10-25T11:58:02Z level=INFO msg="second"`
	next, _ = m.Update(cmd())
	m = next.(Model)
	if len(m.logs) != 2 || !strings.Contains(strings.Join(m.logs, "\n"), "second") {
		t.Fatalf("logs: %v", m.logs)
	}

	// A message of an older stream is ignored.
	next, _ = m.Update(logLinesMsg{gen: m.follow.gen - 1, lines: []string{"stale"}})
	m = next.(Model)
	if len(m.logs) != 2 {
		t.Errorf("stale lines were taken: %v", m.logs)
	}

	// The stream is bounded.
	for i := 0; i < maxLogLines+10; i++ {
		m.logs = append(m.logs, "x")
	}
	next, _ = m.Update(logLinesMsg{gen: m.follow.gen, lines: []string{"last"}})
	m = next.(Model)
	if len(m.logs) != maxLogLines || m.logs[len(m.logs)-1] != "last" {
		t.Errorf("kept %d lines", len(m.logs))
	}

	// The stream ends: the lines stay and the tab reloads on its ticks.
	next, _ = m.Update(logLinesMsg{gen: m.follow.gen, closed: true})
	m = next.(Model)
	if m.follow != nil {
		t.Error("stream still set after it closed")
	}

	next, _ = m.switchTo(tabLogs)
	m = next.(Model)
	next, _ = m.switchTo(tabStatus)
	m = next.(Model)
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Error("the stream was not cancelled when the tab was left")
	}
}
