package hub

import (
	"testing"

	"github.com/CodexCircle/Regulus/internal/codecheck"
)

// Every code of a rollout step's message has a text in both languages of the
// dashboard (#368).
func TestEveryRolloutCodeHasATextInBothLanguages(t *testing.T) {
	codes := codecheck.StringConsts(t, "rollout.go", "Code")
	codecheck.CheckCatalogs(t, "../..", "updates", "updates.rolloutMsg.", codes)
}
