// Package plugin finds, checks and runs plugins: separate programs that add
// features to an agent or a hub (each starts a Supervisor with its role) and talk to it over gRPC on a local Unix
// socket (proto/regulus/plugin/v1).
//
// The pieces, in the order a plugin goes through them:
//
//   - manifest.go: plugin.yaml, read strictly and validated (id, version,
//     plugin API, the core versions it needs, its components and the
//     permissions it asks for).
//   - discover.go: the plugins directory, one directory per plugin, named
//     after its id, either with plugin.yaml at its top or (the layout of the
//     store) with a link current to the version directory that holds it.
//   - installed.go: that layout, source.json (where an installed plugin came
//     from) and the list of what is on disk, running or not.
//   - supervisor.go: starts the component for this role of each plugin as its
//     own process, checks that it is the plugin the manifest names, asks its
//     health, restarts it when it stops, and gives up on one that keeps
//     failing. A plugin never runs inside the host's process, so its crash
//     cannot take the host down.
//   - ui.go: the plugins that have a dashboard page: finds one through
//     <id>/current or <id>, reads its module without leaving the plugin
//     directory, and builds the HTML page and the Content-Security-Policy for the
//     sandboxed frame it runs in (#21).
//   - bridge.go: the fixed list of read-only operations a plugin's page may ask
//     the hub for, with the permissions each needs (#261, minimal).
//   - sdk/: what a plugin written in Go uses to serve the contract.
//
// It does not install, update or remove plugins (internal/store does, through
// a root helper), check their signatures, or give them any permission; it does
// not run the operations of the bridge (package hub does) or draw the
// dashboard's side of the frame.
// The design is in docs/plugins/.
package plugin
