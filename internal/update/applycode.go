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
