package hub

import "errors"

// lockFileName is the file in the data directory that a running hub holds
// locked (flock), so that one data directory has one hub. It is never
// removed: removing a lock file while another process opens it would let two
// processes hold "the" lock on two different files.
const lockFileName = "hub.lock"

// ErrAlreadyRunning: another hub holds the lock of the data directory.
var ErrAlreadyRunning = errors.New("another hub is already running with this data directory")
