package store

import (
	"fmt"
	"sort"

	"github.com/CodexCircle/Regulus/internal/plugin"
	"github.com/CodexCircle/Regulus/internal/update"
)

// Machine is the platform a hub runs on, for choosing an artifact.
type Machine struct{ OS, Arch string }

// Env is what a version is checked against.
type Env struct {
	// Hub is this Regulus' release; HubKnown is false for a development build,
	// which runs any plugin whose api it speaks (docs/plugins/manifest.md).
	Hub      update.Version
	HubKnown bool
	Machine  Machine
	// Installed maps a plugin id to its installed version.
	Installed map[string]string
}

// VersionJSON, PluginJSON and the types around them are the contract of
// GET /api/store; the dashboard codes against these names.
type VersionJSON struct {
	Version            string       `json:"version"`
	Channel            string       `json:"channel"`
	ReleasedAt         string       `json:"released_at"`
	Compatible         bool         `json:"compatible"`
	IncompatibleReason string       `json:"incompatible_reason,omitempty"`
	Permissions        []Permission `json:"permissions"`
	Provides           []string     `json:"provides"`
	Needs              []Need       `json:"needs"`
	Yanked             bool         `json:"yanked"`
}

// InstalledJSON is the installed version of a plugin.
type InstalledJSON struct {
	Version string `json:"version"`
}

// PluginJSON is a plugin as the store page shows it.
type PluginJSON struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Source      string         `json:"source"`
	Homepage    string         `json:"homepage"`
	Kind        string         `json:"kind,omitempty"`
	Audience    string         `json:"audience"`
	Installed   *InstalledJSON `json:"installed"`
	Latest      *string        `json:"latest"`
	Versions    []VersionJSON  `json:"versions"`
}

// CheckVersion says whether this hub can run a version, and why not.
func CheckVersion(v IndexVersion, env Env) (ok bool, reason string) {
	if v.API < plugin.MinAPIVersion || v.API > plugin.APIVersion {
		return false, fmt.Sprintf("it speaks plugin API %d; this Regulus speaks %d to %d", v.API, plugin.MinAPIVersion, plugin.APIVersion)
	}
	if v.Requires != "" {
		r, err := plugin.ParseRange(v.Requires)
		if err != nil {
			return false, "its requirement on Regulus cannot be read: " + err.Error()
		}
		if env.HubKnown && !r.Contains(env.Hub) {
			return false, fmt.Sprintf("it needs Regulus %s; this is %s", v.Requires, env.Hub)
		}
	}
	for _, a := range v.Artifacts {
		if a.OS == env.Machine.OS && a.Arch == env.Machine.Arch {
			return true, ""
		}
	}
	return false, fmt.Sprintf("it has no package for %s/%s", env.Machine.OS, env.Machine.Arch)
}

// Catalogue turns an index into the plugins of one source, for a hub that
// follows channel. A version the channel does not offer is left out, and so is
// a plugin left with none. latest is the newest version that is offered,
// compatible and not yanked.
func Catalogue(ix Index, source string, channel update.Channel, env Env) []PluginJSON {
	out := []PluginJSON{}
	for _, p := range ix.Plugins {
		type item struct {
			v   update.Version
			dto VersionJSON
			ok  bool
		}
		var items []item
		for _, v := range p.Versions {
			pv, err := update.ParseVersion(v.Version)
			if err != nil || !channel.Offers(pv) {
				continue
			}
			ok, why := CheckVersion(v, env)
			items = append(items, item{pv, VersionJSON{
				Version: v.Version, Channel: string(pv.Channel()), ReleasedAt: v.ReleasedAt,
				Compatible: ok && !v.Yanked, IncompatibleReason: why, Yanked: v.Yanked,
				Permissions: nonNil(v.Permissions), Provides: nonNilS(v.Provides), Needs: nonNilN(v.Needs),
			}, ok})
			if v.Yanked && why == "" {
				items[len(items)-1].dto.IncompatibleReason = "this version was withdrawn (yanked)"
			}
		}
		if len(items) == 0 {
			continue
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].v.Compare(items[j].v) > 0 })
		pj := PluginJSON{
			ID: p.ID, Name: p.Name, Description: p.Description, Source: source, Homepage: p.Homepage,
			Kind: p.Kind, Audience: p.Audience, Versions: []VersionJSON{},
		}
		if pj.Audience == "" {
			pj.Audience = "admin"
		}
		for _, it := range items {
			pj.Versions = append(pj.Versions, it.dto)
			if pj.Latest == nil && it.dto.Compatible {
				l := it.dto.Version
				pj.Latest = &l
			}
		}
		if v, ok := env.Installed[p.ID]; ok {
			pj.Installed = &InstalledJSON{Version: v}
		}
		out = append(out, pj)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func nonNil(p []Permission) []Permission {
	if p == nil {
		return []Permission{}
	}
	return p
}
func nonNilS(p []string) []string {
	if p == nil {
		return []string{}
	}
	return p
}
func nonNilN(p []Need) []Need {
	if p == nil {
		return []Need{}
	}
	return p
}
