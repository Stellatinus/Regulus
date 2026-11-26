package packaging

import (
	"os"
	"strings"
	"testing"
)

// The embedded units are the files of the package, not copies: this fails if
// one is not embedded or does not say what the helper needs.
func TestEmbeddedUnitsAreThePackagesFiles(t *testing.T) {
	for name, want := range map[string]string{
		"regulus-plugin-apply.path":    "PathExists=/var/lib/regulus/plugin-apply/request.json",
		"regulus-plugin-apply.service": "ExecStart=/usr/bin/regulus plugin-apply",
	} {
		got, err := Units.ReadFile(name)
		if err != nil {
			t.Fatalf("%s is not embedded: %v", name, err)
		}
		if !strings.Contains(string(got), want) {
			t.Errorf("%s does not contain %q", name, want)
		}
		disk, err := os.ReadFile(name)
		if err != nil || string(disk) != string(got) {
			t.Errorf("%s: the embedded copy differs from the file", name)
		}
	}
}
