// Package store is the plugin store's client (docs/plugins/store.md): it reads
// the signed index of each source, checks it, says for every plugin version
// whether this hub can run it, and installs, updates and removes plugins.
//
// It does, on the hub: parse and validate an index of format 1 (a format it
// does not speak, an index that has expired and one older than the last
// verified are refused), verify its Sigstore signature through a Verifier,
// filter versions by the channel a source is followed on, check each version
// against this Regulus (its release, the plugin API range) and this machine
// (an artifact for its os and arch), build the catalogue that GET /api/store
// serves, and, for install, update and remove (Installer), pick the version,
// download the package, check its size and SHA-256 against the signed index,
// stage it and ask the root helper. While it waits it keeps where the operation
// is (Progress: downloading with a percentage, then the step the helper says it
// is in) for GET /api/store/progress; the record is in memory and a finished
// one is dropped after a minute.
//
// It does, as the root helper (Applier, run by `regulus plugin-apply`): verify
// the signed index and the package again, refuse an unsafe package, unpack it
// into <plugins dir>/<id>/<version>/, switch the link current, record the
// source in source.json, or remove a plugin. It also says which step it is in
// (verifying, installing, removing) in progress.json, for information only: a
// failure to write it never stops a change.
//
// It does not: run a plugin (internal/plugin), decide who may ask (the hub does
// that with a permission and a recent proof of the password), test a plugin after
// installing it, roll back, pin a
// version, check the requirements between plugins, update by itself (the
// automatic mode), or add a source from the dashboard. The helper does not
// check that an index is not older than the last one the hub verified.
package store
