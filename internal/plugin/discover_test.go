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
