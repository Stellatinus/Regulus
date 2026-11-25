package hub

import "errors"

// LockDataDir takes the instance lock of a data directory, as a hub does, for
// `regulus restore`: it fails with ErrAlreadyRunning while a hub runs on it.
// Call the returned function to release it.
func LockDataDir(dir string) (release func(), err error) { return lockDataDir(dir) }

// lockFileName is the file in the data directory that a running hub holds
// locked (flock), so that one data directory has one hub. It is never
// removed: removing a lock file while another process opens it would let two
// processes hold "the" lock on two different files.
const lockFileName = "hub.lock"

// ErrAlreadyRunning: another hub holds the lock of the data directory.
var ErrAlreadyRunning = errors.New("another hub is already running with this data directory")
