// rollout_store.go keeps what the rollout of the agents must not forget when the
// hub restarts (#211): the last rollout, server by server, and the version for
// which a rollout failed, which the automatic mode does not try again by
// itself. Both are rows of the settings table, as JSON and as text; no
// migration is needed.

package hub

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const (
	settingRolloutLast   = "rollout.last"
	settingRolloutFailed = "rollout.failed_target"
)

// msgHubRestarted is the message of a server whose result the hub did not see
// because it restarted in the middle of a rollout.
const msgHubRestarted = "the hub restarted before the result was known"

func (h *Hub) saveSetting(ctx context.Context, key, value string) error {
	_, err := h.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

func (h *Hub) loadSetting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := h.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// persistRollout writes the state of run. persistMu orders the writes, so an
// older picture of the run never overwrites a newer one. A failure is logged:
// the rollout goes on, only its memory across a restart is at stake.
func (h *Hub) persistRollout(run *rolloutRun) {
	h.rollout.persistMu.Lock()
	defer h.rollout.persistMu.Unlock()
	h.rollout.mu.Lock()
	st := run.status
	st.Agents = append([]RolloutAgent(nil), run.status.Agents...)
	h.rollout.mu.Unlock()
	st.Candidates = nil
	data, err := json.Marshal(st)
	if err == nil {
		err = h.saveSetting(context.Background(), settingRolloutLast, string(data))
	}
	if err != nil {
		h.log.Warn("saving the state of the update of the servers", "err", err)
	}
}

// setFailedTarget records the version for which a rollout failed ("" once one
// succeeded), in memory and in the database.
func (h *Hub) setFailedTarget(v string) {
	h.rollout.mu.Lock()
	h.rollout.failedTarget = v
	h.rollout.mu.Unlock()
	if err := h.saveSetting(context.Background(), settingRolloutFailed, v); err != nil {
		h.log.Warn("saving the version the servers failed to update to", "err", err)
	}
}

// loadRollout brings back what the last run of the hub left. A rollout that was
// running then ended with that run: it is shown as stopped, the servers it had
// not reached as skipped, and the ones it was waiting for as unknown, since
// their result was never seen.
func (h *Hub) loadRollout(ctx context.Context) error {
	failed, _, err := h.loadSetting(ctx, settingRolloutFailed)
	if err != nil {
		return err
	}
	raw, ok, err := h.loadSetting(ctx, settingRolloutLast)
	if err != nil {
		return err
	}
	h.rollout.mu.Lock()
	h.rollout.failedTarget = failed
	h.rollout.mu.Unlock()
	if !ok {
		return nil
	}
	var st RolloutStatus
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		h.log.Warn("the saved state of the last update of the servers cannot be read; it is ignored", "err", err)
		return nil
	}
	if st.Target != h.version {
		// A rollout to the version the hub ran before it was updated: the
		// page would show it as one to this version. Only failedTarget, which
		// names its version, is kept.
		return nil
	}
	if st.Agents == nil {
		st.Agents = []RolloutAgent{}
	}
	run := &rolloutRun{status: st, cancel: func() {}, done: closedChan()}
	if st.State == RolloutRunning {
		run.status.State = RolloutStopped
		now := time.Now().UTC()
		run.status.FinishedAt = &now
		for i := range run.status.Agents {
			switch run.status.Agents[i].Status {
			case AgentPending:
				run.status.Agents[i].Status = AgentSkipped
			case AgentOffered, AgentInstalling:
				run.status.Agents[i].Status, run.status.Agents[i].Message = AgentUnknown, msgHubRestarted
			}
		}
	}
	h.rollout.mu.Lock()
	h.rollout.run = run
	h.rollout.mu.Unlock()
	if st.State == RolloutRunning {
		h.persistRollout(run)
	}
	return nil
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}
