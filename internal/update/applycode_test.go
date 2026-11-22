package update

import (
	"errors"
	"testing"

	"github.com/CodexCircle/Regulus/internal/codecheck"
)

// Every code the installer writes in result.json has a text in both languages
// of the dashboard (#368).
func TestEveryApplyCodeHasATextInBothLanguages(t *testing.T) {
	codes := codecheck.StringConsts(t, "applycode.go", "Apply")
	codecheck.CheckCatalogs(t, "../..", "updates", "updates.applyMsg.", codes)
}

func TestSetMessageTakesTheCodeOfACodedError(t *testing.T) {
	var res ApplyResult
	res.setMessage(coded(errors.New("boom"), ApplyNotNewer, "version", "1", "installed", "2"))
	if res.Message != "boom" || res.Code != ApplyNotNewer || res.Args["installed"] != "2" {
		t.Errorf("result = %+v", res)
	}
	res = ApplyResult{}
	res.setMessage(errors.New("plain"))
	if res.Message != "plain" || res.Code != "" || res.Args != nil {
		t.Errorf("an error without a code: %+v", res)
	}
}
