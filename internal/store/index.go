package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/CodexCircle/Regulus/internal/update"
)

// FormatVersion is the only index format this client speaks.
const FormatVersion = 1

// MaxIndexSize bounds what is read from a source: an index is small.
const MaxIndexSize = 8 << 20

// clockSkew is how far in the future an index's issued_at may be.
const clockSkew = 10 * time.Minute

// Errors of an index. ErrExpired and ErrOlder are told apart so the page can
// say what is wrong.
var (
	ErrFormat  = errors.New("the index has a format this Regulus does not speak")
	ErrExpired = errors.New("the index has expired")
	ErrOlder   = errors.New("the index is older than the last one verified")
)

// Index is the signed list of a source's plugins ("The index" in the design).
// Fields it does not know are ignored, as docs/versioning.md says.
type Index struct {
	Format   int           `json:"format"`
	Source   string        `json:"source"`
	IssuedAt time.Time     `json:"issued_at"`
	Expires  time.Time     `json:"expires"`
	Plugins  []IndexPlugin `json:"plugins"`
}

// IndexPlugin is one plugin of an index.
type IndexPlugin struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Kind        string         `json:"kind"`
	Audience    string         `json:"audience"`
	Homepage    string         `json:"homepage"`
	Versions    []IndexVersion `json:"versions"`
}

// IndexVersion is one release of a plugin.
type IndexVersion struct {
	Version     string       `json:"version"`
	Channel     string       `json:"channel"`
	ReleasedAt  string       `json:"released_at"`
	API         uint32       `json:"api"`
	Requires    string       `json:"requires"`
	Components  []string     `json:"components"`
	Provides    []string     `json:"provides"`
	Needs       []Need       `json:"needs"`
	Permissions []Permission `json:"permissions"`
	Artifacts   []Artifact   `json:"artifacts"`
	Yanked      bool         `json:"yanked"`
}

// Need is a capability a plugin needs from another.
type Need struct {
	Capability string `json:"capability"`
	Optional   bool   `json:"optional,omitempty"`
}

// Permission is what a plugin asks to be allowed.
type Permission struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// Artifact is a package for one machine.
type Artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

var idRE = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// ParseIndex reads and validates an index at time now. It does not check the
// signature (the caller verified the same bytes first) nor the rollback rule,
// which needs the last verified index (see CheckNotOlder).
func ParseIndex(data []byte, now time.Time) (Index, error) {
	if len(data) > MaxIndexSize {
		return Index{}, fmt.Errorf("the index is larger than %d MiB", MaxIndexSize>>20)
	}
	var ix Index
	if err := json.Unmarshal(data, &ix); err != nil {
		return Index{}, fmt.Errorf("the index is not valid JSON: %w", err)
	}
	if ix.Format != FormatVersion {
		return Index{}, fmt.Errorf("%w (format %d, this one speaks %d)", ErrFormat, ix.Format, FormatVersion)
	}
	if ix.IssuedAt.IsZero() || ix.Expires.IsZero() || !ix.Expires.After(ix.IssuedAt) {
		return Index{}, errors.New("the index has no valid issued_at and expires")
	}
	if ix.IssuedAt.After(now.Add(clockSkew)) {
		return Index{}, errors.New("the index is issued in the future")
	}
	if !now.Before(ix.Expires) {
		return Index{}, fmt.Errorf("%w (%s)", ErrExpired, ix.Expires.UTC().Format(time.RFC3339))
	}
	seen := map[string]bool{}
	for _, p := range ix.Plugins {
		if !idRE.MatchString(p.ID) || len(p.ID) < 2 || len(p.ID) > 40 {
			return Index{}, fmt.Errorf("the index has a plugin with a bad id %q", p.ID)
		}
		if seen[p.ID] {
			return Index{}, fmt.Errorf("the index lists %s twice", p.ID)
		}
		seen[p.ID] = true
		for _, v := range p.Versions {
			if _, err := update.ParseVersion(v.Version); err != nil {
				return Index{}, fmt.Errorf("%s: %w", p.ID, err)
			}
		}
	}
	return ix, nil
}

// CheckNotOlder refuses an index issued before the last one verified: a replay
// of an older signed index would bring back yanked versions.
func CheckNotOlder(ix Index, last time.Time) error {
	if !last.IsZero() && ix.IssuedAt.Before(last) {
		return fmt.Errorf("%w (issued %s, last verified %s)", ErrOlder, ix.IssuedAt.UTC().Format(time.RFC3339), last.UTC().Format(time.RFC3339))
	}
	return nil
}
