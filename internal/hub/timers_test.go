package hub

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/CodexCircle/Regulus/internal/config"
)

// #473: demote cancels the timers without waiting for them, and the history
// timer still makes a last flush. Starting the timers again must wait for the
// old ones to return, or two generations share the history at the same time.
func TestStartTimersWaitsForTheTimersItReplaces(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	useTestDatabase(t, &cfg)
	h, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	h.serveCtx = t.Context()
	h.startTimers()
	defer h.endTimers()
	h.demote() // cancels the timers, does not wait for them

	// An old timer that is still finishing its last flush.
	h.timersWG.Add(1)
	release := make(chan struct{})
	var once sync.Once
	closeRelease := func() { once.Do(func() { close(release) }) }
	defer closeRelease() // before endTimers waits, so a failure does not hang
	go func() {
		<-release
		h.timersWG.Done()
	}()

	started := make(chan struct{})
	go func() {
		h.startTimers()
		close(started)
	}()
	select {
	case <-started:
		t.Fatal("the timers started again while an old one was still running")
	case <-time.After(200 * time.Millisecond):
	}
	h.timersMu.Lock()
	running := h.timersRunning
	h.timersMu.Unlock()
	if running {
		t.Fatal("the timers are marked as running before the old ones returned")
	}

	closeRelease()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the timers did not start after the old one returned")
	}
	h.timersMu.Lock()
	running = h.timersRunning
	h.timersMu.Unlock()
	if !running {
		t.Fatal("the timers are not running after the old one returned")
	}
}

// demote must not wait for the old timers: it is called from the database
// watcher, and a wait there would hold up the check that promotes the hub.
func TestDemoteDoesNotWaitForTheTimers(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	useTestDatabase(t, &cfg)
	h, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	h.serveCtx = t.Context()
	h.startTimers()
	defer h.endTimers()

	h.timersWG.Add(1) // an old timer that never returns during the test
	defer h.timersWG.Done()
	done := make(chan struct{})
	go func() {
		h.demote()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("demote waited for the timers")
	}
}
