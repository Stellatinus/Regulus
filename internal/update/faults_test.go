package update

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// killAt runs Apply in a goroutine that ends, without returning, the first
// time step is reached: what a kill -9 or a power cut does to the helper. The
// state on disk is whatever the helper had written by then.
func (f *installFixture) killAt(t *testing.T, step string) {
	t.Helper()
	run, probe := f.in.Run, f.in.Probe
	f.in.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if step == "restart" && name == "systemctl" && len(args) > 0 && args[0] == "restart" {
			runtime.Goexit()
		}
		return run(ctx, name, args...)
	}
	f.in.Probe = func(ctx context.Context, since time.Time) (string, error) {
		if step == "watch" {
			runtime.Goexit()
		}
		return probe(ctx, since)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = f.in.Apply(t.Context())
	}()
	<-done
	f.in.Run, f.in.Probe = run, probe
}

// The helper dies at each point after the executable was replaced, then the
// machine starts again (or systemd runs the recovery): whatever the new
// version is like, the end is one version on disk, the same one in the result
// the hub reads, and no marker. This is what the [Test] issue for the helper
// under faults checks on a real machine (#240).
func TestAHelperThatDiesIsSettledToOneConsistentState(t *testing.T) {
	for _, step := range []string{"restart", "watch"} {
		for _, healthy := range []bool{true, false} {
			for _, path := range []string{"the helper runs again", "the machine starts again"} {
				name := "killed at " + step + ", new version healthy " + map[bool]string{true: "yes", false: "no"}[healthy] + ", " + path
				t.Run(name, func(t *testing.T) {
					f := newInstallFixture(t, "0.1.0", "0.2.0", nil)
					f.healthy = func(v string) bool { return healthy || v != "0.2.0" }
					f.request(t, "0.2.0")
					f.killAt(t, step)

					if got := f.installed(t); got != "0.2.0" {
						t.Fatalf("the executable is %q after the kill, want the new version in place", got)
					}
					if _, ok := f.pendingMarker(); !ok {
						t.Fatal("the helper died after the swap without leaving a marker")
					}
					if path == "the machine starts again" {
						restore := f.in.Run
						f.atBoot()
						if _, settled, err := f.in.RecoverBeforeStart(t.Context()); settled || err != nil {
							t.Fatalf("before the start: settled = %v, err = %v", settled, err)
						}
						f.in.Run = restore
					}
					res, did, _ := f.in.Recover(t.Context())
					if !did {
						t.Fatal("the marker was not found")
					}
					want, wantOut := "0.2.0", OutcomeApplied
					if !healthy {
						want, wantOut = "0.1.0", OutcomeRolledBack
					}
					if got := f.installed(t); got != want || res.Outcome != wantOut {
						t.Errorf("executable %q, outcome %q; want %q, %q", got, res.Outcome, want, wantOut)
					}
					if r, ok := ReadResult(ApplyDir(f.dataDir)); !ok || r.Outcome != wantOut {
						t.Errorf("result for the hub = %+v (found %v)", r, ok)
					}
					if _, ok := f.pendingMarker(); ok {
						t.Error("the marker is still there")
					}
					if _, ok := ReadRequest(ApplyDir(f.dataDir)); ok {
						t.Error("the request is still there: it would start the update again")
					}
				})
			}
		}
	}
}
