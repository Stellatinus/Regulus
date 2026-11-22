package update

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type verifierFunc func(ctx context.Context, tag string, data, bundle []byte) error

func (f verifierFunc) Verify(ctx context.Context, tag string, data, bundle []byte) error {
	return f(ctx, tag, data, bundle)
}

// signedBy accepts a bundle that says "signed by <tag>" for exactly that tag,
// like the real verifier ties the signature to the release workflow at a tag.
func signedBy() Verifier {
	return verifierFunc(func(_ context.Context, tag string, _, bundle []byte) error {
		if string(bundle) != "signed by "+tag {
			return errors.New("bad signature: " + string(bundle))
		}
		return nil
	})
}

func indexJSON(t *testing.T, tag string, issued, expires time.Time, channels map[Channel]string) []byte {
	t.Helper()
	b, err := json.Marshal(Index{Tag: tag, IssuedAt: issued, Expires: expires, Channels: channels})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// withIndex adds a signed index to a fake release.
func withIndex(f *fakeRelease, data []byte, signer string) {
	f.files[IndexName] = data
	f.files[IndexBundleName] = []byte("signed by " + signer)
}

type indexRig struct {
	*rig
	rel *fakeRelease
	now time.Time
}

func newIndexRig(t *testing.T, current, offered string) *indexRig {
	t.Helper()
	r := newRig(t, current, offered, func(o *Options) { o.Verifier = signedBy() })
	return &indexRig{rig: r, rel: r.rel, now: *r.clock}
}

func (r *indexRig) check(t *testing.T) Status {
	t.Helper()
	*r.clock = r.clock.Add(time.Hour) // past the minimum gap between checks
	st, err := r.m.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestIndexIsTakenFromTheNewestReleaseThatHasOne(t *testing.T) {
	r := newIndexRig(t, "0.1.0", "0.2.0")
	withIndex(r.rel, indexJSON(t, "v0.2.0", r.now, r.now.Add(14*24*time.Hour), map[Channel]string{Stable: "v0.2.0"}), "v0.2.0")
	r.src.set([]Release{r.rel.release()}, nil)
	st := r.check(t)
	if st.IndexProblem != "" || st.Index == nil || st.Index.Tag != "v0.2.0" {
		t.Errorf("a good index: %+v / %q", st.Index, st.IndexProblem)
	}
	if !st.UpdateAvailable {
		t.Error("the update was not found")
	}
}

// Releases made before the index existed carry none: nothing is said.
func TestNoIndexAtAllIsNotAProblemUntilOneWasSeen(t *testing.T) {
	r := newIndexRig(t, "0.1.0", "0.2.0")
	if st := r.check(t); st.IndexProblem != "" || st.Index != nil {
		t.Errorf("no index yet: %+v / %q", st.Index, st.IndexProblem)
	}
	withIndex(r.rel, indexJSON(t, "v0.2.0", r.now, r.now.Add(14*24*time.Hour), map[Channel]string{Stable: "v0.2.0"}), "v0.2.0")
	r.src.set([]Release{r.rel.release()}, nil)
	if st := r.check(t); st.Index == nil {
		t.Fatal("the index was not read")
	}
	// A mirror that now strips it is noticed.
	delete(r.rel.files, IndexName)
	r.src.set([]Release{r.rel.release()}, nil)
	if st := r.check(t); st.IndexProblem != IndexMissing {
		t.Errorf("a stripped index: %q", st.IndexProblem)
	}
}

// The freeze attack: the list leaves out a release that the signed index names.
func TestAHiddenReleaseIsNoticed(t *testing.T) {
	r := newIndexRig(t, "0.1.0", "0.2.0")
	withIndex(r.rel, indexJSON(t, "v0.2.0", r.now, r.now.Add(14*24*time.Hour), map[Channel]string{Stable: "v0.3.0"}), "v0.2.0")
	r.src.set([]Release{r.rel.release()}, nil)
	st := r.check(t)
	if st.IndexProblem != IndexHidden || st.IndexDetail != "0.3.0" {
		t.Errorf("problem = %q (%q)", st.IndexProblem, st.IndexDetail)
	}
	// A pre-release the hub's channel does not follow is not "hidden".
	withIndex(r.rel, indexJSON(t, "v0.2.0", r.now.Add(time.Minute), r.now.Add(14*24*time.Hour), map[Channel]string{Stable: "v0.2.0", Alpha: "v0.3.0-alpha.1"}), "v0.2.0")
	r.src.set([]Release{r.rel.release()}, nil)
	if st := r.check(t); st.IndexProblem != "" {
		t.Errorf("a stable hub learns of an alpha: %q", st.IndexProblem)
	}
}

// An index that has run out warns, at the check and later without one.
func TestAnExpiredIndexWarns(t *testing.T) {
	r := newIndexRig(t, "0.1.0", "0.2.0")
	withIndex(r.rel, indexJSON(t, "v0.2.0", r.now, r.now.Add(48*time.Hour), map[Channel]string{Stable: "v0.2.0"}), "v0.2.0")
	r.src.set([]Release{r.rel.release()}, nil)
	if st := r.check(t); st.IndexProblem != "" {
		t.Fatalf("fresh index: %q", st.IndexProblem)
	}
	*r.clock = r.clock.Add(72 * time.Hour)
	if st := r.status(t); st.IndexProblem != IndexExpired {
		t.Errorf("an index that ran out since the last check: %q", st.IndexProblem)
	}
	if st := r.check(t); st.IndexProblem != IndexExpired {
		t.Errorf("an expired index at a check: %q", st.IndexProblem)
	}
}

// A hub never goes back to an older index than the last it verified.
func TestAReplayedIndexIsRefused(t *testing.T) {
	r := newIndexRig(t, "0.1.0", "0.2.0")
	fresh := indexJSON(t, "v0.2.0", r.now.Add(48*time.Hour), r.now.Add(10*24*time.Hour), map[Channel]string{Stable: "v0.2.0"})
	withIndex(r.rel, fresh, "v0.2.0")
	r.src.set([]Release{r.rel.release()}, nil)
	if st := r.check(t); st.Index == nil || st.IndexProblem != "" {
		t.Fatalf("fresh: %+v / %q", st.Index, st.IndexProblem)
	}
	// An older, still unexpired and correctly signed index comes back.
	withIndex(r.rel, indexJSON(t, "v0.2.0", r.now, r.now.Add(14*24*time.Hour), map[Channel]string{Stable: "v0.2.0"}), "v0.2.0")
	r.src.set([]Release{r.rel.release()}, nil)
	st := r.check(t)
	if st.IndexProblem != IndexReplayed || st.Index != nil {
		t.Errorf("a replayed index: %+v / %q", st.Index, st.IndexProblem)
	}
	// The fresh one still works afterwards: the mark did not move back.
	withIndex(r.rel, fresh, "v0.2.0")
	r.src.set([]Release{r.rel.release()}, nil)
	if st := r.check(t); st.IndexProblem != "" || st.Index == nil {
		t.Errorf("the fresh index again: %q", st.IndexProblem)
	}
}

func TestAnIndexThatCannotBeTrustedIsRefused(t *testing.T) {
	good := func(r *indexRig) []byte {
		return indexJSON(t, "v0.2.0", r.now, r.now.Add(14*24*time.Hour), map[Channel]string{Stable: "v0.2.0"})
	}
	for name, tc := range map[string]struct {
		make func(r *indexRig)
		want string
	}{
		"signed for another tag": {func(r *indexRig) { withIndex(r.rel, good(r), "v0.9.0") }, "bad signature"},
		"attached to another release": {func(r *indexRig) {
			withIndex(r.rel, indexJSON(t, "v0.1.0", r.now, r.now.Add(time.Hour), map[Channel]string{Stable: "v0.1.0"}), "v0.2.0")
		}, "belongs to v0.1.0"},
		"not JSON": {func(r *indexRig) { withIndex(r.rel, []byte("{"), "v0.2.0") }, "not valid JSON"},
		"never expires": {func(r *indexRig) {
			withIndex(r.rel, indexJSON(t, "v0.2.0", r.now, r.now.Add(3650*24*time.Hour), map[Channel]string{Stable: "v0.2.0"}), "v0.2.0")
		}, "longer than"},
		"wrong channel": {func(r *indexRig) {
			withIndex(r.rel, indexJSON(t, "v0.2.0", r.now, r.now.Add(time.Hour), map[Channel]string{Stable: "v0.3.0-beta.1"}), "v0.2.0")
		}, "newest of channel"},
		"missing bundle": {func(r *indexRig) {
			withIndex(r.rel, good(r), "v0.2.0")
			r.rel.files[IndexBundleName] = nil
			delete(r.rel.files, IndexBundleName)
		}, ""},
	} {
		r := newIndexRig(t, "0.1.0", "0.2.0")
		tc.make(r)
		r.src.set([]Release{r.rel.release()}, nil)
		st := r.check(t)
		if tc.want == "" {
			if st.IndexProblem != "" {
				t.Errorf("%s: %q", name, st.IndexProblem)
			}
			continue
		}
		if st.IndexProblem != IndexInvalid || !strings.Contains(st.IndexDetail, tc.want) || st.Index != nil {
			t.Errorf("%s: problem %q detail %q", name, st.IndexProblem, st.IndexDetail)
		}
		// The release list itself still works: the update is offered.
		if !st.UpdateAvailable {
			t.Errorf("%s: the release was not found", name)
		}
	}
}

func TestChecksWithoutAVerifierIgnoreTheIndex(t *testing.T) {
	r := newRig(t, "0.1.0", "0.2.0")
	withIndex(r.rel, []byte("garbage"), "nobody")
	r.src.set([]Release{r.rel.release()}, nil)
	*r.clock = r.clock.Add(time.Hour)
	st, err := r.m.Check(t.Context())
	if err != nil || st.IndexProblem != "" || st.Index != nil {
		t.Errorf("%+v, %v", st, err)
	}
}
