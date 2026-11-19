package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/storage"
)

// atBoot makes the fixture answer like a machine that is starting: the
// service is not active yet, and any attempt to start, stop or restart a unit
// is recorded (before the service starts nothing may be asked of systemd but
// a question: a job would wait for the unit that asks).
func (f *installFixture) atBoot() *[]string {
	var jobs []string
	run := f.in.Run
	f.in.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "systemctl" && len(args) > 0 {
			if args[0] == "is-active" {
				return nil, errors.New("inactive")
			}
			jobs = append(jobs, strings.Join(args, " "))
		}
		return run(ctx, name, args...)
	}
	return &jobs
}

func TestRecoverBeforeStart(t *testing.T) {
	for name, tc := range map[string]struct {
		onDisk      string
		wantSettled bool
		wantOut     string
		wantBin     string
		wantFail    bool
	}{
		"the new version is in place: left for the confirm step": {"0.2.0", false, "", "0.2.0", false},
		"the file is empty":       {"", true, OutcomeRolledBack, "0.1.0", true},
		"the file is garbage":     {"half a binary", true, OutcomeRolledBack, "0.1.0", true},
		"the swap never happened": {"0.1.0", true, OutcomeFailed, "0.1.0", true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newInstallFixture(t, "0.1.0", "0.2.0", nil)
			f.interrupted(t, tc.onDisk)
			jobs := f.atBoot()

			res, settled, err := f.in.RecoverBeforeStart(t.Context())
			if settled != tc.wantSettled || res.Outcome != tc.wantOut || (err != nil) != tc.wantFail {
				t.Errorf("settled = %v, result = %+v, err = %v", settled, res, err)
			}
			if got := f.installed(t); got != tc.wantBin {
				t.Errorf("the executable is %q, want %q", got, tc.wantBin)
			}
			if len(*jobs) != 0 {
				t.Errorf("systemd was asked for %q before the service starts", *jobs)
			}
			p, ok := f.pendingMarker()
			if ok == tc.wantSettled {
				t.Errorf("marker present = %v, settled = %v", ok, tc.wantSettled)
			}
			if !tc.wantSettled && len(p.BootAttempts) != 1 {
				t.Errorf("boot attempts = %v, want one", p.BootAttempts)
			}
			if r, found := ReadResult(ApplyDir(f.dataDir)); found != tc.wantSettled || (found && r.Outcome != tc.wantOut) {
				t.Errorf("result for the hub = %+v (found %v)", r, found)
			}
		})
	}
}

// A new version that takes the machine down each time it starts is given two
// starts, then the previous version is put back before it runs a third time.
func TestRecoverBeforeStartGivesUpOnAVersionThatKeepsTheMachineDown(t *testing.T) {
	f := newInstallFixture(t, "0.1.0", "0.2.0", nil)
	f.interrupted(t, "0.2.0")
	jobs := f.atBoot()
	now := f.in.Now()
	f.in.Now = func() time.Time { return now }

	for boot := 1; boot <= 2; boot++ {
		if _, settled, err := f.in.RecoverBeforeStart(t.Context()); settled || err != nil {
			t.Fatalf("start %d: settled = %v, err = %v", boot, settled, err)
		}
		now = now.Add(3 * time.Minute)
	}
	res, settled, err := f.in.RecoverBeforeStart(t.Context())
	if !settled || err == nil || res.Outcome != OutcomeRolledBack || !strings.Contains(res.Message, "not confirmed") {
		t.Fatalf("third start: settled = %v, result = %+v, err = %v", settled, res, err)
	}
	if got := f.installed(t); got != "0.1.0" {
		t.Errorf("the executable is %q", got)
	}
	if _, ok := f.pendingMarker(); ok {
		t.Error("the marker is still there")
	}
	if len(*jobs) != 0 {
		t.Errorf("systemd was asked for %q", *jobs)
	}
}

// A marker that is still there days later (the confirm step never ran) is not
// a machine that cannot stay up: the version that has been running is not
// thrown away, with its data, by counting starts.
func TestRecoverBeforeStartDoesNotRollBackAfterDays(t *testing.T) {
	f := newInstallFixture(t, "0.1.0", "0.2.0", nil)
	f.interrupted(t, "0.2.0")
	f.atBoot()
	now := f.in.Now()
	f.in.Now = func() time.Time { return now }
	for boot := 1; boot <= 5; boot++ {
		if _, settled, err := f.in.RecoverBeforeStart(t.Context()); settled || err != nil {
			t.Fatalf("start %d: settled = %v, err = %v", boot, settled, err)
		}
		now = now.Add(48 * time.Hour)
	}
	if got := f.installed(t); got != "0.2.0" {
		t.Errorf("the executable is %q", got)
	}
}

