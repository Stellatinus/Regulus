// Package capability is the draft data model for what an agent machine does
// (its capabilities) and what it may reach or use (its permissions), with the
// defaults of each preset (compute, backup, gateway, development) and the rules
// for overriding them per machine. Issues #169 (capabilities), #170
// (permissions) and #168 (the development preset) design it; the intended use
// is described in docs/node-types.md and docs/node-type-changes.md.
//
// It models the two keys of exposure (#317): what changes what a machine
// exposes (NeedsConsent, PermissionNeedsConsent) is in an Assignment only when
// the hub assigns it and the machine's own allow list agrees; otherwise it is
// listed as Withheld, never reported by Has or Allows. Unknown names are
// refused.
//
// It does not touch the database, the protocol, the HTTP API or any running
// code: nothing calls it yet, and it reads no allow list itself (the caller
// passes the one the machine reported). Only the consent rule is recorded in
// docs/decisions.md (#317). These are not the user permissions of internal/auth (what a
// person may do on the hub). Names are provisional.
package capability

import (
	"errors"
	"fmt"
	"slices"
)

// Capability is a function a machine does.
type Capability string

// The capabilities listed in #169. Exposure-changing ones need the machine's
// own consent as well as the hub's grant (see NeedsConsent).
const (
	Report            Capability = "report"             // system information, heartbeat, metrics: always on
	SelfUpdate        Capability = "self-update"        // #18
	Inventory         Capability = "inventory"          // read-only machine information, #62
	Workloads         Capability = "workloads"          // #113
	PrivilegedActions Capability = "privileged-actions" // #60
	PluginRuntime     Capability = "plugin-runtime"     // #19
	BackupStorage     Capability = "backup-storage"     // #166
	Relay             Capability = "relay"              // access gateway, #161
	Notifications     Capability = "notifications"      // access gateway, #161
	Authentication    Capability = "authentication"     // access gateway, #161
)

// Permission is something a machine may reach or use.
type Permission string

// The permissions listed in #170.
const (
	ReachHub          Permission = "reach-hub"          // always allowed
	ReachPeers        Permission = "reach-peers"        // direct links to other machines, #113; none exist today
	SharedResources   Permission = "shared-resources"   // what the hub holds for everyone; none exist yet
	EgressNetwork     Permission = "egress-network"     // reaching the network outside the group
	InboundListener   Permission = "inbound-listener"   // accepting connections
	ReceiveBackups    Permission = "receive-backups"    // #166
	UsePrivilegedActs Permission = "privileged-actions" // #60
)

// Preset is a named starting set of capabilities and permissions for an agent.
// The hub and the redundant hub are roles (#104), not presets.
type Preset string

// The presets of docs/node-types.md.
const (
	Compute     Preset = "compute"
	Backup      Preset = "backup"
	Gateway     Preset = "gateway"
	Development Preset = "development"
)

var (
	// ErrUnknownPreset is returned for a preset that does not exist.
	ErrUnknownPreset = errors.New("unknown preset")
	// ErrFixed is returned when an override tries to remove a capability or
	// permission that is always on (Report, ReachHub).
	ErrFixed = errors.New("always on, cannot be removed")
	// ErrNeedsGrant is returned when an override tries to give a development
	// machine a permission that is only available through a delayed grant
	// (#168), never as a setting.
	ErrNeedsGrant = errors.New("only available through a grant")
	// ErrNotAllowed is returned when a preset may never have a capability or
	// permission, whatever the override says.
	ErrNotAllowed = errors.New("not allowed for this preset")
	// ErrUnknown is returned for a capability or permission name that does
	// not exist, in an override or in the machine's allow list.
	ErrUnknown = errors.New("unknown name")
	// ErrNoConsentNeeded is returned when the machine's allow list names a
	// capability that does not need consent: the list holds only those that
	// do (docs/node-type-changes.md).
	ErrNoConsentNeeded = errors.New("does not need the machine's consent")
)

