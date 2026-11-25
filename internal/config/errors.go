package config

import (
	"fmt"
	"slices"
)

// ErrorCode names why a value for a setting was refused or could not be
// saved. The console turns a code into a sentence in its own language
// (internal/console); the CLI prints the English text of SettingError.Error.
type ErrorCode string

// The codes of SettingError, each with the arguments it carries in Args, in
// order. The English texts are in englishErrors.
const (
	ErrUnknownSetting  ErrorCode = "unknown_setting"  // the key, the known keys
	ErrEmpty           ErrorCode = "empty"            // none
	ErrAddress         ErrorCode = "address"          // none
	ErrPort            ErrorCode = "port"             // none
	ErrSchema          ErrorCode = "schema"           // none
	ErrPasswordInFile  ErrorCode = "password_in_file" // the environment variable
	ErrBool            ErrorCode = "bool"             // none
	ErrDuration        ErrorCode = "duration"         // none
	ErrSize            ErrorCode = "size"             // none
	ErrRetention       ErrorCode = "retention"        // none
	ErrTooShort        ErrorCode = "too_short"        // the minimum
	ErrChoice          ErrorCode = "choice"           // the choices, joined with ", "
	ErrReadFile        ErrorCode = "read_file"        // the reason
	ErrDataDirMove     ErrorCode = "data_dir_move"    // the old directory, the new one
	ErrUnusualLine     ErrorCode = "unusual_line"     // the line
	ErrUnreadable      ErrorCode = "unreadable"       // the file, the reason
	ErrHandEdit        ErrorCode = "hand_edit"        // the file
	ErrPostgresHost    ErrorCode = "postgres_host"    // none
	ErrInvalid         ErrorCode = "invalid"          // the reason (English, from Validate)
	ErrWritePermission ErrorCode = "write_permission" // the reason
	ErrWriteFile       ErrorCode = "write_file"       // the reason
)

// englishErrors are the texts of the codes for the CLI (and any caller that
// shows the error as it is). Every %s or %q takes one of Args, in order.
var englishErrors = map[ErrorCode]string{
	ErrUnknownSetting:  "unknown setting %q (known: %s)",
	ErrEmpty:           "must not be empty",
	ErrAddress:         "use host:port, such as :8080 or 127.0.0.1:8080, or only the port",
	ErrPort:            "the port must be a number from 1 to 65535",
	ErrSchema:          "use letters, digits and underscore, not starting with a digit",
	ErrPasswordInFile:  "the secret is never stored in the configuration file: put %s=... in /etc/regulus/regulus.env (mode 0640, root:regulus) and restart the service",
	ErrBool:            "use true or false",
	ErrDuration:        "use a duration such as 10s or 1m",
	ErrSize:            "use a size such as 256MiB or 1GiB",
	ErrRetention:       "use days such as 3d or 90d (or a duration such as 36h), or 0 to turn the tier off",
	ErrTooShort:        "too short (minimum %s)",
	ErrChoice:          "use one of: %s",
	ErrReadFile:        "read config file: %s",
	ErrDataDirMove:     "%s holds the hub's data and %s holds none. Changing the setting does not move it: the hub would start with an empty database, a new secret key and a new CA, every enrolled agent would distrust it and the two-factor secrets would be lost. Stop the hub, copy the directory to the new place, then change the setting; or repeat the command with --force to start fresh on purpose",
	ErrUnusualLine:     "cannot edit it automatically (unusual line %q); edit the file by hand",
	ErrUnreadable:      "the edit would leave %s unreadable: %s",
	ErrHandEdit:        "cannot edit it in %s automatically (it is written in a form this tool does not change); edit the file by hand",
	ErrPostgresHost:    "invalid configuration: database.host must be set when database.type is postgres",
	ErrInvalid:         "invalid configuration: %s",
	ErrWritePermission: "write config file: %s (the file belongs to another user: run it with sudo)",
	ErrWriteFile:       "write config file: %s",
}

// ErrorCodes lists every code, for a caller (the console) that keeps its own
// text for each and wants a test to catch one it lacks.
func ErrorCodes() []ErrorCode {
	codes := make([]ErrorCode, 0, len(englishErrors))
	for c := range englishErrors {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	return codes
}

// EnglishError is the English template of a code, with its verbs.
func EnglishError(c ErrorCode) string { return englishErrors[c] }

// SettingError is why Normalize, SetFile or SetFileForce refused a value. Its
// Error is English, prefixed with the key ("listen: the port must be ...");
// a GUI shows its own text for Code with Args instead.
type SettingError struct {
	Key  string
	Code ErrorCode
	// Args fill the verbs of the text of Code, in order.
	Args []string
	// Err is the underlying error, when there is one (a file that cannot be
	// read or written).
	Err error
}

func (e *SettingError) Error() string {
	args := make([]any, len(e.Args))
	for i, a := range e.Args {
		args[i] = a
	}
	text := fmt.Sprintf(englishErrors[e.Code], args...)
	if e.Key == "" {
		return text
	}
	return e.Key + ": " + text
}

func (e *SettingError) Unwrap() error { return e.Err }

func settingErr(key string, code ErrorCode, args ...string) *SettingError {
	return &SettingError{Key: key, Code: code, Args: args}
}