// When the service is running it is not the start of the machine: nothing is
// touched, the confirm step settles it with the service under watch.
func TestRecoverBeforeStartLeavesARunningServiceAlone(t *testing.T) {
	f := newInstallFixture(t, "0.1.0", "0.2.0", nil)
	f.interrupted(t, "") // even with a broken file
	if _, settled, err := f.in.RecoverBeforeStart(t.Context()); settled || err != nil {
		t.Fatalf("settled = %v, err = %v", settled, err)
	}
	if p, ok := f.pendingMarker(); !ok || len(p.BootAttempts) != 0 {
		t.Errorf("marker = %+v (found %v)", p, ok)
	}
	if f.restarts != 0 {
		t.Error("the service was restarted")
	}
}

// The fallback script put the previous executable back after the new version
// had already migrated the database: the database goes back too, without
// asking systemd for anything.
func TestRecoverBeforeStartPutsTheDatabaseBack(t *testing.T) {
	for name, onDisk := range map[string]string{"previous executable in place": "0.1.0", "executable damaged": ""} {
		t.Run(name, func(t *testing.T) {
			f := newInstallFixture(t, "0.1.0", "0.2.0", nil)
			open := f.withDatabase(t)
			snap := filepath.Join(f.in.StateDir, snapshotName)
			before, err := storage.SnapshotSQLite(t.Context(), f.dataDir, snap)
			if err != nil {
				t.Fatal(err)
			}
			db := open() // the new version ran
			for _, q := range []string{`INSERT INTO schema_migrations (version, name) VALUES (9999, 'from_the_future')`, `INSERT INTO keepme VALUES ('after')`} {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			_ = db.Close()
			backup := filepath.Join(f.in.StateDir, backupName)
			mustWrite(t, backup, []byte("0.1.0"))
			if err := f.in.writePending(Pending{From: "0.1.0", To: "0.2.0", Backup: backup, Snapshot: snap, SchemaBefore: before, Unit: "regulus.service", DataDir: f.dataDir}); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, f.binary, []byte(onDisk))
			jobs := f.atBoot()

			res, settled, _ := f.in.RecoverBeforeStart(t.Context())
			if !settled || res.Outcome != OutcomeRolledBack || !strings.Contains(res.Message, "database was put back") {
				t.Fatalf("settled = %v, result = %+v", settled, res)
			}
			if n := rows(t, f.dataDir); n != 1 {
				t.Errorf("rows = %d, want 1", n)
			}
			if got := f.installed(t); got != "0.1.0" {
				t.Errorf("the executable is %q", got)
			}
			if len(*jobs) != 0 {
				t.Errorf("systemd was asked for %q", *jobs)
			}
		})
	}
}

// An older version wrote the marker, a newer one reads it at the next start
// (and the other way round after a rollback): unknown fields are ignored and
// the fields of today keep their names.
func TestPendingMarkerIsReadAcrossVersions(t *testing.T) {
	f := newInstallFixture(t, "0.1.0", "0.2.0", nil)
	backup := filepath.Join(f.in.StateDir, backupName)
	mustWrite(t, backup, []byte("0.1.0"))
	marker := `{"from":"0.1.0","to":"0.2.0","backup":"` + backup + `","unit":"regulus.service","agent":false,"data_dir":"` + f.dataDir + `","started":"2026-10-26T03:00:00Z","a_field_of_a_later_version":{"x":1}}`
	mustWrite(t, f.in.pendingPath(), []byte(marker))
	p, found, err := f.in.readPending()
	if !found || err != nil || p.From != "0.1.0" || p.To != "0.2.0" || p.Backup != backup || p.Unit != "regulus.service" || p.DataDir != f.dataDir {
		t.Errorf("marker = %+v, found = %v, err = %v", p, found, err)
	}
}

func TestLockStateLetsOneHelperWorkAtATime(t *testing.T) {
	dir := t.TempDir()
	unlock, err := LockState(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := LockState(ctx, dir); err == nil {
		t.Fatal("a second helper got the lock")
	}
	unlock()
	unlock2, err := LockState(t.Context(), dir)
	if err != nil {
		t.Fatalf("after the first helper ended: %v", err)
	}
	unlock2()
	if _, err := os.Stat(filepath.Join(dir, "lock")); err != nil {
		t.Error(err)
	}
}
