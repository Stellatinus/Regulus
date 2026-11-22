// Package codecheck is for tests only: it checks that every code a Go package
// writes next to a message (an audit detail, a result of the update helper, a
// step of a rollout; #368) has a text in the English and the Italian catalog of
// the dashboard. It does not run in the hub, and it does not check that the
// text is right, only that there is one.
package codecheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// StringConsts returns the values of the string constants in goFile whose name
// starts with prefix.
func StringConsts(t *testing.T, goFile, prefix string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), goFile, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, sp := range gd.Specs {
			vs := sp.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if !strings.HasPrefix(n.Name, prefix) || i >= len(vs.Values) {
					continue
				}
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					out = append(out, v)
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("no constant starting with %q in %s", prefix, goFile)
	}
	return out
}

// CheckCatalogs fails for each code that has no `<keyPrefix><code>` text in
// both `<area>.en.ts` and `<area>.it.ts` under the dashboard's messages.
func CheckCatalogs(t *testing.T, repoRoot, area, keyPrefix string, codes []string) {
	t.Helper()
	for _, lang := range []string{"en", "it"} {
		path := filepath.Join(repoRoot, "dashboard", "src", "i18n", "messages", area+"."+lang+".ts")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, code := range codes {
			re := regexp.MustCompile(`(?m)^\s*"` + regexp.QuoteMeta(keyPrefix+code) + `":\s*\S`)
			if !re.Match(b) {
				t.Errorf("code %q has no text %q in %s", code, keyPrefix+code, path)
			}
		}
	}
}
