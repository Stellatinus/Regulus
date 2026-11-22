package update

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"

	"gopkg.in/yaml.v3"
)

// PolicyFile is where the root helper looks for the local update policy (#215).
// It is read from /etc, which only root writes, and never from the data
// directory the hub's user owns: a compromised hub must not be able to widen
// what it may install as root.
const PolicyFile = "/etc/regulus/update-policy.yaml"

// Policy bounds what the root helper installs on this machine, whatever the
// hub or the agent asks for. A release has to be signed (checked elsewhere) and
// newer than the installed one; the policy adds how far from the installed
// version it may be. The zero Policy allows everything. See docs/updates.md.
type Policy struct {
	// MaxChannel is the least cautious channel a release may belong to: with
	// "stable" a beta or an alpha is refused, with "beta" an alpha is, with
	// "alpha" or empty nothing is.
	MaxChannel Channel `yaml:"max_channel"`
	// MaxMajor is the highest major version that may be installed. Nil: no
	// limit.
	MaxMajor *uint64 `yaml:"max_major"`
}

// Check says whether the policy allows installing to. It returns a coded error
// (ApplyPolicyChannel, ApplyPolicyMajor) when it does not.
func (p Policy) Check(to Version) error {
	if p.MaxChannel != "" && !p.MaxChannel.Offers(to) {
		return coded(fmt.Errorf("version %s is on the %s channel, and this server's update policy allows at most %s (%s)", to, to.Channel(), p.MaxChannel, PolicyFile),
			ApplyPolicyChannel, "version", to.String(), "channel", string(to.Channel()), "max", string(p.MaxChannel))
	}
	if p.MaxMajor != nil && to.Major > *p.MaxMajor {
		return coded(fmt.Errorf("version %s is a new major version, and this server's update policy allows at most %d (%s)", to, *p.MaxMajor, PolicyFile),
			ApplyPolicyMajor, "version", to.String(), "max", fmt.Sprint(*p.MaxMajor))
	}
	return nil
}

// LoadPolicy reads the policy file. A missing file is the zero Policy. A file
// that cannot be trusted (not a regular file, writable by anyone but its
// owner, or, with requireRoot, not owned by root), or that does not parse, is
// an error: the helper then refuses to install anything, because ignoring a
// policy it cannot read would be the same as having none.
func LoadPolicy(path string, requireRoot bool) (Policy, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Policy{}, nil
	}
	if err != nil {
		return Policy{}, err
	}
	if !fi.Mode().IsRegular() {
		return Policy{}, fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return Policy{}, fmt.Errorf("%s is writable by others than its owner (mode %o)", path, fi.Mode().Perm())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && requireRoot && st.Uid != 0 {
		return Policy{}, fmt.Errorf("%s is not owned by root", path)
	}
	data, err := readRegular(path, maxControlFile)
	if err != nil {
		return Policy{}, err
	}
	var p Policy
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil && !errors.Is(err, io.EOF) {
		return Policy{}, fmt.Errorf("%s: %w", path, err)
	}
	if p.MaxChannel != "" && !p.MaxChannel.Valid() {
		return Policy{}, fmt.Errorf("%s: max_channel is %q, want stable, beta or alpha", path, p.MaxChannel)
	}
	return p, nil
}