var (
	allCapabilities = []Capability{Report, SelfUpdate, Inventory, Workloads, PrivilegedActions, PluginRuntime, BackupStorage, Relay, Notifications, Authentication}
	allPermissions  = []Permission{ReachHub, ReachPeers, SharedResources, EgressNetwork, InboundListener, ReceiveBackups, UsePrivilegedActs}
)

// Known reports whether c is a capability of this package.
func (c Capability) Known() bool { return slices.Contains(allCapabilities, c) }

// Known reports whether p is a permission of this package.
func (p Permission) Known() bool { return slices.Contains(allPermissions, p) }

// Assignment is what a machine ends up with: the effective set, in which
// everything that needs consent has both keys. All lists are sorted and contain
// no duplicates, so two equal assignments compare equal.
type Assignment struct {
	Capabilities []Capability
	Permissions  []Permission
	// Withheld are the capabilities the hub assigns but the machine's allow
	// list does not ("assigned, not allowed by the node"). They are not in
	// Capabilities and Has reports false for them.
	Withheld []Capability
	// WithheldPermissions are the permissions assigned but without consent
	// (see PermissionNeedsConsent). They are not in Permissions.
	WithheldPermissions []Permission
}

// Has reports whether the assignment includes c. A capability that needs
// consent is included only when the machine allowed it.
func (a Assignment) Has(c Capability) bool { return slices.Contains(a.Capabilities, c) }

// Allows reports whether the assignment includes p. A permission that needs
// consent is included only when the machine allowed what it is for.
func (a Assignment) Allows(p Permission) bool { return slices.Contains(a.Permissions, p) }

// Override changes a preset's defaults for one machine. Deny and Disable are
// applied after Allow and Enable, so removing wins over adding.
type Override struct {
	Enable  []Capability
	Disable []Capability
	Allow   []Permission
	Deny    []Permission
}

// NeedsConsent reports whether turning c on also needs the machine's own
// consent (its allow list, empty by default), because it changes what the
// machine exposes. The hub alone must not be able to make a server open a port
// (#160, #164).
func NeedsConsent(c Capability) bool {
	switch c {
	case Relay, Notifications, Authentication:
		return true
	}
	return false
}

// PermissionNeedsConsent reports whether p changes what the machine exposes.
// The allow list names capabilities only, so such a permission is effective
// only while at least one capability that needs consent is effective (it was
// assigned and allowed): accepting connections is for a relay, notifications
// or authentication, never on the hub's word alone.
func PermissionNeedsConsent(p Permission) bool { return p == InboundListener }

var defaults = map[Preset]Assignment{
	Compute: {
		Capabilities: []Capability{Report, SelfUpdate, Inventory, Workloads, PrivilegedActions, PluginRuntime},
		Permissions:  []Permission{ReachHub, EgressNetwork, UsePrivilegedActs},
	},
	Backup: {
		Capabilities: []Capability{Report, SelfUpdate, Inventory, BackupStorage},
		Permissions:  []Permission{ReachHub, ReceiveBackups},
	},
	Gateway: {
		// Choosing the preset is the hub's key for relay and the listener;
		// they work only once the machine allows relay (Resolve).
		Capabilities: []Capability{Report, SelfUpdate, Inventory, Relay},
		Permissions:  []Permission{ReachHub, InboundListener},
	},
	Development: {
		// Workloads and the network outside the group are limited in a way
		// that is still open in #168, so they are not on by default.
		Capabilities: []Capability{Report, SelfUpdate, Inventory},
		Permissions:  []Permission{ReachHub},
	},
}

// forbidden lists what a preset may never have through an override.
var forbidden = map[Preset]struct {
	caps  []Capability
	perms []Permission
}{
	Development: {
		caps:  []Capability{Relay, Notifications, Authentication, BackupStorage, PrivilegedActions},
		perms: []Permission{InboundListener, ReceiveBackups, UsePrivilegedActs},
	},
}

// grantOnly lists what a preset may have only through a grant, never an override.
var grantOnly = map[Preset][]Permission{
	Development: {ReachPeers, SharedResources},
}

