//go:build unix

package update

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The update helpers run with UMask=0077 (packaging/regulus-update.service):
// the executable they write must still be 0755, or the hub, which runs as the
// regulus user, cannot start it ("Permission denied", status 203/EXEC).
func TestExecutableIsWorldExecutableUnderAStrictUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(dir, "copied")
	if err := copyExecutable(src, copied); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "r.tar.gz")
	if err := os.WriteFile(archive, tarball(t, map[string]string{"regulus": "#!/bin/sh\n"}), 0o600); err != nil {
		t.Fatal(err)
	}
	extracted := filepath.Join(dir, "extracted")
	if err := extractBinary(archive, extracted); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{copied, extracted} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o755 {
			t.Errorf("%s: mode %o, want 755", filepath.Base(p), got)
		}
	}
}
