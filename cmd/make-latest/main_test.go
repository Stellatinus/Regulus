package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/update"
)

func TestMakeLatest(t *testing.T) {
	dir := t.TempDir()
	releases := filepath.Join(dir, "releases.json")
	// Two pages as `gh api --paginate` prints them, a plugin release, a draft and a
	// release whose flag disagrees with its tag.
	pages := `[{"tag_name":"v1.4.0","prerelease":false},{"tag_name":"plugin/docker/v9.0.0","prerelease":false},{"tag_name":"v1.5.0-beta.1","prerelease":true},{"tag_name":"v1.9.0","draft":true}]
[{"tag_name":"v1.3.0","prerelease":false},{"tag_name":"v1.5.0-alpha.2","prerelease":true},{"tag_name":"v2.0.0","prerelease":true}]`
	if err := os.WriteFile(releases, []byte(pages), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "latest.json")
	now := time.Date(2026, 11, 22, 12, 0, 0, 500, time.UTC)
	if err := run(releases, "v1.4.1", 14*24*time.Hour, out, now); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	ix, err := update.ParseIndex(data)
	if err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	if ix.Tag != "v1.4.1" || !ix.IssuedAt.Equal(now.Truncate(time.Second)) || ix.Expires.Sub(ix.IssuedAt) != 14*24*time.Hour {
		t.Errorf("index = %+v", ix)
	}
	if ix.Channels[update.Stable] != "v1.4.1" || ix.Channels[update.Beta] != "v1.5.0-beta.1" || ix.Channels[update.Alpha] != "v1.5.0-alpha.2" {
		// v2.0.0 is flagged as a pre-release but has no pre-release tag: it is skipped.
		t.Errorf("channels = %+v", ix.Channels)
	}
}

func TestMakeLatestRefusesWhatItCannotUse(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "r.json")
	_ = os.WriteFile(f, []byte(`[]`), 0o600)
	for name, tc := range map[string]struct {
		tag  string
		life time.Duration
		want string
	}{
		"not a tag":  {"main", time.Hour, "not a release tag"},
		"no life":    {"v1.0.0", 0, "valid for"},
		"too long":   {"v1.0.0", 1000 * 24 * time.Hour, "valid for"},
		"unreadable": {"v1.0.0", time.Hour, ""},
	} {
		file := f
		if name == "unreadable" {
			file = filepath.Join(dir, "missing.json")
		}
		err := run(file, tc.tag, tc.life, filepath.Join(dir, "o.json"), time.Now())
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := run(f, "v1.0.0", time.Hour, filepath.Join(dir, "o.json"), time.Now()); err != nil {
		t.Errorf("an empty list is fine for the first release: %v", err)
	}
}
