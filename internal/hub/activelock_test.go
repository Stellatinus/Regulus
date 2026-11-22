package hub

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"testing/fstest"
	"time"

	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/storage"
	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

func activeLockHub(t *testing.T) (*Hub, config.Config) {
	t.Helper()
	if !storagetest.Postgres() {
		t.Skip("PostgreSQL only: the active-hub lock does not exist on SQLite")
	}
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	useTestDatabase(t, &cfg)
	h, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h, cfg
}

// endLockSession ends the PostgreSQL session that holds the lock, as
// pg_terminate_backend does, and waits until the lock is free.
func endLockSession(t *testing.T, h *Hub) {
	t.Helper()
	const key = `hashtext('regulus-active-hub:' || current_schema())::bigint`
	if _, err := h.store.Write.Exec(`SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype = 'advisory' AND objsubid = 1
		AND granted AND pid <> pg_backend_pid() AND ((classid::bigint << 32) | objid::bigint) = ` + key); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var held bool
		if err := h.store.Write.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype = 'advisory' AND objsubid = 1
			AND ((classid::bigint << 32) | objid::bigint) = ` + key + `)`).Scan(&held); err != nil {
			t.Fatal(err)
		}
		if !held {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the lock was not released")
		}
	}
}

// TestHubTakesTheActiveLockAgainAfterTheSessionIsLost is #344: the database
// watcher notices the lost lock and takes it again, so a second hub still
// cannot start, and the hub keeps its timers.
func TestHubTakesTheActiveLockAgainAfterTheSessionIsLost(t *testing.T) {
	h, cfg := activeLockHub(t)
	stopped := false
	h.stopTimers = func() { stopped = true }

	endLockSession(t, h)
	h.checkDatabase(context.Background())

	if h.demoted.Load() || stopped || h.dbState.get() != "" {
		t.Fatalf("demoted = %v, timers stopped = %v, problem = %q; the hub should simply hold the lock again", h.demoted.Load(), stopped, h.dbState.get())
	}
	if second, err := storage.OpenHub(context.Background(), cfg.Storage()); !errors.Is(err, storage.ErrHubActive) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("a second hub on the same database: error = %v, want ErrHubActive", err)
	}
}

// If another hub got in while the lock was free, this one stops its timers and
// says so, instead of running them beside the other.
func TestHubStopsItsTimersWhenAnotherHubTookTheLock(t *testing.T) {
	h, cfg := activeLockHub(t)
	stopped := 0
	h.stopTimers = func() { stopped++ }

	endLockSession(t, h)
	other, err := storage.OpenHub(context.Background(), cfg.Storage())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()

	h.checkDatabase(context.Background())
	h.checkDatabase(context.Background())

	if !h.demoted.Load() || stopped != 1 {
		t.Fatalf("demoted = %v, timers stopped %d times; want demoted and stopped once", h.demoted.Load(), stopped)
	}
	if got := h.dbState.get(); got == "" {
		t.Fatal("the problem is not shown to the administrator")
	}
}

// #356: when the other hub is stopped, the demoted one takes the lock again,
// clears the problem and starts its timers again, without a restart.
func TestDemotedHubStartsItsTimersAgainWhenTheOtherHubIsGone(t *testing.T) {
	h, cfg := activeLockHub(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.serveCtx = ctx
	h.startTimers()
	defer h.endTimers()
	firstBase := h.rollout.base

	endLockSession(t, h)
	other, err := storage.OpenHub(context.Background(), cfg.Storage())
	if err != nil {
		t.Fatal(err)
	}
	h.checkDatabase(context.Background())
	if !h.demoted.Load() || h.dbState.get() == "" || firstBase.Err() == nil {
		t.Fatalf("demoted = %v, problem = %q, timers still running = %v", h.demoted.Load(), h.dbState.get(), firstBase.Err() == nil)
	}

	// The other hub is stopped on purpose.
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	h.checkDatabase(context.Background())
	if h.demoted.Load() || h.dbState.get() != "" {
		t.Fatalf("demoted = %v, problem = %q; the hub should be active again", h.demoted.Load(), h.dbState.get())
	}
	h.rollout.mu.Lock()
	base := h.rollout.base
	h.rollout.mu.Unlock()
	if base.Err() != nil {
		t.Fatal("the timers did not start again")
	}
	// A second hub still cannot start.
	if second, err := storage.OpenHub(context.Background(), cfg.Storage()); !errors.Is(err, storage.ErrHubActive) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("a second hub: error = %v, want ErrHubActive", err)
	}
}
