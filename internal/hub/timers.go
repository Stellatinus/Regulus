package hub

import (
	"context"
	"time"
)

// startTimers starts the work that must run in one hub only: the periodic
// rollout check and the update checks (#344). It does nothing before Serve has
// set its context or while the timers already run. A demoted hub ends them with
// demote and calls this again when it holds the lock again (#356).
func (h *Hub) startTimers() {
	h.timersMu.Lock()
	defer h.timersMu.Unlock()
	if h.serveCtx == nil || h.serveCtx.Err() != nil || h.timersRunning {
		return
	}
	tctx, stop := context.WithCancel(h.serveCtx)
	h.stopTimers = stop
	h.timersRunning = true

	h.rollout.mu.Lock()
	h.rollout.base = tctx
	h.rollout.mu.Unlock()

	h.timersWG.Add(3)
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
