package hub

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/CodexCircle/Regulus/internal/auth"
)

// TestEveryErrorCodeHasAText is #354: each code the hub or the auth package
// can answer with has a text in the English and the Italian catalog, and is in
// the dashboard's list of codes it translates (api.ts). A new code that misses
// one of them fails here instead of showing English to an Italian reader.
func TestEveryErrorCodeHasAText(t *testing.T) {
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "dashboard", "src", rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	en, it, api := read("i18n/messages/core.en.ts"), read("i18n/messages/core.it.ts"), read("api.ts")

	seen := map[string]bool{}
	for _, code := range append(allErrorCodes(), auth.ErrorCodes()...) {
		if seen[code] {
			continue
		}
		seen[code] = true
		key := `"hub.error.` + code + `":`
		if !strings.Contains(en, key) {
			t.Errorf("code %q has no English text (core.en.ts %s)", code, key)
		}
		if !strings.Contains(it, key) {
			t.Errorf("code %q has no Italian text (core.it.ts %s)", code, key)
		}
		if !strings.Contains(api, `"`+code+`",`) {
			t.Errorf("code %q is not in HUB_ERROR_CODES of dashboard/src/api.ts", code)
		}
	}
	// The other way: a text the hub never sends is dead weight.
	for _, m := range regexp.MustCompile(`"hub\.error\.([a-z_]+)":`).FindAllStringSubmatch(en, -1) {
		if !seen[m[1]] && !isOlderCode(m[1]) {
			t.Errorf("the catalog has a text for %q, which neither the hub nor auth lists", m[1])
		}
	}
}

// isOlderCode: the codes of #330 are listed in auth.errorCodes through
// ErrorCodes, so none is exempt; kept as a hook for codes written by other
// packages.
func isOlderCode(string) bool { return false }

// TestErrorCodesInSourceAreListed finds the codes written as literals in the
// handlers and checks they are in the lists the test above covers: a code
// written in place and forgotten in the list would have no text checked.
func TestErrorCodesInSourceAreListed(t *testing.T) {
	listed := map[string]bool{}
	for _, c := range append(allErrorCodes(), auth.ErrorCodes()...) {
		listed[c] = true
	}
	type source struct {
		dir string
		re  *regexp.Regexp
	}
	for _, src := range []source{
		{".", regexp.MustCompile(`writeJSONErrorCode\([^)]*,\s*"([a-z_]+)"\)`)},
		{"../auth", regexp.MustCompile(`(?:writeErrorCode\([^)]*,\s*|inputErr\()"([a-z_]+)"`)},
	} {
		files, _ := filepath.Glob(filepath.Join(src.dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range src.re.FindAllStringSubmatch(string(b), -1) {
				if !listed[m[1]] {
					t.Errorf("%s writes the code %q, which is not in the list of codes", f, m[1])
				}
			}
		}
	}
}
