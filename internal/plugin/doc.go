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
//     after its id.
//   - supervisor.go: starts the component for this role of each plugin as its
//     own process, checks that it is the plugin the manifest names, asks its
//     health, restarts it when it stops, and gives up on one that keeps
//     failing. A plugin never runs inside the host's process, so its crash
//     cannot take the host down.
//   - sdk/: what a plugin written in Go uses to serve the contract.
//
// It does not install, update or remove plugins, check their signatures, or
// give them any permission (regulus-plugins#1); it does not load dashboard widgets (#21).
// The design is in docs/plugins/.
package plugin
