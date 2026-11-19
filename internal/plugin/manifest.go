package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/CodexCircle/Regulus/internal/update"
)

// APIVersion is the plugin API this build of Regulus speaks: the contract in
// proto/regulus/plugin/v1 and the meaning of plugin.yaml. MinAPIVersion is
// the oldest one it still runs. A plugin declares the version it speaks with
// `api`; see docs/versioning.md for when it changes.
const (
	APIVersion    uint32 = 1
	MinAPIVersion uint32 = 1
)

// ManifestName is the file that describes a plugin, at the top of its
// directory.
const ManifestName = "plugin.yaml"

// maxManifest bounds what is read of a manifest.
const maxManifest = 64 << 10

// Roles a component can be started in.
const (
	RoleAgent = "agent"
	RoleHub   = "hub"
)

// Manifest is plugin.yaml. Unknown keys are refused, so that a misspelt key
// is an error rather than a setting silently ignored.
type Manifest struct {
	// ID names the plugin everywhere: its directory, its permissions, its
	// tags (plugin/<id>/vX.Y.Z). Lower-case letters, digits and single
	// hyphens, 2 to 40 characters, starting with a letter.
	ID string `yaml:"id"`
	// Version is the plugin's own release version (SemVer, no "v").
	Version string `yaml:"version"`
	// Name and Description are shown to the administrator.
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// API is the plugin API version the plugin speaks.
	API uint32 `yaml:"api"`
	// Requires is the range of Regulus versions the plugin works with, for
	// example ">=0.3.0 <0.5.0". Empty means any.
	Requires string `yaml:"requires"`
	// Components are the parts of the plugin; at least one.
	Components Components `yaml:"components"`
	// Permissions are what the plugin asks to be allowed. They become
	// permissions an administrator can give to roles (#61); the plugin gets
	// none of them by itself (#20).
	Permissions []Permission `yaml:"permissions"`
	// Provides names integration points other plugins can use. Reserved: no
	// plugin can depend on another yet (docs/plugins/README.md).
	Provides []string `yaml:"provides"`
}

// Components are the parts a plugin can have.
type Components struct {
	Agent *Program `yaml:"agent"`
	Hub   *Program `yaml:"hub"`
	// UI is a dashboard module (#21). It is validated, never run.
	UI *UIModule `yaml:"ui"`
}

// Program is a component that runs as a process.
type Program struct {
	// Command is the executable, relative to the plugin directory, inside it.
	Command string `yaml:"command"`
	// Args are passed as they are; no shell is involved.
	Args []string `yaml:"args"`
}

// UIModule is a dashboard component.
type UIModule struct {
	// Module is a JavaScript module, relative to the plugin directory.
	Module string `yaml:"module"`
}

// Permission is one permission a plugin asks for.
type Permission struct {
	// ID is "<plugin id>.<name>", for example "docker.containers.read".
	ID          string `yaml:"id"`
	Description string `yaml:"description"`
}

