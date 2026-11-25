package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// The update helpers up to 0.3.0-beta.1 wrote the new executable with mode
// 0700 (their UMask=0077 cut the 0755 they asked for, #448), so the hub and the
// agent, which run as the regulus user, could not start it. An update is
// installed by the helper of the version already in place, so an update from
// such a version would make the same mistake again. Those helpers do run the
// new executable as root before they put it in place (`regulus version` on
// /usr/bin/.regulus.update) and after (`regulus version` on /usr/bin/regulus):
// at that moment the new version gives its own file the right mode. It also
// does it whenever root runs it for anything else.

// execNames are the names the executable has in /usr/bin during and after an
// update: the installed file, the candidate of the helper and the copy of a
// rollback.
var execNames = map[string]bool{"regulus": true, ".regulus.update": true, ".regulus.rollback": true}

// repairExecMode sets mode 0755 on the running executable when it is one of
// the files above, owned by root, and the service user cannot run it. It does
// nothing otherwise and never reports an error: the command goes on either way.
func repairExecMode() {
	if os.Geteuid() != 0 {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	_ = fixExecMode(exe, "/usr/bin")
}

// fixExecMode is repairExecMode on a given path, for tests; dir is where the
// executable must be. It reports whether the mode was changed.
func fixExecMode(exe, dir string) bool {
	if filepath.Dir(exe) != dir || !execNames[filepath.Base(exe)] {
		return false
	}
	fi, err := os.Lstat(exe)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || st.Uid != uint32(os.Geteuid()) {
		return false
	}
	if fi.Mode().Perm()&0o055 == 0o055 {
		return false
	}
	return os.Chmod(exe, 0o755) == nil
}
