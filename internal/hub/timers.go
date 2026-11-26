package hub

import (
	"context"
	"time"
)

// startTimers starts the work that must run in one hub only: the periodic
// rollout check and the update checks (#344). It does nothing before Serve has
// set its context or while the timers already run. A demoted hub ends them with
// demote and calls this again when it holds the lock again (#356).
//
// demote cancels the old timers but does not wait for them, and the history one
// still makes a last flush with a context of its own. So the new timers are
// started only after the old ones have returned (#473): they would otherwise
// share the history (its window, its last prune) and could write it at the same
// time. The wait is outside timersMu, so demote and endTimers are never held up
// by it; the conditions are checked again after it, because they may have
// changed. Only one goroutine starts timers at a time (Serve at the start, then
// the database watcher through promote), so the wait group is not reused while
// another Wait is pending.
func (h *Hub) startTimers() {
	if !h.timersMayStart() {
		return
	}
	h.timersWG.Wait()
	h.timersMu.Lock()
	defer h.timersMu.Unlock()
	if !h.timersMayStartLocked() {
		return
	}
	tctx, stop := context.WithCancel(h.serveCtx)
	h.stopTimers = stop
	h.timersRunning = true

	h.rollout.mu.Lock()
	h.rollout.base = tctx
	h.rollout.mu.Unlock()

	h.timersWG.Add(4)
	go func() {
		defer h.timersWG.Done()
		tick := time.NewTicker(5 * time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-tctx.Done():
				return
			case <-tick.C:
				h.autoRollout(tctx)
			}
		}
	}()
	go func() {
		defer h.timersWG.Done()
		h.updates.Run(tctx)
	}()
	go func() {
		defer h.timersWG.Done()
		h.history.run(tctx)
	}()
	go func() {
		defer h.timersWG.Done()
		h.runAlerts(tctx)
	}()
}

// timersMayStart reports whether startTimers has anything to do.
func (h *Hub) timersMayStart() bool {
	h.timersMu.Lock()
	defer h.timersMu.Unlock()
	return h.timersMayStartLocked()
}

func (h *Hub) timersMayStartLocked() bool {
	return h.serveCtx != nil && h.serveCtx.Err() == nil && !h.timersRunning
}

// endTimers stops the timers and waits for them; Serve calls it on the way out.
func (h *Hub) endTimers() {
	h.timersMu.Lock()
	stop := h.stopTimers
	h.timersRunning = false
	h.timersMu.Unlock()
	if stop != nil {
		stop()
	}
	h.timersWG.Wait()
}