var (
	idRE   = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	permRE = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`)
)

// ParseManifest reads and validates a manifest.
func ParseManifest(r io.Reader) (Manifest, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxManifest+1))
	if err != nil {
		return Manifest{}, err
	}
	if len(data) > maxManifest {
		return Manifest{}, fmt.Errorf("%s is larger than %d KiB", ManifestName, maxManifest>>10)
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return Manifest{}, fmt.Errorf("%s is empty", ManifestName)
		}
		return Manifest{}, fmt.Errorf("%s: %w", ManifestName, err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Validate reports the first thing wrong with the manifest.
func (m Manifest) Validate() error {
	if len(m.ID) < 2 || len(m.ID) > 40 || !idRE.MatchString(m.ID) {
		return fmt.Errorf("id %q must be 2 to 40 lower-case letters, digits and single hyphens, starting with a letter", m.ID)
	}
	if _, err := update.ParseVersion(m.Version); err != nil || strings.HasPrefix(m.Version, "v") {
		return fmt.Errorf("version %q must be a SemVer version without a leading v, such as 0.1.0", m.Version)
	}
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("name must not be empty")
	}
	if m.API == 0 {
		return errors.New("api must name the plugin API version the plugin speaks (1)")
	}
	if _, err := ParseRange(m.Requires); err != nil {
		return fmt.Errorf("requires: %w", err)
	}
	c := m.Components
	if c.Agent == nil && c.Hub == nil && c.UI == nil {
		return errors.New("components must name at least one of agent, hub and ui")
	}
	for role, p := range map[string]*Program{RoleAgent: c.Agent, RoleHub: c.Hub} {
		if p == nil {
			continue
		}
		if err := checkRelative(p.Command); err != nil {
			return fmt.Errorf("components.%s.command: %w", role, err)
		}
	}
	if c.UI != nil {
		if err := checkRelative(c.UI.Module); err != nil {
			return fmt.Errorf("components.ui.module: %w", err)
		}
		if !strings.HasSuffix(c.UI.Module, ".js") {
			return errors.New("components.ui.module must be a .js module")
		}
	}
	seen := map[string]bool{}
	for _, p := range m.Permissions {
		if !permRE.MatchString(p.ID) || !strings.HasPrefix(p.ID, m.ID+".") {
			return fmt.Errorf("permission %q must start with the plugin id and a dot (%s.something)", p.ID, m.ID)
		}
		if seen[p.ID] {
			return fmt.Errorf("permission %q is listed twice", p.ID)
		}
		seen[p.ID] = true
		if strings.TrimSpace(p.Description) == "" {
			return fmt.Errorf("permission %q needs a description: it is what the administrator reads before allowing it", p.ID)
		}
	}
	return nil
}

// checkRelative accepts a path inside the plugin directory: relative, clean,
// not leaving it. Links are checked again when the plugin starts.
func checkRelative(p string) error {
	switch {
	case p == "":
		return errors.New("must not be empty")
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("%q must be relative to the plugin directory", p)
	case strings.ContainsRune(p, '\\') || strings.ContainsRune(p, 0):
		return fmt.Errorf("%q is not a plain path", p)
	}
	clean := path.Clean(p)
	if clean != p || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("%q must be a clean path inside the plugin directory", p)
	}
	return nil
}

// Program returns the component of the given role, or nil.
func (m Manifest) Program(role string) *Program {
	switch role {
	case RoleAgent:
		return m.Components.Agent
	case RoleHub:
		return m.Components.Hub
	}
	return nil
}

// Compatible says whether this plugin can run on a core of the given version
// speaking the plugin API of this build. A development build of the core
// (not a release version) runs any plugin whose API it speaks.
func (m Manifest) Compatible(coreVersion string) error {
	if m.API < MinAPIVersion || m.API > APIVersion {
		return fmt.Errorf("the plugin speaks plugin API %d; this Regulus speaks %d to %d", m.API, MinAPIVersion, APIVersion)
	}
	v, ok := update.ParseBuildVersion(coreVersion)
	if !ok {
		return nil
	}
	r, err := ParseRange(m.Requires)
	if err != nil {
		return err
	}
	if !r.Contains(v) {
		return fmt.Errorf("the plugin needs Regulus %s, this is %s", m.Requires, v)
	}
	return nil
}

// Range is a set of versions: every constraint must hold.
type Range []constraint

type constraint struct {
	op string
	v  update.Version
}

func sameRelease(a, b update.Version) bool {
	return a.Major == b.Major && a.Minor == b.Minor && a.Patch == b.Patch
}

// ParseRange reads constraints such as ">=0.3.0 <0.5.0" (also separated by
// commas). Operators: >=, >, <=, <, =. Empty means any version. Versions are
// ordered as SemVer, with one exception: "<X.Y.Z" also leaves out the
// pre-releases of X.Y.Z.
func ParseRange(s string) (Range, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' })
	var out Range
	for _, f := range fields {
		op := ""
		for _, o := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(f, o) {
				op = o
				break
			}
		}
		if op == "" {
			return nil, fmt.Errorf("%q needs an operator: >=, >, <=, < or =", f)
		}
		v, err := update.ParseVersion(strings.TrimPrefix(f, op))
		if err != nil {
			return nil, fmt.Errorf("%q: %w", f, err)
		}
		out = append(out, constraint{op, v})
	}
	return out, nil
}

// Contains reports whether v satisfies every constraint.
func (r Range) Contains(v update.Version) bool {
	for _, c := range r {
		cmp := v.Compare(c.v)
		ok := false
		switch c.op {
		case ">=":
			ok = cmp >= 0
		case ">":
			ok = cmp > 0
		case "<=":
			ok = cmp <= 0
		case "<":
			// "<0.5.0" means "not the 0.5.0 release line": its pre-releases
			// (0.5.0-alpha.1), which SemVer orders before it, are left out
			// too, as a plugin author writing this bound means.
			preOfBound := v.Prerelease() && !c.v.Prerelease() && sameRelease(v, c.v)
			ok = cmp < 0 && !preOfBound
		case "=":
			ok = cmp == 0
		}
		if !ok {
			return false
		}
	}
	return true
}
