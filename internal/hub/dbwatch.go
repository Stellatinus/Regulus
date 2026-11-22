package hub

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/CodexCircle/Regulus/internal/storage"
)

// dbWatchInterval is how often the hub checks that the database answers.
const dbWatchInterval = 15 * time.Second

// dbHealth remembers whether the database answered the last time it was asked,
// so the problem can be shown (regulus status, the console) and logged once
// when it starts and once when it ends, not at every check.
type dbHealth struct {
	mu      sync.Mutex
	problem string
	// lost is set when another hub took over the database (#344); it goes
	// away when this hub holds the lock again (#356), whatever the pings say.
	lost string
}

func (d *dbHealth) setLost(p string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lost = p
}

func (d *dbHealth) set(p string) (changed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	changed = d.problem != p
	d.problem = p
	return changed
}

func (d *dbHealth) get() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lost != "" {
		return d.lost
	}
	return d.problem
}

// checkDatabase pings the database once and records the result. database/sql
// opens a new connection by itself after PostgreSQL restarts, so the hub keeps
// running and recovers without a restart; this only reports it.
func (h *Hub) checkDatabase(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	problem := ""
	began := time.Now()
	if err := h.db.PingContext(pctx); err != nil {
		problem = storage.Explain(err)
	} else {
		h.stats.dbRead(time.Since(began))
	}
	if problem == "" {
		h.checkActiveLock(pctx)
	}
	if !h.dbState.set(problem) {
		return
	}
	if problem != "" {
		h.log.Error("database is not answering", "backend", h.cfg.Storage().Describe(), "problem", problem)
	} else {
		h.log.Info("database answers again", "backend", h.cfg.Storage().Describe())
	}
}

// checkActiveLock makes sure this hub still holds the "active hub" lock of the
// PostgreSQL database (#344). The lock lives as long as one database session;
// when PostgreSQL ends that session the hub takes the lock again. If another
// hub already holds it, two hubs are running on one database: this one logs
// loudly and stops the timers that must run in one place only (update checks,
// automatic rollouts) until it is restarted.
func (h *Hub) checkActiveLock(ctx context.Context) {
	retaken, err := h.store.EnsureActive(ctx)
	switch {
	case err == nil && h.demoted.Load():
		h.promote()
		h.log.Warn("the other hub is gone: this hub holds the active-hub lock again and started its update and rollout timers", "backend", h.cfg.Storage().Describe())
	case err == nil && retaken:
		h.log.Warn("the database session that holds the active-hub lock was lost; the lock was taken again", "backend", h.cfg.Storage().Describe())
	case errors.Is(err, storage.ErrHubActive):
		if h.demote() {
			h.log.Error("another hub holds the active-hub lock of this database; this hub stopped its update and rollout timers and starts them again when the other hub is gone", "backend", h.cfg.Storage().Describe())
		}
	case err != nil && ctx.Err() == nil:
		h.log.Warn("checking the active-hub lock", "err", storage.Explain(err))
	}
}

// demote stops the timers of a hub that no longer holds the active-hub lock
// and records the problem, which regulus status and the console show. It
// reports whether this call did it, so the log says it once.
func (h *Hub) demote() bool {
	if h.demoted.Swap(true) {
		return false
	}
	h.dbState.setLost("another hub took over this database: this hub stopped its update and rollout timers; it starts them again by itself when the other hub is stopped")
	h.timersMu.Lock()
	stop := h.stopTimers
	h.timersRunning = false
	h.timersMu.Unlock()
	if stop != nil {
		stop()
	}
	return true
}

// promote undoes demote when the other hub is gone and this one holds the lock
// again (#356): the problem is cleared and the timers start again. The lock is
// the only authority, so there is no approval step; a redundant hub (#103)
// will decide who is active by the term of #108, and this check goes there.
func (h *Hub) promote() {
	if !h.demoted.Swap(false) {
		return
	}
	h.dbState.setLost("")
	h.startTimers()
}

// watchDatabase checks the database until ctx ends, once at the start and
// then every dbWatchInterval. Only a remote database can go away, so SQLite is
// not polled. The local status socket reports what this records instead of
// pinging itself (#313).
func (h *Hub) watchDatabase(ctx context.Context) {
	if !h.cfg.Storage().IsPostgres() {
		return
	}
	go func() {
		t := time.NewTicker(dbWatchInterval)
		defer t.Stop()
		h.checkDatabase(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.checkDatabase(ctx)
			}
		}
	}()
}
