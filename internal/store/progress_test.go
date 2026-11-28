package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProgressOfAnInstallEndsDoneAndTheHelperSaidItsSteps(t *testing.T) {
	e := newEnv2(t, "0.1.0")
	if _, ok := e.inst.Progress(); ok {
		t.Fatal("there is progress before anything ran")
	}
	if _, err := e.do(func() (Outcome, error) { return e.inst.Install(context.Background(), "ann", "disks", "", "") }); err != nil {
		t.Fatal(err)
	}
	p, ok := e.inst.Progress()
	if !ok || p.State != ProgressDone || p.Plugin != "disks" || p.Action != "install" || p.Version != "0.1.0" {
		t.Fatalf("progress = %+v %v", p, ok)
	}
	// The helper left the last step it was in, for the request it served.
	hp, ok := ReadProgress(ApplyDir(e.data))
	if !ok || hp.Step != StepInstalling || hp.RequestID == "" || hp.Plugin != "disks" {
		t.Fatalf("helper progress = %+v %v", hp, ok)
	}
}

func TestProgressOfAnUpdateIsAnUpdateAndOfARemovalIsARemoval(t *testing.T) {
	e := newEnv2(t, "0.1.0")
	if _, err := e.do(func() (Outcome, error) { return e.inst.Install(context.Background(), "ann", "disks", "", "") }); err != nil {
		t.Fatal(err)
	}
	e.pkgs["0.2.0"] = uiOnly(t, "disks", "0.2.0")
	if _, err := e.do(func() (Outcome, error) { return e.inst.Update(context.Background(), "ann", "disks", "") }); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.inst.Progress(); p.Action != "update" || p.Version != "0.2.0" || p.State != ProgressDone {
		t.Fatalf("progress of the update = %+v", p)
	}
	if _, err := e.do(func() (Outcome, error) { return e.inst.Remove(context.Background(), "ann", "disks") }); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.inst.Progress(); p.Action != "remove" || p.State != ProgressDone {
		t.Fatalf("progress of the removal = %+v", p)
	}
	if hp, _ := ReadProgress(ApplyDir(e.data)); hp.Step != StepRemoving {
		t.Fatalf("helper step = %q", hp.Step)
	}
}

func TestProgressOfARefusedInstallEndsFailedWithTheCodes(t *testing.T) {
	e := newEnv2(t, "0.1.0")
	e.app.Verifier = &fakeVerifier{err: errors.New("bad signature")} // the hub accepts the index, the helper does not
	_, err := e.do(func() (Outcome, error) { return e.inst.Install(context.Background(), "ann", "disks", "", "") })
	if code(err) != "helper_failed/not_verified" {
		t.Fatalf("err = %v", err)
	}
	p, ok := e.inst.Progress()
	if !ok || p.State != ProgressFailed || p.Code != "helper_failed" || p.Helper != "not_verified" || p.Message == "" || p.Step != "" {
		t.Fatalf("progress = %+v %v", p, ok)
	}
}

func TestProgressShowsTheDownloadPercentWhileTheSizeIsKnown(t *testing.T) {
	var seen []int
	w := &countWriter{total: 200, report: func(p int) { seen = append(seen, p) }}
	for i := 0; i < 4; i++ {
		_, _ = w.Write(make([]byte, 50))
	}
	if len(seen) != 4 || seen[0] != 25 || seen[3] != 100 {
		t.Fatalf("percentages = %v", seen)
	}
	// A write that changes nothing is not reported again, and a download that
	// runs past the size stays at 100.
	_, _ = w.Write(make([]byte, 1))
	if len(seen) != 4 {
		t.Fatalf("reported again at 100: %v", seen)
	}
}

func TestProgressOfAFinishedOperationIsKeptForAMinute(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	tr := &tracker{now: func() time.Time { return now }}
	if _, ok := tr.get(); ok {
		t.Fatal("progress with nothing begun")
	}
	tr.begin("disks", "install", "1.0.0")
	tr.step(StepDownloading, 40)
	if p, _ := tr.get(); p.Step != StepDownloading || p.Percent != 40 || p.State != ProgressRunning {
		t.Fatalf("running = %+v", p)
	}
	now = now.Add(10 * time.Minute) // a running operation is never dropped
	if _, ok := tr.get(); !ok {
		t.Fatal("a running operation disappeared")
	}
	tr.finish(nil)
	now = now.Add(30 * time.Second)
	if p, ok := tr.get(); !ok || p.State != ProgressDone {
		t.Fatalf("finished = %+v %v", p, ok)
	}
	// A step after the end changes nothing.
	tr.step(StepVerifying, -1)
	if p, _ := tr.get(); p.State != ProgressDone || p.Step != "" {
		t.Fatalf("a step moved a finished operation: %+v", p)
	}
	now = now.Add(2 * time.Minute)
	if _, ok := tr.get(); ok {
		t.Fatal("the finished operation was kept too long")
	}
}
