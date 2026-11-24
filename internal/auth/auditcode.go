package auth

import (
	"context"
	"encoding/json"
)

// Audit detail codes (#368). The detail of an audit entry is English text; the
// entry also carries a code and arguments so that the dashboard can show it in
// the viewer's language without reading the English words. A code is a stable
// id, and the dashboard has a text for it in `users.detail.<code>`
// (dashboard/src/i18n/messages/users.*.ts); a test there fails when a code has
// no text in either language. Add a code here only together with those texts.
const (
	CodeRole                   = "role"                   // role
	CodeFromTo                 = "from_to"                // from, to
	CodeFromVersionMessage     = "from_version_message"   // version, message
	CodeFromVersion            = "from_version"           // version
	CodeOtherSessionsEnded     = "other_sessions_ended"   //
	CodeResetLinkUsed          = "reset_link_used"        //
	CodeSessionsEnded          = "sessions_ended"         //
	CodeSessionsEndedByCommand = "sessions_ended_command" //
	CodeFirstAdmin             = "first_admin"            //
	CodeResetAdminCommand      = "reset_admin_command"    //
	CodeWrongSecondFactor      = "wrong_second_factor"    //
	CodeAccountDisabled        = "account_disabled"       //
	CodeRecoveryCode           = "recovery_code"          //
	CodePort                   = "port"                   // port
	CodeRolloutStarted         = "rollout_started"        // servers, wave
	CodeRolloutState           = "rollout_state"          // state: stopped, done or failed
	CodeRoleRenamed            = "role_renamed"           // name, permissions
	CodePermissions            = "permissions"            // permissions: ids separated by commas
	CodeCreateAdminCommand     = "create_admin_command"   //

	// Details that were English only (#372).
	CodeUpdateSettings       = "update_settings"        // mode, channel
	CodeUpdateSettingsWindow = "update_settings_window" // mode, channel, from, to
	CodeDownloaded           = "update_downloaded"      // archive, sha256, verified
	CodeDownloadFailed       = "update_download_failed" // error: the system's own text, English
	CodeTwoFactorSettings    = "two_factor_settings"    // the password check of the two-factor settings failed
	CodeReauthentication     = "reauthentication"       // the password check of a re-authentication failed

	// API tokens (#406).
	CodeAPIToken        = "api_token"              // name, permissions: ids separated by commas
	CodeAPITokenName    = "api_token_name"         // name
	CodeAPITokenRequest = "api_token_request"      // method, path
	CodeAPITokenLimited = "api_token_rate_limited" //
)

type codeKey struct{}

type codeValue struct {
	code string
	args map[string]string
}

// WithCode returns a context that makes the next audit entry written with it
// carry a code and arguments (key, value, key, value...). It exists so that
// the parts of the hub that record through Record (update, fleet, rollout) can
// pass a code without a change to their small interfaces.
func WithCode(ctx context.Context, code string, kv ...string) context.Context {
	args := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		args[kv[i]] = kv[i+1]
	}
	return context.WithValue(ctx, codeKey{}, codeValue{code, args})
}

func codeFrom(ctx context.Context) (string, map[string]string) {
	v, _ := ctx.Value(codeKey{}).(codeValue)
	return v.code, v.args
}

// RecordCode is Record with a code and arguments (key, value, ...).
func (s *Service) RecordCode(ctx context.Context, actor, event, target, detail, code string, kv ...string) {
	s.audit(WithCode(ctx, code, kv...), actor, event, target, detail)
}

func encodeArgs(args map[string]string) string {
	if len(args) == 0 {
		return ""
	}
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(b)
}

func decodeArgs(s string) map[string]string {
	if s == "" {
		return nil
	}
	var m map[string]string
	if json.Unmarshal([]byte(s), &m) != nil || len(m) == 0 {
		return nil
	}
	return m
}

// WithCodeArgs is WithCode with the arguments already in a map.
func WithCodeArgs(ctx context.Context, code string, args map[string]string) context.Context {
	cp := make(map[string]string, len(args))
	for k, v := range args {
		cp[k] = v
	}
	return context.WithValue(ctx, codeKey{}, codeValue{code, cp})
}
