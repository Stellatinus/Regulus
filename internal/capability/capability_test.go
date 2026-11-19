package capability_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/CodexCircle/Regulus/internal/capability"
)

var presets = []capability.Preset{capability.Compute, capability.Backup, capability.Gateway, capability.Development}

func mustDefaults(t *testing.T, p capability.Preset) capability.Assignment {
	t.Helper()
	a, err := capability.Defaults(p)
	if err != nil {
		t.Fatalf("Defaults(%s): %v", p, err)
	}
	return a
}

func TestEveryPresetHasTheAlwaysOnPieces(t *testing.T) {
	for _, p := range presets {
		a := mustDefaults(t, p)
		if !a.Has(capability.Report) || !a.Has(capability.SelfUpdate) || !a.Has(capability.Inventory) {
			t.Errorf("%s: missing report, self-update or inventory: %v", p, a.Capabilities)
		}
		if !a.Allows(capability.ReachHub) {
			t.Errorf("%s: cannot reach the hub", p)
		}
	}
}

func TestNoPresetReachesPeersOrSharedResourcesByDefault(t *testing.T) {
	// Nodes never contact each other today, and no shared resource exists:
	// both are denied by default for every preset (docs/node-types.md).
	for _, p := range presets {
		a := mustDefaults(t, p)
		if a.Allows(capability.ReachPeers) || a.Allows(capability.SharedResources) {
			t.Errorf("%s: reach-peers or shared-resources on by default: %v", p, a.Permissions)
		}
	}
}

func TestPresetSpecifics(t *testing.T) {
	cases := []struct {
		preset  capability.Preset
		has     []capability.Capability
		hasNot  []capability.Capability
		allows  []capability.Permission
		denies  []capability.Permission
		comment string
	}{
		{capability.Compute, []capability.Capability{capability.Workloads, capability.PluginRuntime, capability.PrivilegedActions},
			[]capability.Capability{capability.BackupStorage, capability.Relay},
			[]capability.Permission{capability.EgressNetwork}, []capability.Permission{capability.InboundListener, capability.ReceiveBackups}, "standard machine"},
		{capability.Backup, []capability.Capability{capability.BackupStorage},
			[]capability.Capability{capability.Workloads, capability.Relay, capability.PrivilegedActions},
			[]capability.Permission{capability.ReceiveBackups}, []capability.Permission{capability.EgressNetwork, capability.InboundListener}, "hub only"},
		{capability.Gateway, nil,
			[]capability.Capability{capability.Relay, capability.Notifications, capability.Authentication, capability.Workloads},
			nil, []capability.Permission{capability.InboundListener, capability.ReceiveBackups}, "nothing exposed until the machine allows it"},
		{capability.Development, nil,
			[]capability.Capability{capability.Workloads, capability.PrivilegedActions, capability.Relay, capability.BackupStorage},
			nil, []capability.Permission{capability.EgressNetwork, capability.InboundListener, capability.UsePrivilegedActs}, "isolated"},
	}
	for _, c := range cases {
		a := mustDefaults(t, c.preset)
		for _, x := range c.has {
			if !a.Has(x) {
				t.Errorf("%s (%s): should have %s", c.preset, c.comment, x)
			}
		}
		for _, x := range c.hasNot {
			if a.Has(x) {
				t.Errorf("%s (%s): should not have %s", c.preset, c.comment, x)
			}
		}
		for _, x := range c.allows {
			if !a.Allows(x) {
				t.Errorf("%s (%s): should allow %s", c.preset, c.comment, x)
			}
		}
		for _, x := range c.denies {
			if a.Allows(x) {
				t.Errorf("%s (%s): should not allow %s", c.preset, c.comment, x)
			}
		}
	}
}

func TestDefaultsAreSortedCopies(t *testing.T) {
	a := mustDefaults(t, capability.Compute)
	if !slices.IsSorted(a.Capabilities) || !slices.IsSorted(a.Permissions) {
		t.Errorf("defaults not sorted: %v %v", a.Capabilities, a.Permissions)
	}
	a.Capabilities[0] = "tampered"
	if b := mustDefaults(t, capability.Compute); slices.Contains(b.Capabilities, "tampered") {
		t.Error("changing a result changed the defaults")
	}
}

