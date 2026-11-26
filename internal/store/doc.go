// Package store is the read side of the plugin store (docs/plugins/store.md):
// it reads the signed index of each source, checks it, and says for every
// plugin version whether this hub can run it.
//
// It does: parse and validate an index of format 1 (a format it does not speak,
// an index that has expired and one older than the last verified are refused),
// verify its Sigstore signature through a Verifier, filter versions by the
// channel a source is followed on, check each version against this Regulus (its
// release, the plugin API range) and this machine (an artifact for its os and
// arch), and build the catalogue that GET /api/store serves.
//
// It does not: download or install a plugin, write the plugins directory, run
// anything, or decide who may ask (the hub does that with a permission).
// Installing needs the root helper, which is not built; the install route
// answers 501.
package store
