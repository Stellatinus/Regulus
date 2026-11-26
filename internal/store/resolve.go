package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/CodexCircle/Regulus/internal/update"
)

// Codes of an Error, part of the API (docs/http-api.md).
const (
	ErrCodeNotFound         = "plugin_not_found"
	ErrCodeSourceAmbiguous  = "source_ambiguous"
	ErrCodeSourceUnknown    = "source_unknown"
	ErrCodeSourceUnavail    = "source_unavailable"
	ErrCodeVersionNotOffer  = "version_not_offered"
	ErrCodeNoCompatible     = "no_compatible_version"
	ErrCodeAlreadyInstalled = "already_installed"
	ErrCodeNotInstalled     = "not_installed"
	ErrCodeNoSource         = "no_source"
	ErrCodeUpToDate         = "up_to_date"
	ErrCodePluginsOff       = "plugins_off"
	ErrCodeBusy             = "busy"
	ErrCodeDownload         = "download_failed"
	ErrCodeHelperTimeout    = "helper_timeout"
	ErrCodeHelperFailed     = "helper_failed"
)

// Error is a refusal with a stable code. The hub answers with it ("code") and
// the dashboard translates the code; Error() is the English text.
type Error struct {
	CodeStr string
	Msg     string
	// Helper is the code the root helper gave, when it is what refused.
	Helper string
}

func (e *Error) Error() string { return e.Msg }

// Code returns the stable code.
func (e *Error) Code() string { return e.CodeStr }

// Args is unused today; it is there so the hub can pass it on.
func (e *Error) Args() map[string]any {
	if e.Helper == "" {
		return nil
	}
	return map[string]any{"helper_code": e.Helper}
}

func errf(code, format string, args ...any) *Error {
	return &Error{CodeStr: code, Msg: fmt.Sprintf(format, args...)}
}

// Offer is a version of a plugin the hub can install, with everything the
// helper needs to verify it again.
type Offer struct {
	Source   Source
	Plugin   IndexPlugin
	Version  IndexVersion
	Artifact Artifact
	// Index and Bundle are the signed index and its signature, as read.
	Index  []byte
	Bundle []byte
}

// Resolve finds the version of plugin id to install. source names the source
// ("" to use the only source that offers it), version the version ("" for the
// newest that is offered, compatible and not yanked). A version that is
// yanked, that the channel does not offer or that this hub cannot run is
// refused, never installed.
func (s *Service) Resolve(ctx context.Context, id, version, source string) (Offer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.o.Now()
	hub := s.o.HubChannel(ctx)
	var named *Source
	for i := range s.o.Sources {
		if s.o.Sources[i].Name == source {
			named = &s.o.Sources[i]
		}
	}
	if source != "" && named == nil {
		return Offer{}, errf(ErrCodeSourceUnknown, "there is no source named %q", source)
	}
	type cand struct {
		offer Offer
		v     update.Version
	}
	byName := map[string][]cand{}
	var order []string
	var unavailable []string
	for _, src := range s.o.Sources {
		if source != "" && src.Name != source {
			continue
		}
		st := s.st[src.Name]
		if st == nil {
			st = &sourceState{}
			s.st[src.Name] = st
		}
		if st.tried.IsZero() || now.Sub(st.tried) >= s.o.TTL {
			s.refresh(ctx, src, st)
		}
		if !st.have || !now.Before(st.index.Expires) {
			unavailable = append(unavailable, src.Name)
			continue
		}
		ch := hub
		if src.Channel != "" {
			ch = update.Channel(src.Channel)
		}
		env := s.o.Env()
		for _, p := range st.index.Plugins {
			if p.ID != id {
				continue
			}
			for _, v := range p.Versions {
				pv, err := update.ParseVersion(v.Version)
				if err != nil {
					continue
				}
				if version != "" && v.Version != version {
					continue
				}
				if !ch.Offers(pv) {
					continue
				}
				ok, why := CheckVersion(v, env)
				if version != "" {
					// Asked for by name: say why it cannot be had.
					if v.Yanked {
						return Offer{}, errf(ErrCodeNoCompatible, "%s %s was withdrawn (yanked)", id, version)
					}
					if !ok {
						return Offer{}, errf(ErrCodeNoCompatible, "%s %s cannot be installed: %s", id, version, why)
					}
				} else if !ok || v.Yanked {
					continue
				}
				var art Artifact
				for _, a := range v.Artifacts {
					if a.OS == env.Machine.OS && a.Arch == env.Machine.Arch {
						art = a
					}
				}
				if _, seen := byName[src.Name]; !seen {
					order = append(order, src.Name)
				}
				byName[src.Name] = append(byName[src.Name], cand{Offer{Source: src, Plugin: p, Version: v, Artifact: art, Index: st.raw, Bundle: st.bundle}, pv})
			}
		}
	}
	switch len(order) {
	case 0:
		if len(unavailable) > 0 && source != "" {
			return Offer{}, errf(ErrCodeSourceUnavail, "the source %s cannot be read now (see the store page)", source)
		}
		if version != "" {
			return Offer{}, errf(ErrCodeVersionNotOffer, "no source offers %s %s on this hub's channel", id, version)
		}
		return Offer{}, errf(ErrCodeNotFound, "no source offers a version of %s that this hub can install", id)
	case 1:
	default:
		sort.Strings(order)
		return Offer{}, errf(ErrCodeSourceAmbiguous, "%s is offered by several sources (%v): say which one", id, order)
	}
	cs := byName[order[0]]
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].v.Compare(cs[j].v) > 0 })
	return cs[0].offer, nil
}
