package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// pendingName is the marker in the state directory (root's own, so nothing in
// it can be forged by the hub's user) that exists while an update is between
// the replacement of the executable and a confirmed outcome.
const pendingName = "pending.json"

// Pending describes an update that was started and not finished. It is written
// before the executable is replaced and removed when the update ended, applied
// or rolled back. A helper that is killed in between (out of memory, a stop,
// its time limit, a restart of the machine) leaves it behind, and Recover
// finishes the job.
type Pending struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Backup string `json:"backup"`
	// Snapshot is the copy of the SQLite database taken before the swap, and
	// SchemaBefore the schema version it has; empty and 0 when there is none
	// (an agent, or a hub on PostgreSQL).
	Snapshot     string    `json:"snapshot,omitempty"`
	SchemaBefore int       `json:"schema_before,omitempty"`
	Unit         string    `json:"unit"`
	Agent        bool      `json:"agent"`
	DataDir      string    `json:"data_dir"`
	Started      time.Time `json:"started"`
	// BootAttempts are the times the machine started with this update still
	// unsettled and the new version in place (RecoverBeforeStart adds one
	// each time). A new version that keeps taking the machine down is not
	// started for ever: see maxBootAttempts.
	BootAttempts []time.Time `json:"boot_attempts,omitempty"`
}

// The marker is written by the version that was installed and may be read, at
// the next start of the machine, by the version that replaced it: it is one of
// the contracts between versions (docs/versioning.md). Fields are only added,
// never renamed or given another meaning, and a reader ignores what it does
// not know.

const (
	// maxBootAttempts is how many starts of the machine the new version gets
	// to be confirmed after an interrupted update. When it was given this
	// many, all inside bootAttemptWindow, and the update is still unsettled,
	// the new version is taking the machine down (or never stays up long
	// enough to be judged): the previous version is put back before the
	// service starts.
	maxBootAttempts = 2
	// bootAttemptWindow separates a machine that keeps restarting from one
	// that was restarted days later with the marker still there (a confirm
	// step that never ran): the second must not lose days of data to a
	// rollback, so it is given to the confirm step again instead.
	bootAttemptWindow = time.Hour
)

func (in *Installer) pendingPath() string { return filepath.Join(in.StateDir, pendingName) }

// writePending records the update on disk before anything is replaced: the
// file and its directory are synced, so the marker survives a power cut that
// the replacement it announces also survives.
func (in *Installer) writePending(p Pending) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(in.StateDir, ".pending-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), in.pendingPath()); err != nil {
		return err
	}
	syncDir(in.StateDir, in.Log)
	return nil
}

func (in *Installer) clearPending() {
	if err := os.Remove(in.pendingPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		in.Log.Error("removing the marker of the update in progress", "err", err)
	}
	// The copy of the database holds password hashes and keys: it does not
	// outlive the update.
	_ = os.Remove(filepath.Join(in.StateDir, snapshotName))
	syncDir(in.StateDir, in.Log)
}

// syncDir flushes a directory so that a rename in it survives a power cut. A
// failure is logged, not fatal: some file systems do not support it.
func syncDir(dir string, log *slog.Logger) {
	d, err := os.Open(dir)
	if err != nil {
		log.Warn("syncing a directory", "dir", dir, "err", err)
		return
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		log.Warn("syncing a directory", "dir", dir, "err", err)
	}
}

