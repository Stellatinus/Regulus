//go:build !unix

package hub

import (
	"fmt"
	"os"
)

// lockDataDir only creates the data directory on systems without flock. The
// hub is released for Linux only (see docs/decisions.md, "Platforms"); a
// build for another system does not protect against a second hub.
func lockDataDir(dir string) (release func(), err error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	return func() {}, nil
}
