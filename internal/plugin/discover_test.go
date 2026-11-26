package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, filepath.Join(root, "docker"), goodManifest)
	writeManifest(t, filepath.Join(root, "renamed"), goodManifest) // directory not named after the id
	writeManifest(t, filepath.Join(root, "broken"), "id: broken\n")
	_ = os.MkdirAll(filepath.Join(root, "empty"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, ".hidden"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "README"), []byte("not a plugin"), 0o644)
	_ = os.Symlink(filepath.Join(root, "docker"), filepath.Join(root, "link"))

	found, problems, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Manifest.ID != "docker" || found[0].Dir != filepath.Join(root, "docker") {
		t.Errorf("found = %+v", found)
	}
	got := map[string]string{}
	for _, p := range problems {
		got[filepath.Base(p.Dir)] = p.Err.Error()
	}
	for dir, want := range map[string]string{"renamed": "named after", "broken": "", "empty": "no such file", "link": "symbolic link"} {
		msg, ok := got[dir]
		if !ok || !strings.Contains(msg, want) {
			t.Errorf("%s: problem %q (reported %v), want %q", dir, msg, ok, want)
		}
	}
	if len(problems) != 4 {
		t.Errorf("problems = %v", problems)
	}
}

func TestDiscoverWithoutADirectory(t *testing.T) {
	found, problems, err := Discover(filepath.Join(t.TempDir(), "none"))
	if err != nil || found != nil || problems != nil {
		t.Errorf("%v %v %v", found, problems, err)
	}
}

// The layout the store creates: <id>/<version>/ and <id>/current -> <version>.
func TestDiscoverFollowsCurrentOfTheStoreLayout(t *testing.T) {
	root := t.TempDir()
	good := strings.ReplaceAll(goodManifest, "version: 0.3.1", "version: 0.3.1")
	pdir := filepath.Join(root, "docker")
	writeManifest(t, filepath.Join(pdir, "0.3.1"), good)
	writeManifest(t, filepath.Join(pdir, "0.2.0"), strings.ReplaceAll(good, "0.3.1", "0.2.0"))
	if err := os.Symlink("0.3.1", filepath.Join(pdir, CurrentName)); err != nil {
		t.Fatal(err)
	}
	found, problems, err := Discover(root)
	if err != nil || len(problems) != 0 || len(found) != 1 {
		t.Fatalf("%+v %v %v", found, problems, err)
	}
	if f := found[0]; !f.Versioned || f.Dir != filepath.Join(pdir, "0.3.1") || f.Manifest.Version != "0.3.1" {
		t.Fatalf("found = %+v", f)
	}
	if err := WriteSourceRecord(pdir, SourceRecord{Source: "regulus-plugins", Version: "0.3.1"}); err != nil {
		t.Fatal(err)
	}
	if in := ListInstalled(root); len(in) != 1 || in[0].Source != "regulus-plugins" || in[0].Version != "0.3.1" {
		t.Fatalf("installed = %+v", in)
	}
	// A record for another version says nothing about the one that runs.
	_ = WriteSourceRecord(pdir, SourceRecord{Source: "regulus-plugins", Version: "0.2.0"})
	if in := ListInstalled(root); in[0].Source != "" {
		t.Fatalf("a stale record was used: %+v", in)
	}
}

func TestDiscoverRefusesACurrentThatLeadsAway(t *testing.T) {
	outside := t.TempDir()
	writeManifest(t, outside, goodManifest)
	for name, target := range map[string]string{
		"absolute":   outside,
		"parent":     "../elsewhere",
		"nested":     "0.3.1/sub",
		"dot":        ".",
		"hidden":     ".git",
		"missing":    "9.9.9",
		"to a file":  "plugin-file",
		"empty name": "",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			pdir := filepath.Join(root, "docker")
			writeManifest(t, filepath.Join(pdir, "0.3.1"), goodManifest)
			_ = os.WriteFile(filepath.Join(pdir, "plugin-file"), []byte("x"), 0o644)
			if target == "" {
				if err := os.WriteFile(filepath.Join(pdir, CurrentName), nil, 0o644); err != nil { // not a link at all
					t.Fatal(err)
				}
			} else if err := os.Symlink(target, filepath.Join(pdir, CurrentName)); err != nil {
				t.Fatal(err)
			}
			found, problems, err := Discover(root)
			if err != nil || len(found) != 0 || len(problems) != 1 {
				t.Fatalf("found %+v, problems %v, err %v", found, problems, err)
			}
		})
	}
}

func TestSourceRecordIsBounded(t *testing.T) {
	dir := t.TempDir()
	if _, ok := ReadSourceRecord(dir); ok {
		t.Fatal("a missing record was read")
	}
	_ = os.WriteFile(filepath.Join(dir, SourceFileName), []byte(strings.Repeat("x", maxSourceRecord+1)), 0o644)
	if _, ok := ReadSourceRecord(dir); ok {
		t.Fatal("an oversized record was read")
	}
	other := filepath.Join(t.TempDir(), "target")
	_ = os.WriteFile(other, []byte(`{"source":"x","version":"1.0.0"}`), 0o644)
	_ = os.Remove(filepath.Join(dir, SourceFileName))
	_ = os.Symlink(other, filepath.Join(dir, SourceFileName))
	if _, ok := ReadSourceRecord(dir); ok {
		t.Fatal("a link was followed")
	}
}
