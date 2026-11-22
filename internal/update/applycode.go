package update

import "errors"

// The codes of the messages the installer writes in result.json (#368). The
// dashboard has a text for each in `updates.applyMsg.<code>` (English and
// Italian); a test there fails when a code has none. Arguments are in the
// comments. A failure the list does not cover has no code and keeps its
// English Message.
const (
	ApplyRequestUnreadable        = "request_unreadable"        //
	ApplyInvalidVersion           = "invalid_version"           // error
	ApplyNotNewer                 = "not_newer"                 // version, installed
	ApplyRolledBack               = "rolled_back"               // error, version, note
	ApplyRollbackFailed           = "rollback_failed"           // error, version, rollback_error
	ApplyInterruptedStands        = "interrupted_stands"        //
	ApplyInterruptedNotInstalled  = "interrupted_not_installed" // version
	ApplyInterruptedRolledBack    = "interrupted_rolled_back"   // version, note
	ApplyInterruptedRollbackFails = "interrupted_rollback_fail" // version, error

	// The steps of an installation that can fail with the text of a system
	// error (#372). Each takes that text as error: it comes from the system
	// (a file, a signature check, systemctl) in English and is shown as it
	// is, see docs/decisions.md ("GUI translations").
	ApplyVersionUnreadable   = "version_unreadable"     // error: asking the installed binary
	ApplyWorkDirFailed       = "work_dir_failed"        // error
	ApplyFetchFailed         = "fetch_failed"           // error: copy, signature or checksum
	ApplyExtractFailed       = "extract_failed"         // error
	ApplyNewBinaryMismatch   = "new_binary_mismatch"    // version, got
	ApplyBackupFailed        = "backup_failed"          // error
	ApplyDatabaseCopyFailed  = "database_copy_failed"   // error
	ApplyPendingWriteFailed  = "pending_write_failed"   // error
	ApplySwapFailed          = "swap_failed"            // error
	ApplyMarkerUnreadable    = "marker_unreadable"      //
	ApplyMarkerOutside       = "marker_outside"         //
	ApplyBootNotConfirmed    = "boot_not_confirmed"     // version, count, from, note
	ApplyBootNotConfirmedBad = "boot_not_confirmed_bad" // version, count, from, error
	ApplyBootUnusable        = "boot_unusable"          // from, note
	ApplyBootUnusableBad     = "boot_unusable_bad"      // from, error
	ApplyBootDatabaseBad     = "boot_database_bad"      // from, error
	ApplyPolicyChannel       = "policy_channel"         // version, channel, max
	ApplyPolicyMajor         = "policy_major"           // version, max
	ApplyPolicyUnreadable    = "policy_unreadable"      // error
	ApplyBootDatabaseBack    = "boot_database_back"     // from, note
)

// codedError is an error that also says its code and arguments.
type codedError struct {
	err  error
	code string
	args map[string]string
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

// coded marks err with a code and arguments (key, value, ...). The text of err
// does not change.
func coded(err error, code string, kv ...string) error {
	args := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		args[kv[i]] = kv[i+1]
	}
	return &codedError{err: err, code: code, args: args}
}

// setMessage puts err in res: its text, and its code if it has one.
func (res *ApplyResult) setMessage(err error) {
	res.Message = err.Error()
	var ce *codedError
	if errors.As(err, &ce) {
		res.Code, res.Args = ce.code, ce.args
	}
}