func TestUnknownPreset(t *testing.T) {
	if _, err := capability.Defaults("nope"); !errors.Is(err, capability.ErrUnknownPreset) {
		t.Errorf("Defaults: %v, want ErrUnknownPreset", err)
	}
	if _, err := capability.Resolve("nope", capability.Override{}, nil); !errors.Is(err, capability.ErrUnknownPreset) {
		t.Errorf("Resolve: %v, want ErrUnknownPreset", err)
	}
}

func TestResolveWithoutOverrideEqualsDefaults(t *testing.T) {
	for _, p := range presets {
		got, err := capability.Resolve(p, capability.Override{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := mustDefaults(t, p)
		if !slices.Equal(got.Capabilities, want.Capabilities) || !slices.Equal(got.Permissions, want.Permissions) {
			t.Errorf("%s: %v != %v", p, got, want)
		}
	}
}

func TestResolveOneBinaryAnyMachine(t *testing.T) {
	// A standard machine can be given backup storage: it is the same binary.
	a, err := capability.Resolve(capability.Compute, capability.Override{
		Enable: []capability.Capability{capability.BackupStorage, capability.BackupStorage},
		Allow:  []capability.Permission{capability.ReceiveBackups},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Has(capability.BackupStorage) || !a.Allows(capability.ReceiveBackups) || !a.Has(capability.Workloads) {
		t.Errorf("unexpected result: %v", a)
	}
	if !slices.IsSorted(a.Capabilities) || len(slices.Compact(slices.Clone(a.Capabilities))) != len(a.Capabilities) {
		t.Errorf("not sorted or has duplicates: %v", a.Capabilities)
	}
}

func TestResolveRemovingWinsOverAdding(t *testing.T) {
	a, err := capability.Resolve(capability.Compute, capability.Override{
		Enable:  []capability.Capability{capability.Relay},
		Disable: []capability.Capability{capability.Relay, capability.Workloads},
		Deny:    []capability.Permission{capability.EgressNetwork},
	}, []capability.Capability{capability.Relay})
	if err != nil {
		t.Fatal(err)
	}
	if a.Has(capability.Relay) || a.Has(capability.Workloads) || a.Allows(capability.EgressNetwork) {
		t.Errorf("removal did not win: %v", a)
	}
}

func TestResolveRefusals(t *testing.T) {
	cases := []struct {
		name    string
		preset  capability.Preset
		o       capability.Override
		allowed []capability.Capability
		want    error
	}{
		{"cannot disable report", capability.Compute, capability.Override{Disable: []capability.Capability{capability.Report}}, nil, capability.ErrFixed},
		{"cannot deny the hub", capability.Backup, capability.Override{Deny: []capability.Permission{capability.ReachHub}}, nil, capability.ErrFixed},
		{"development cannot be a gateway", capability.Development, capability.Override{Enable: []capability.Capability{capability.Relay}}, nil, capability.ErrNotAllowed},
		{"development cannot listen", capability.Development, capability.Override{Allow: []capability.Permission{capability.InboundListener}}, nil, capability.ErrNotAllowed},
		{"development needs a grant for shared resources", capability.Development, capability.Override{Allow: []capability.Permission{capability.SharedResources}}, nil, capability.ErrNeedsGrant},
		{"development needs a grant to reach peers", capability.Development, capability.Override{Allow: []capability.Permission{capability.ReachPeers}}, nil, capability.ErrNeedsGrant},
		{"unknown capability enabled", capability.Compute, capability.Override{Enable: []capability.Capability{"teleport"}}, nil, capability.ErrUnknown},
		{"unknown capability disabled", capability.Compute, capability.Override{Disable: []capability.Capability{"Relay"}}, nil, capability.ErrUnknown},
		{"unknown permission allowed", capability.Compute, capability.Override{Allow: []capability.Permission{"root"}}, nil, capability.ErrUnknown},
		{"unknown permission denied", capability.Compute, capability.Override{Deny: []capability.Permission{""}}, nil, capability.ErrUnknown},
		{"unknown name in the allow list", capability.Gateway, capability.Override{}, []capability.Capability{"gateway"}, capability.ErrUnknown},
		{"allow list names a capability without consent", capability.Gateway, capability.Override{}, []capability.Capability{capability.Workloads}, capability.ErrNoConsentNeeded},
	}
	for _, c := range cases {
		if _, err := capability.Resolve(c.preset, c.o, c.allowed); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

func TestNeedsConsent(t *testing.T) {
	for _, c := range []capability.Capability{capability.Relay, capability.Notifications, capability.Authentication} {
		if !capability.NeedsConsent(c) {
			t.Errorf("%s should need the machine's consent", c)
		}
	}
	for _, c := range []capability.Capability{capability.Report, capability.SelfUpdate, capability.BackupStorage, capability.Workloads} {
		if capability.NeedsConsent(c) {
			t.Errorf("%s should not need the machine's consent", c)
		}
	}
}

func TestPermissionNeedsConsent(t *testing.T) {
	if !capability.PermissionNeedsConsent(capability.InboundListener) {
		t.Error("inbound-listener should need the machine's consent")
	}
	for _, p := range []capability.Permission{capability.ReachHub, capability.EgressNetwork, capability.ReceiveBackups} {
		if capability.PermissionNeedsConsent(p) {
			t.Errorf("%s should not need the machine's consent", p)
		}
	}
}

// The two keys of docs/node-types.md: what changes what a machine exposes is
// effective only when the hub assigns it and the machine allows it (#317).
func TestExposureNeedsBothKeys(t *testing.T) {
	relay := []capability.Capability{capability.Relay}

	// The hub's key alone: the gateway preset without the machine's consent.
	a, err := capability.Resolve(capability.Gateway, capability.Override{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Has(capability.Relay) || a.Allows(capability.InboundListener) {
		t.Errorf("exposure without consent: %+v", a)
	}
	if !slices.Equal(a.Withheld, relay) || !slices.Equal(a.WithheldPermissions, []capability.Permission{capability.InboundListener}) {
		t.Errorf("withheld = %v %v, want relay and inbound-listener", a.Withheld, a.WithheldPermissions)
	}

	// Both keys.
	a, err = capability.Resolve(capability.Gateway, capability.Override{}, relay)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Has(capability.Relay) || !a.Allows(capability.InboundListener) || len(a.Withheld) != 0 || len(a.WithheldPermissions) != 0 {
		t.Errorf("gateway with relay allowed: %+v", a)
	}

	// The machine's key alone: allowing relay on a compute machine the hub
	// did not make a gateway turns nothing on.
	a, err = capability.Resolve(capability.Compute, capability.Override{}, relay)
	if err != nil {
		t.Fatal(err)
	}
	if a.Has(capability.Relay) || a.Allows(capability.InboundListener) {
		t.Errorf("consent without the hub's key: %+v", a)
	}

	// Allowing relay does not consent to the gateway's other capabilities.
	a, err = capability.Resolve(capability.Gateway, capability.Override{
		Enable: []capability.Capability{capability.Notifications, capability.Authentication},
	}, relay)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Has(capability.Relay) || a.Has(capability.Notifications) || a.Has(capability.Authentication) {
		t.Errorf("consent leaked from relay: %+v", a)
	}
	if !slices.Equal(a.Withheld, []capability.Capability{capability.Authentication, capability.Notifications}) {
		t.Errorf("withheld = %v", a.Withheld)
	}

	// The listener cannot be given on its own: with no exposure capability
	// effective, it stays withheld whatever the override says.
	a, err = capability.Resolve(capability.Compute, capability.Override{
		Allow: []capability.Permission{capability.InboundListener},
	}, relay)
	if err != nil {
		t.Fatal(err)
	}
	if a.Allows(capability.InboundListener) || !slices.Contains(a.WithheldPermissions, capability.InboundListener) {
		t.Errorf("listener without an exposure capability: %+v", a)
	}

	// An override that removes relay wins, and the listener goes with it.
	a, err = capability.Resolve(capability.Gateway, capability.Override{Disable: relay}, relay)
	if err != nil {
		t.Fatal(err)
	}
	if a.Has(capability.Relay) || a.Allows(capability.InboundListener) || len(a.Withheld) != 0 {
		t.Errorf("relay disabled on the hub: %+v", a)
	}
}

// No preset, whatever the allow list, reports an exposure capability or
// permission without consent.
func TestNoPresetExposesWithoutConsent(t *testing.T) {
	for _, p := range presets {
		a := mustDefaults(t, p)
		for _, c := range []capability.Capability{capability.Relay, capability.Notifications, capability.Authentication} {
			if a.Has(c) {
				t.Errorf("%s: %s on without consent", p, c)
			}
		}
		if a.Allows(capability.InboundListener) {
			t.Errorf("%s: inbound-listener without consent", p)
		}
	}
}
