# Plugins

A plugin adds features to Regulus: on the servers (agent side), on the hub, and in the dashboard. It is a **separate program**, released on its own, that Regulus starts and talks to over gRPC on a local Unix socket. A plugin that crashes or hangs ends its own process, never the agent's or the hub's.

A plugin is **code that extends Regulus**. It is not how third-party software such as Plex or Radarr is installed (those are **apps**, which run in containers and need no plugin), and it is not the same as a **manager** (a function of a server such as the firewall or RAID, which a plugin may implement). See [Managers, backends and apps](../managers-and-apps.md). The words *extension* and *add-on* are not used.

This folder is the guide to the plugin system. Read the pages in order:

| Page | What it covers | Status |
|---|---|---|
| [Manifest](manifest.md) | `plugin.yaml`: id, version, plugin API, the Regulus versions it needs, components, permissions | implemented (#19) |
| [Runtime](runtime.md) | how Regulus finds, starts, checks, restarts and stops plugins; the contract on the socket; limits and security; where their state is shown | implemented on the agent (#19) and the hub (#262) |
| [Writing a plugin](writing-a-plugin.md) | the example plugin, making your own step by step, the SDK for Go, other languages, packaging | implemented (#19, #22) |

The code is in `internal/plugin` (manifest, discovery, supervisor), `internal/plugin/sdk` (for plugins written in Go), `proto/regulus/plugin/v1` (the contract) and `plugins/example-hello` (the example).

## What exists and what does not

| Part | Issue | Status |
|---|---|---|
| Manifest format, validation, version ranges | #19 | **implemented**, unit tests |
| Discovery of the plugins directory | #19 | **implemented**, unit tests |
| Runtime: separate processes, gRPC on a Unix socket, identity check, health checks, restart with backoff, giving up, stopping the process group | #19 | **implemented** for the agent (`plugins_dir`), tests with real processes; **not verified** on a real server (no `[Test]` run yet) |
| Running plugins on the hub | #262 | **implemented** (`plugins_dir` of the hub), tests with real processes; **not verified** on an installed hub |
| State of the plugins outside the log | #262 | **implemented**: `GET /api/plugins` (hub and agents), the console's Status tab and `regulus status` (hub); agents report theirs with their metrics. The dashboard page is not built yet |
| The host side of plugin features (what a plugin can ask Regulus for, how its data reaches the dashboard) | #19 follow-up | **not designed**: today a plugin can only prove it runs and is healthy |
| Index, installation, updates, removal, signatures, per-plugin update mode | #20 | designed below, not implemented |
| Dashboard widgets loaded at runtime | #21 | designed below, not implemented (the manifest already accepts `components.ui`) |
| Example plugin and developer guide | #22 | **done as far as the system goes**: the example (agent and hub component, state in its data directory, health, a provisional widget), a step-by-step guide and a packaging script. A *real* plugin that does something for the dashboard needs the host side (open question 1) and the widget loader (#21) |
| Third-party plugins | #23 | after the internal system is stable |
| Skills for the AI assistant | #68 | `skills` is not accepted in the manifest yet |

## Core and plugins are versioned separately

The **core** (hub, agent, dashboard) has one version number and one release (`vX.Y.Z`). Each **plugin** has its own version and is released on its own, with tags of the form `plugin/<id>/vX.Y.Z` *(proposed)*. A plugin can therefore be updated without updating the core, and the other way round. The release listing of the core already ignores those tags and reads further pages when plugin releases fill the first one (#213).

Two numbers tie them together, both checked when a plugin starts:

- **`api`**: the plugin API version the plugin speaks, the contract in `proto/regulus/plugin/v1` and the meaning of `plugin.yaml`. Regulus runs plugins whose `api` is between `plugin.MinAPIVersion` and `plugin.APIVersion` (both 1 today). See [Versioning](../versioning.md).
- **`requires`**: the range of Regulus releases the plugin works with, for example `">=0.3.0 <0.5.0"`.

## Index, installation and updates (#20, design)

- CI publishes an `index.json` listing each plugin, its versions, **checksums** and **compatibility** (`api`, `requires`). The format has a `source` field so other sources can be added later (#23).
- The hub installs, updates and removes plugins from the dashboard, shows the requested permissions before installing, verifies signatures and distributes the plugin to the agents that need it, over the agents' existing connection, like the core's updates.
- The same three update modes as the core (`manual`, `download`, `automatic`), with a per-plugin override.
- A plugin's version can be **pinned**, and the previous version is kept for a **rollback**.
- *Proposed* layout on disk once versions are kept side by side: `<plugins dir>/<id>/<version>/` with `<id>/current` naming the one that runs. Today a plugin is `<plugins dir>/<id>/` (see [Runtime](runtime.md)); the change is for #20 to make.

## Dashboard widgets (#21, design)

Plugins ship widgets as JavaScript modules (`components.ui.module` in the manifest), served by the hub and registered at runtime. A broken widget only breaks its own frame (the dashboard already isolates widgets, see [Dashboard](../dashboard.md)).

## Repository layout (#22)

Internal plugins live in `plugins/<id>/` in this repository, with the manifest and the source next to each other. The first one is `plugins/example-hello`.

## Third-party plugins (#23)

Later, after the internal plugin system is stable: extra index sources, an **allowlist of trusted signers**, warnings for unsigned plugins and sandboxed permissions. Until then, formats are kept ready for it.

## Open questions

1. **What a plugin can do.** The contract has only `Describe` and `Health`. What a plugin offers (metrics for the dashboard, commands an administrator runs, data the hub stores) and what it may ask of the host (a key-value store, the hub's audit log, the agent's connection) is the next design step. It must say how the permissions of the manifest are enforced.
2. **Dependencies between plugins.** What happens when a plugin that others depend on (through `provides`) is updated or removed? Block, warn, or update the dependents together? #20 does not say yet. `provides` is accepted but unused.
3. **Tag format.** Is `plugin/<id>/vX.Y.Z` confirmed, and does the release workflow publish plugins from those tags?
4. **Plugin signatures.** Do internal plugins use the same Sigstore keyless signature as core releases?
5. **Isolation.** A plugin runs as the same user as the agent, with the same systemd sandbox. A user per plugin, or a stricter sandbox, is open (see [Runtime](runtime.md), "Security").
6. **Skill parameters.** JSON Schema is the candidate (#68); also undecided: several risk levels per skill, and how the assistant discovers a server's skills.
