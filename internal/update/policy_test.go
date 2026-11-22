package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func u64(n uint64) *uint64 { return &n }

func mustVersion(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// #215: the policy bounds channel and major version.
func TestPolicyCheck(t *testing.T) {
	tests := []struct {
		name   string
		p      Policy
		to     string
		refuse string // the code, or "" when allowed
	}{
		{"no policy allows everything", Policy{}, "2.0.0-alpha.1", ""},
		{"stable refuses an alpha", Policy{MaxChannel: Stable}, "1.0.0-alpha.1", ApplyPolicyChannel},
		{"stable refuses a beta", Policy{MaxChannel: Stable}, "1.0.0-beta.2", ApplyPolicyChannel},
		{"stable allows a release", Policy{MaxChannel: Stable}, "1.0.1", ""},
		{"beta allows a beta", Policy{MaxChannel: Beta}, "1.0.0-rc.1", ""},
		{"beta refuses an alpha", Policy{MaxChannel: Beta}, "1.0.0-alpha.3", ApplyPolicyChannel},
		{"alpha allows an alpha", Policy{MaxChannel: Alpha}, "1.0.0-alpha.3", ""},
		{"a new major is refused", Policy{MaxMajor: u64(1)}, "2.0.0", ApplyPolicyMajor},
		{"the same major is allowed", Policy{MaxMajor: u64(1)}, "1.9.0", ""},
		{"major 0 can be the limit", Policy{MaxMajor: u64(0)}, "1.0.0", ApplyPolicyMajor},
		{"both are checked", Policy{MaxChannel: Stable, MaxMajor: u64(1)}, "2.0.0", ApplyPolicyMajor},
	}
	for _, tc := range tests {
		err := tc.p.Check(mustVersion(t, tc.to))
		if tc.refuse == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		var res ApplyResult
		if err == nil {
			t.Errorf("%s: allowed", tc.name)
			continue
		}
		res.setMessage(err)
		if res.Code != tc.refuse || res.Args["version"] != tc.to {
			t.Errorf("%s: %+v", tc.name, res)
		}
	}
}

func TestLoadPolicy(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p, err := LoadPolicy(filepath.Join(dir, "none.yaml"), false); err != nil || p.MaxChannel != "" || p.MaxMajor != nil {
		t.Errorf("a missing file: %+v, %v", p, err)
	}
	p, err := LoadPolicy(write("ok.yaml", "max_channel: stable\nmax_major: 1\n", 0o644), false)
	if err != nil || p.MaxChannel != Stable || p.MaxMajor == nil || *p.MaxMajor != 1 {
		t.Errorf("a good file: %+v, %v", p, err)
	}
	for name, tc := range map[string]struct {
		body string
		mode os.FileMode
		want string
	}{
		"group writable": {"max_channel: stable\n", 0o664, "writable"},
		"world writable": {"max_channel: stable\n", 0o666, "writable"},
		"a typo":         {"max_chanel: stable\n", 0o644, "max_chanel"},
		"a bad channel":  {"max_channel: nightly\n", 0o644, "nightly"},
		"not yaml":       {"max_major: [1\n", 0o644, ""},
	} {
		if _, err := LoadPolicy(write(strings.ReplaceAll(name, " ", "-")+".yaml", tc.body, tc.mode), false); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// A link is not followed.
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(filepath.Join(dir, "ok.yaml"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(link, false); err == nil {
		t.Error("a symbolic link was accepted")
	}
}

// The helper refuses what the policy forbids, whatever the request, and
// changes nothing; a policy it cannot read refuses everything.
func TestInstallerRefusesWhatThePolicyForbids(t *testing.T) {
	f := newInstallFixture(t, "0.1.0", "0.2.0", nil)
	f.in.Policy = Policy{MaxMajor: u64(0), MaxChannel: Stable}
	f.request(t, "0.2.0")
	if res, err := f.in.Apply(t.Context()); err != nil || res.Outcome != OutcomeApplied {
		t.Fatalf("an allowed update: %+v, %v", res, err)
	}

	f = newInstallFixture(t, "0.1.0", "1.0.0", nil)
	f.in.Policy = Policy{MaxMajor: u64(0)}
	f.request(t, "1.0.0")
	res, err := f.in.Apply(t.Context())
	if err == nil || res.Outcome != OutcomeFailed || res.Code != ApplyPolicyMajor {
		t.Errorf("a new major: %+v, %v", res, err)
	}
	if got := f.installed(t); got != "0.1.0" {
		t.Errorf("the binary was replaced: %q", got)
	}

	f = newInstallFixture(t, "0.1.0", "0.2.0-alpha.1", nil)
	f.in.Policy = Policy{MaxChannel: Stable}
	f.request(t, "0.2.0-alpha.1")
	if res, err := f.in.Apply(t.Context()); err == nil || res.Code != ApplyPolicyChannel || f.installed(t) != "0.1.0" {
		t.Errorf("a pre-release on a stable server: %+v, %v", res, err)
	}

	f = newInstallFixture(t, "0.1.0", "0.2.0", nil)
	f.in.PolicyErr = os.ErrPermission
	f.request(t, "0.2.0")
	if res, err := f.in.Apply(t.Context()); err == nil || res.Code != ApplyPolicyUnreadable || f.installed(t) != "0.1.0" {
		t.Errorf("an unreadable policy: %+v, %v", res, err)
	}
}
