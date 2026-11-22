package auth

import (
	"testing"

	"github.com/CodexCircle/Regulus/internal/codecheck"
)

// Every code the hub writes next to an audit detail has a text in both
// languages of the dashboard (#368).
func TestEveryAuditCodeHasATextInBothLanguages(t *testing.T) {
	codes := codecheck.StringConsts(t, "auditcode.go", "Code")
	codecheck.CheckCatalogs(t, "../..", "users", "users.detail.code.", codes)
}
