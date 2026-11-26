package plugin

import "strings"

// Op is one thing a plugin's page may ask the hub for through the bridge
// (POST /api/plugins/{id}/call, #261). The list is fixed in the core: a plugin
// cannot add an operation, and every operation is read-only.
type Op struct {
	// Name is what the page sends: "<area>.<what>".
	Name string
	// Permission is the plugin permission it needs, without the plugin id:
	// the plugin's manifest must request "<id>.<Permission>".
	Permission string
	// Core is the permission the logged-in person needs (internal/auth). The
	// narrower of the two wins: both are required.
	Core string
}

// Ops is the whole allowlist. A test in internal/hub checks that Core names
// real permissions (this package does not import internal/auth).
var Ops = []Op{
	{Name: "inventory.disks", Permission: "read", Core: "inventory.read"},
	{Name: "inventory.disk_moves", Permission: "read", Core: "inventory.read"},
}

// LookupOp finds an operation by name.
func LookupOp(name string) (Op, bool) {
	for _, o := range Ops {
		if o.Name == name {
			return o, true
		}
	}
	return Op{}, false
}

// PluginPermission is the id of the permission the plugin must request.
func (o Op) PluginPermission(pluginID string) string { return pluginID + "." + o.Permission }

// Requests reports whether the manifest asks for the permission. This is what
// the plugin REQUESTED: an administrator does not approve it when installing
// yet (#61).
func (m Manifest) Requests(permission string) bool {
	for _, p := range m.Permissions {
		if strings.EqualFold(p.ID, permission) {
			return true
		}
	}
	return false
}
