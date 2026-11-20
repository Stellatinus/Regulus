//go:build unix

package hub

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// lockDataDir takes the exclusive lock of the data directory, creating the
// directory if needed, and returns the function that releases it. It does not
// wait: a second hub on the same directory fails at once with
// ErrAlreadyRunning and the process id the first one wrote into the file. The
// kernel drops the lock when the process ends, however it ends, so a hub that
// was killed leaves nothing to clean up.
func lockDataDir(dir string) (release func(), err error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dir, lockFileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			holder := ""
			if b, rerr := os.ReadFile(path); rerr == nil {
				if pid := strings.TrimSpace(string(b)); pid != "" {
					holder = " (process " + pid + ")"
				}
			}
			return nil, fmt.Errorf("%w%s: %s is locked; stop that hub first, or give this one another data_dir", ErrAlreadyRunning, holder, path)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	// The process id is for the message above and for people; the lock is
	// what counts.
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