// Defaults returns what a preset gives a machine that allows nothing: the
// preset with no override and an empty allow list, so the gateway's relay and
// listener are Withheld. The result is a copy; changing it does not change
// the defaults.
func Defaults(p Preset) (Assignment, error) {
	return Resolve(p, Override{}, nil)
}

// Resolve applies an override to a preset's defaults, then the machine's
// allow list (its local consent, the second key), and returns the effective
// assignment: effective = assigned ∩ (allowed ∪ what needs no consent).
//
// It refuses an unknown name in the override or the allow list (ErrUnknown),
// an allow list entry that needs no consent (ErrNoConsentNeeded), an override
// that removes what is always on (ErrFixed), or gives a development machine a
// grant-only permission (ErrNeedsGrant) or a forbidden one (ErrNotAllowed).
func Resolve(p Preset, o Override, allowed []Capability) (Assignment, error) {
	d, ok := defaults[p]
	if !ok {
		return Assignment{}, fmt.Errorf("%w: %q", ErrUnknownPreset, p)
	}
	for _, list := range [][]Capability{o.Enable, o.Disable} {
		for _, c := range list {
			if !c.Known() {
				return Assignment{}, fmt.Errorf("%w: capability %q", ErrUnknown, c)
			}
		}
	}
	for _, list := range [][]Permission{o.Allow, o.Deny} {
		for _, perm := range list {
			if !perm.Known() {
				return Assignment{}, fmt.Errorf("%w: permission %q", ErrUnknown, perm)
			}
		}
	}
	for _, c := range allowed {
		if !c.Known() {
			return Assignment{}, fmt.Errorf("%w: allowed capability %q", ErrUnknown, c)
		}
		if !NeedsConsent(c) {
			return Assignment{}, fmt.Errorf("%w: %s", ErrNoConsentNeeded, c)
		}
	}
	if slices.Contains(o.Disable, Report) {
		return Assignment{}, fmt.Errorf("%w: %s", ErrFixed, Report)
	}
	if slices.Contains(o.Deny, ReachHub) {
		return Assignment{}, fmt.Errorf("%w: %s", ErrFixed, ReachHub)
	}
	for _, c := range o.Enable {
		if slices.Contains(forbidden[p].caps, c) {
			return Assignment{}, fmt.Errorf("%w: capability %s on %s", ErrNotAllowed, c, p)
		}
	}
	for _, perm := range o.Allow {
		if slices.Contains(grantOnly[p], perm) {
			return Assignment{}, fmt.Errorf("%w: permission %s on %s", ErrNeedsGrant, perm, p)
		}
		if slices.Contains(forbidden[p].perms, perm) {
			return Assignment{}, fmt.Errorf("%w: permission %s on %s", ErrNotAllowed, perm, p)
		}
	}

	// The hub's key: the preset plus the override, removing winning.
	caps := append(slices.Clone(d.Capabilities), o.Enable...)
	caps = slices.DeleteFunc(caps, func(c Capability) bool { return slices.Contains(o.Disable, c) })
	perms := append(slices.Clone(d.Permissions), o.Allow...)
	perms = slices.DeleteFunc(perms, func(perm Permission) bool { return slices.Contains(o.Deny, perm) })

	// The machine's key.
	var a Assignment
	exposed := false
	for _, c := range caps {
		switch {
		case !NeedsConsent(c):
			a.Capabilities = append(a.Capabilities, c)
		case slices.Contains(allowed, c):
			a.Capabilities = append(a.Capabilities, c)
			exposed = true
		default:
			a.Withheld = append(a.Withheld, c)
		}
	}
	for _, perm := range perms {
		if PermissionNeedsConsent(perm) && !exposed {
			a.WithheldPermissions = append(a.WithheldPermissions, perm)
			continue
		}
		a.Permissions = append(a.Permissions, perm)
	}
	a.Capabilities = sortCompact(a.Capabilities)
	a.Permissions = sortCompact(a.Permissions)
	a.Withheld = sortCompact(a.Withheld)
	a.WithheldPermissions = sortCompact(a.WithheldPermissions)
	return a, nil
}

func sortCompact[T ~string](s []T) []T {
	slices.Sort(s)
	return slices.Compact(s)
}
