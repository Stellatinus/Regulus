package hub

import (
	"errors"
	"net/http"

	"github.com/CodexCircle/Regulus/internal/update"
)

// Codes of the hub's answers that are not explained by the status alone (#354).
// The dashboard shows them in the page's language ("hub.error.<code>" in
// dashboard/src/i18n/messages/core.*.ts); the English text stays in the answer
// for scripts and for clients that do not know the code. A new error that
// people meet in the dashboard gets a code here, a line in allErrorCodes and a
// text in both catalogs; TestEveryErrorCodeHasAText fails otherwise.
const (
	codeInternal = "internal_error"

	codeAgentNotFound    = "agent_not_found"
	codeInvalidJSON      = "invalid_json"
	codeTokenNotFound    = "token_not_found"
	codeAgentNotEnrolled = "agent_not_enrolled"
	codeInvalidTTLFormat = "invalid_ttl"

	codeLayoutsUnavailable = "layouts_unavailable"
	codeLayoutTooLarge     = "layout_too_large"
	codeLayoutRequired     = "layout_required"

	codeStreamUnavailable = "stream_unavailable"
	codeTooManyStreams    = "too_many_streams"
)

// errorCodes maps the errors the handlers know to a code.
var errorCodes = []struct {
	err  error
	code string
}{
	{update.ErrTooSoon, "update_too_soon"},
	{update.ErrChecksOff, "update_checks_off"},
	{update.ErrBusy, "update_busy"},
	{update.ErrNothingToFetch, "update_nothing_to_fetch"},
	{update.ErrNotReleaseBuild, "update_not_release_build"},
	{update.ErrApplyDisabled, "update_apply_disabled"},
	{update.ErrNothingToApply, "update_nothing_to_apply"},
	{update.ErrNotVerified, "update_not_verified"},
	{update.ErrApplying, "update_applying"},
	{ErrRolloutRunning, "rollout_running"},
	{ErrNothingToRoll, "rollout_nothing_to_roll"},
	{ErrNotRelease, "rollout_not_release"},
	{ErrInvalidPort, "port_invalid"},
	{ErrAgentsOn, "agents_on"},
	{ErrAgentsInConfig, "agents_in_config"},
	{ErrHubNotServing, "hub_not_serving"},
	{errListenerBlocked, "listener_blocked"},
	{ErrAgentsOff, "agents_off"},
}

// Codes that no sentinel error carries: update.IsUpstream and the typed
// errors of the fleet package.
const (
	codeUpstream    = "update_upstream"
	codeInvalidName = "invalid_name"
	codeInvalidTTL  = "invalid_token_ttl"
)

// codeOfError is the code of err, or "" when it has none.
func codeOfError(err error) string {
	for _, c := range errorCodes {
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	if update.IsUpstream(err) {
		return codeUpstream
	}
	return ""
}

// writeJSONErr answers with err's text and, when it has one, its code and the
// arguments of its text. An error that carries its own code (a Code method)
// wins over the table; update_upstream and listener_blocked carry the English
// reason as the argument "detail", which their text ends with.
func writeJSONErr(w http.ResponseWriter, status int, err error) {
	var c coded
	if errors.As(err, &c) && c.Code() != "" {
		body := map[string]any{"error": err.Error(), "code": c.Code()}
		if args := c.Args(); len(args) > 0 {
			body["args"] = args
		}
		writeJSONBody(w, status, body)
		return
	}
	code := codeOfError(err)
	if code == "" {
		writeJSONError(w, status, err.Error())
		return
	}
	body := map[string]any{"error": err.Error(), "code": code}
	if code == codeUpstream || code == "listener_blocked" {
		body["args"] = map[string]any{"detail": err.Error()}
	}
	writeJSONBody(w, status, body)
}

// allErrorCodes lists every code this package can write, for the test that
// checks the dashboard has a text for each.
func allErrorCodes() []string {
	codes := []string{
		codeInternal, codeAgentNotFound, codeInvalidJSON, codeTokenNotFound, codeAgentNotEnrolled, codeInvalidTTLFormat,
		codeLayoutsUnavailable, codeLayoutTooLarge, codeLayoutRequired, codeStreamUnavailable, codeTooManyStreams,
		codeUpstream, "rollout_wave_size", "name_too_long", "name_control_chars", "token_ttl_range",
	}
	codes = append(codes, update.SettingsErrorCodes...)
	for _, c := range errorCodes {
		codes = append(codes, c.code)
	}
	return codes
}

// rolloutInputError is a rollout request that cannot be run. It is an
// errBadRollout, with a code of its own for the dashboard (#354).
type rolloutInputError struct{ msg string }

func (e *rolloutInputError) Error() string        { return errBadRollout.Error() + ": " + e.msg }
func (e *rolloutInputError) Unwrap() error        { return errBadRollout }
func (e *rolloutInputError) Code() string         { return "rollout_wave_size" }
func (e *rolloutInputError) Args() map[string]any { return nil }

// coded is an error that carries its own code and arguments (auth.inputError,
// fleet.NameError, update's settings errors...).
type coded interface {
	Code() string
	Args() map[string]any
}