// Recover finishes an update that an earlier run left unfinished. It does
// nothing, and returns false, when there is no marker. Otherwise it settles
// what is on disk and what is running:
//
//   - the executable is the new version: the service is restarted and watched
//     as in a normal update; healthy means the update stands, otherwise the
//     previous version is put back;
//   - anything else (the previous version still there, a missing or damaged
//     file): the previous version is put back from the copy kept for that.
//
// The marker is always removed at the end, even when putting the previous
// version back failed: a marker that stays would start the helper again and
// again. A failure is in the result and in the exit status.
func (in *Installer) Recover(ctx context.Context) (ApplyResult, bool, error) {
	in.defaults()
	p, found, perr := in.readPending()
	if !found {
		return ApplyResult{}, false, nil
	}
	if perr != nil {
		return ApplyResult{Outcome: OutcomeFailed, Message: perr.Error(), At: in.Now().UTC()}, true, perr
	}
	dir := ApplyDir(p.DataDir)
	res := ApplyResult{Version: p.To, From: p.From, At: in.Now().UTC()}
	finish := func(outcome string, err error) (ApplyResult, bool, error) {
		in.clearPending()
		res.Outcome, res.At = outcome, in.Now().UTC()
		if err != nil {
			res.setMessage(err)
		}
		if p.DataDir != "" {
			if werr := WriteResult(dir, res); werr != nil {
				in.Log.Error("writing the result", "err", werr)
			}
		}
		in.Log.Info("interrupted update settled", "outcome", outcome, "from", p.From, "to", p.To, "err", err)
		return res, true, err
	}

	in.Log.Warn("an update was interrupted; settling it", "from", p.From, "to", p.To)
	if v, verr := in.installedVersion(ctx); verr == nil && v.String() == p.To {
		werr := in.restartAndWatch(ctx, p.To)
		if werr == nil {
			res.Message, res.Code = "the helper was interrupted; the new version was checked afterwards and stands", ApplyInterruptedStands
			return finish(OutcomeApplied, nil)
		}
		in.Log.Error("the interrupted update is not healthy; rolling back", "err", werr)
	} else if verr == nil && v.String() == p.From {
		// The swap never happened (or was undone): nothing to put back.
		return finish(OutcomeFailed, coded(fmt.Errorf("the helper was interrupted before the new version was installed; %s is still in place", p.From), ApplyInterruptedNotInstalled, "version", p.From))
	}
	rctx, cancel := in.rollbackContext(ctx)
	defer cancel()
	note, rerr := in.rollback(rctx, p.Backup, p.From, p.Snapshot, p.SchemaBefore)
	if rerr != nil {
		return finish(OutcomeFailed, coded(fmt.Errorf("the helper was interrupted and putting %s back failed: %v", p.From, rerr), ApplyInterruptedRollbackFails, "version", p.From, "error", rerr.Error()))
	}
	return finish(OutcomeRolledBack, coded(fmt.Errorf("the helper was interrupted; version %s was put back%s", p.From, note), ApplyInterruptedRolledBack, "version", p.From, "note", note))
}

// readPending reads the marker. found is false when there is none. A marker
// that cannot be used (unreadable, incomplete, or naming files outside the
// state directory) is removed and reported as an error: it must not start the
// helper again and again, and it names the file that is copied over the
// executable. On success the role the marker records is taken over.
func (in *Installer) readPending() (p Pending, found bool, err error) {
	data, err := readRegular(in.pendingPath(), maxControlFile)
	if errors.Is(err, os.ErrNotExist) {
		return Pending{}, false, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &p)
	}
	if err != nil || p.To == "" || p.From == "" || p.Backup == "" {
		in.clearPending()
		return Pending{}, true, errors.New("the marker of an interrupted update could not be read: it was removed")
	}
	if filepath.Dir(p.Backup) != filepath.Clean(in.StateDir) || (p.Snapshot != "" && filepath.Dir(p.Snapshot) != filepath.Clean(in.StateDir)) {
		in.clearPending()
		return Pending{}, true, errors.New("the marker of an interrupted update names a file outside the state directory: it was removed")
	}
	// What the marker says about the role is what the interrupted run was.
	in.Unit, in.Agent, in.DataDir = p.Unit, p.Agent, p.DataDir
	return p, true, nil
}

// RecoverBeforeStart is the first half of settling an interrupted update when
// the machine starts (regulus-update-recover.service, ordered before the
// service). It runs while the service is down and starts nothing itself, so
// it can only do what needs no running service:
//
//   - the executable is the previous version: the new one was never installed
//     (or the fallback script put the copy back); the database is put back if
//     the new version had already changed it, and the update is over;
//   - the executable cannot say its version, or says another one: the
//     previous version is put back, database included;
//   - the executable is the new version: it was complete on disk before it
//     replaced the old one, but nobody watched it start. The attempt is
//     counted in the marker and the marker stays: the service starts next and
//     the confirm step (Recover, from regulus-update-confirm.service) watches
//     it and rolls back if it is not healthy. A new version that already had
//     maxBootAttempts starts of the machine inside bootAttemptWindow is not
//     given another: it is put back here, before it runs again.
//
// settled is false when the marker was left for the confirm step, or when
// there was none. When the service is running this is not the start of the
// machine and nothing is done: the confirm step, or the next run of the
// helper, settles it with the service under watch.
func (in *Installer) RecoverBeforeStart(ctx context.Context) (res ApplyResult, settled bool, err error) {
	in.defaults()
	p, found, perr := in.readPending()
	if !found {
		return ApplyResult{}, false, nil
	}
	if perr != nil {
		return ApplyResult{Outcome: OutcomeFailed, Message: perr.Error(), At: in.Now().UTC()}, true, perr
	}
	if _, aerr := in.Run(ctx, "systemctl", "is-active", "--quiet", p.Unit); aerr == nil {
		in.Log.Warn("an update is unsettled but the service is running; leaving it to the confirm step", "unit", p.Unit)
		return ApplyResult{}, false, nil
	}
	res = ApplyResult{Version: p.To, From: p.From, At: in.Now().UTC()}
	finish := func(outcome string, err error) (ApplyResult, bool, error) {
		in.clearPending()
		res.Outcome, res.At = outcome, in.Now().UTC()
		if err != nil {
			res.setMessage(err)
		}
		if p.DataDir != "" {
			if werr := WriteResult(ApplyDir(p.DataDir), res); werr != nil {
				in.Log.Error("writing the result", "err", werr)
			}
		}
		in.Log.Info("interrupted update settled before the service starts", "outcome", outcome, "from", p.From, "to", p.To, "err", err)
		return res, true, err
	}
	putBack := func(why string) (ApplyResult, bool, error) {
		note, rerr := in.putBack(ctx, p.Backup, p.From, p.Snapshot, p.SchemaBefore, false)
		if rerr != nil {
			return finish(OutcomeFailed, fmt.Errorf("%s and putting %s back failed: %v", why, p.From, rerr))
		}
		return finish(OutcomeRolledBack, fmt.Errorf("%s; version %s was put back%s", why, p.From, note))
	}

	in.Log.Warn("the machine started with an update unsettled", "from", p.From, "to", p.To, "boot_attempts", len(p.BootAttempts))
	v, verr := in.installedVersion(ctx)
	switch {
	case verr == nil && v.String() == p.From:
		// Only the database can still be the new version's.
		note := ""
		if p.Snapshot != "" {
			var derr error
			if note, derr = in.restoreDatabase(ctx, p.Snapshot, p.SchemaBefore, false); derr != nil {
				return finish(OutcomeFailed, fmt.Errorf("the update was interrupted, %s is in place, but %v", p.From, derr))
			}
		}
		if note != "" {
			return finish(OutcomeRolledBack, fmt.Errorf("the update was interrupted; version %s is in place%s", p.From, note))
		}
		return finish(OutcomeFailed, fmt.Errorf("the update was interrupted before the new version was installed; %s is still in place", p.From))
	case verr == nil && v.String() == p.To:
		now := in.Now().UTC()
		if n := len(p.BootAttempts); n >= maxBootAttempts {
			// A clock that went backwards counts as "just now": the case this
			// guards against is a machine that cannot stay up.
			if since := now.Sub(p.BootAttempts[n-maxBootAttempts]); since < bootAttemptWindow {
				return putBack(fmt.Sprintf("version %s was not confirmed after the machine started %d times with it", p.To, n))
			}
		}
		p.BootAttempts = append(p.BootAttempts, now)
		if werr := in.writePending(p); werr != nil {
			// Not being able to count is not a reason to undo an update that
			// may be fine: the confirm step still judges it.
			in.Log.Error("counting the start in the marker", "err", werr)
		}
		return res, false, nil
	}
	return putBack("the update was interrupted and the executable left in place cannot be used")
}

// LockState takes the lock of the state directory, so that two helpers (an
// update that was asked for and the confirm step after a start of the
// machine) never replace the executable or restart the service at the same
// time. It waits for the other to finish, as long as ctx allows. The lock goes
// away with the process, so a helper that is killed cannot leave it behind.
func LockState(ctx context.Context, stateDir string) (unlock func(), err error) {
	f, err := os.OpenFile(filepath.Join(stateDir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			_ = f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("another update helper is still running: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}
