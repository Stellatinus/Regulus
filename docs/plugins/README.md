# Plugins

A plugin adds features to Regulus: on the servers (agent side), on the hub, and in the dashboard. It is a **separate program**, released on its own, that Regulus starts and talks to over gRPC on a local Unix socket. A plugin that crashes or hangs ends its own process, never the agent's or the hub's.

A plugin is **code that extends Regulus**. It is not how third-party software such as Plex or Radarr is installed (those are **apps**, which run in containers and need no plugin), and it is not the same as a **manager** (a function of a server such as the firewall or RAID, which a plugin may implement). See [Managers, backends and apps](../managers-and-apps.md). The words *extension* and *add-on* are not used.

This folder is the guide to the plugin system. Read the pages in order:

| Page | What it covers | Status |
|---|---|---|
| [Manifest](manifest.md) | `plugin.yaml`: id, version, plugin API, the Regulus versions it needs, components, permissions | implemented (#19) |
| [Runtime](runtime.md) | how Regulus finds, starts, checks, restarts and stops plugins; the contract on the socket; limits and security; where their state is shown | implemented on the agent (#19) and the hub (#262) |
| [Writing a plugin](writing-a-plugin.md) | the example plugin, making your own step by step, the SDK for Go, other languages, packaging | implemented (#19, regulus-plugins#2) |
| [The plugin store](store.md) | what lives in which repository, channels and versions, the development repository, the index, requirements between plugins, updates, kinds and audiences, other sources | **design**; the read side, **install, update and remove** (hub only, root helper) are built, **not verified** on a real hub; rollback, pinning, requirements and automatic mode are not (regulus-plugins#1, regulus-plugins#3) |

**Where the issues are.** The plugins themselves and the catalog live in their own repository, [Stellatinus/regulus-plugins](https://github.com/Stellatinus/regulus-plugins); the **store client** (the dashboard page, the index reader, installation) lives here, with the code ([the plugin store](store.md)). The issues about the store moved there on 2026-11-25: the lifecycle, index and installation (formerly #20) is `regulus-plugins#1`, the example plugin and guide (formerly #22) `regulus-plugins#2`, third-party plugins (formerly #23) `regulus-plugins#3`, and the plugin for free-tier cloud servers (formerly #163) `regulus-plugins#4`. The runtime, the contract, the SDK, the host side of plugins (#261), the isolation (#267), the dashboard widgets (#21) and the tests of the runtime stay here, with the code. A reference written `regulus-plugins#N` is an issue of the other repository.

The code is in `internal/plugin` (manifest, discovery, supervisor), `internal/plugin/sdk` (for plugins written in Go), `proto/regulus/plugin/v1` (the contract) and `plugins/example-hello` (the example).

> **Under review (2026-11-26).** The maintainer decided that plugins are installed on the **hubs only**, and that the redundant hub has the same plugins ([#496](https://github.com/Stellatinus/Regulus/issues/496), [Direction of 2026-11-26](../ui-and-apps-direction.md)). This page still describes a plugin that can run on an agent, and the store sending plugins to agents; that part is not settled and is not to be built before #496 decides what becomes of the agent component.

## What exists and what does not

| Part | Issue | Status |
|---|---|---|
| Manifest format, validation, version ranges | #19 | **implemented**, unit tests |
| Discovery of the plugins directory | #19 | **implemented**, unit tests |
| Runtime: separate processes, gRPC on a Unix socket, identity check, health checks, restart with backoff, giving up, stopping the process group | #19 | **implemented** for the agent (`plugins_dir`), tests with real processes; **not verified** on a real server (no `[Test]` run yet) |
| Running plugins on the hub | #262 | **implemented** (`plugins_dir` of the hub), tests with real processes; **not verified** on an installed hub |
| State of the plugins outside the log | #262 | **implemented**: `GET /api/plugins` (hub and agents), the console's Status tab and `regulus status` (hub); agents report theirs with their metrics; and the dashboard's **Plugins** page (`dashboard/src/PluginsPage.tsx`), which reads `GET /api/plugins` again when the hub pushes `plugins.changed`. The page was opened on a real hub with no plugin directory on 2026-11-24 and said so; it has **not been checked** with a running plugin on an installed hub (#310, #327) |
| The host side of plugin features (what a plugin can ask Regulus for, how its data reaches the dashboard) | #19 follow-up | **not designed**: today a plugin can only prove it runs and is healthy |
| Index, signatures, installation, update and removal (hub only, root helper `regulus plugin-apply`) | regulus-plugins#1 | **implemented** (2026-11-26), tests with a fake signed index and the helper in-process; **not verified** as root under systemd or against a real release. Not built: rollback, pin, requirements between plugins, the automatic mode, per-plugin update mode |
| Dashboard widgets loaded at runtime | #21 | **minimal version implemented**: a plugin's module is served by the hub and shown in a sandboxed frame as a **tab of the node window** (`components.ui.node_tab`); other places for widgets (the Dashboard grid) are not built. Not verified on an installed hub: the frame mechanics were checked in headless Chromium against a stand-in server (see [Runtime](runtime.md#the-page-of-a-plugin-in-the-dashboard-21-261)) |
| What a plugin's page can read from the hub | #261 | **minimal read door implemented**: `POST /api/plugins/{id}/call` with a fixed list of two read-only operations (`inventory.disks`, `inventory.disk_moves`), built for Disks. **This is not the host API of #261**: a plugin program still cannot call the hub, store data or draw its own widgets, and the list grows by a change of the core |
| Example plugin and developer guide | regulus-plugins#2 | **done as far as the system goes**: the example (agent and hub component, state in its data directory, health, a provisional widget), a step-by-step guide and a packaging script. A *real* plugin that does something for the dashboard needs the host side (open question 1) and the widget loader (#21) |
| Third-party plugins | regulus-plugins#3 | after the internal system is stable |
| Skills for the AI assistant | #68 | `skills` is not accepted in the manifest yet |

## Core and plugins are versioned separately

The **core** (hub, agent, dashboard) has one version number and one release (`vX.Y.Z`). Each **plugin** has its own version and is released on its own, with tags of the form `plugin/<id>/vX.Y.Z` *(proposed)*. A plugin can therefore be updated without updating the core, and the other way round. The release listing of the core already ignores those tags and reads further pages when plugin releases fill the first one (#213).

Two numbers tie them together, both checked when a plugin starts:

- **`api`**: the plugin API version the plugin speaks, the contract in `proto/regulus/plugin/v1` and the meaning of `plugin.yaml`. Regulus runs plugins whose `api` is between `plugin.MinAPIVersion` and `plugin.APIVersion` (both 1 today). See [Versioning](../versioning.md).
- **`requires`**: the range of Regulus releases the plugin works with, for example `">=0.3.0 <0.5.0"`.

## Index, installation and updates (regulus-plugins#1, design)

The full design is [the plugin store](store.md); this is the short version.

- CI publishes an `index.json` listing each plugin, its versions, **checksums** and **compatibility** (`api`, `requires`). The format has a `source` field so other sources can be added later (regulus-plugins#3).
- The hub installs, updates and removes plugins from the dashboard, shows the requested permissions before installing, verifies signatures and distributes the plugin to the agents that need it, over the agents' existing connection, like the core's updates.
- The same three update modes as the core (`manual`, `download`, `automatic`), with a per-plugin override.
- A plugin's version can be **pinned**, and the previous version is kept for a **rollback**.
- *Proposed* layout on disk once versions are kept side by side: `<plugins dir>/<id>/<version>/` with `<id>/current` naming the one that runs. Today a plugin is `<plugins dir>/<id>/` (see [Runtime](runtime.md)); the change is for regulus-plugins#1 to make.

## Dashboard widgets (#21)

**What is built (2026-11-26):** one place for a plugin's UI, a **tab of the node window** (`components.ui.node_tab`), and the read door of the next section. The rest of this section is the design it follows; where the build differs it says so ("Built differently").

Plugins ship widgets as JavaScript modules (`components.ui.module` in the manifest), served by the hub and registered at runtime. A broken widget only breaks its own frame (the dashboard already isolates widgets, see [Dashboard](../dashboard.md)): that is an error boundary, which keeps a crash local, **not a security boundary**.

### How widget code is isolated from the session (#315)

A module served from the hub's own origin passes the Content-Security-Policy and would run with the viewer's cookie, so it could call every API the viewer can (create users, reset two-factor, start updates). The manifest's `permissions` cannot be enforced on code that has that power. So, before any widget loader exists:

- **A widget never runs in the dashboard's page.** It runs in an `<iframe sandbox="allow-scripts">` **without** `allow-same-origin`, which gives it an opaque origin: no cookie, no `localStorage`, no access to the parent's DOM, and `fetch` to the hub arrives without the session. The module is served with its own response header `Content-Security-Policy: sandbox allow-scripts; default-src 'none'; script-src 'self'` so that opening its URL directly gives it the same empty origin, and with `connect-src 'none'`: the frame cannot call out. *(Built: `GET /api/plugins/{id}/module` sends this header; the page for the frame is the inlined one, below.)*
- **The only door is `postMessage`.** The dashboard answers a small set of messages from the frame (read the metric series the widget declared, read its own plugin's data, show a notification) and forwards each one as an ordinary API call **made by the dashboard for the plugin id**, which the hub checks against the permissions of the manifest the administrator approved, and against the viewer's own permissions as well (the narrower of the two wins). Nothing in this API writes, until a later design adds write operations with their own approval (#261).
- **No exception for first-party plugins.** A signed first-party plugin would be easier to run in the page, but that makes the rule depend on a list and a signature that regulus-plugins#3 has not designed; one mechanism for every plugin keeps the example plugin (regulus-plugins#2) honest.
- **Cost:** a widget cannot share a stylesheet or a React tree with the dashboard. It gets the theme as a few CSS variables sent in the first message and a size from the frame. #21 decides the message format; this section decides that it is not the page.

**Built differently from the paragraph above.** The module is **not** fetched by the frame (an opaque origin sends no cookie, so it could not authenticate). The hub serves **one HTML page with the module inlined** (`GET /api/plugins/{id}/ui`), with its own policy that allows the two inline scripts by hash and no network, and the dashboard puts that page in the frame; so a module is one self-contained file. The dashboard's own policy does not apply to that page (a frame loaded from a URL has its own), and the page can be framed only by the hub's own origin. Only the dashboard, as the logged-in person, calls the hub, through `POST /api/plugins/{id}/call`.

[Decisions](../decisions.md) records the choice. #21 and #261 must follow it.

## Repository layout (regulus-plugins#2)

`plugins/example-hello` stays in this repository: the tests of the runtime build and run it. The plugins that are released through the store live in [regulus-plugins](https://github.com/Stellatinus/Regulus-Plugins), in `plugins/<id>/`, with the manifest and the source next to each other ([the plugin store](store.md) says how that repository is laid out).

## Third-party plugins (regulus-plugins#3)

Later, after the internal plugin system is stable: extra index sources, an **allowlist of trusted signers**, warnings for unsigned plugins and sandboxed permissions. The design is in [the plugin store](store.md), "Other sources and trust"; until isolation exists, formats are kept ready and nothing more.

## Open questions

1. **What a plugin can do.** The contract has only `Describe` and `Health`. What a plugin offers (metrics for the dashboard, commands an administrator runs, data the hub stores) and what it may ask of the host (a key-value store, the hub's audit log, the agent's connection) is the next design step. It must say how the permissions of the manifest are enforced.
2. **Dependencies between plugins.** Proposed in [the plugin store](store.md), "Requirements": plugins provide and need *capabilities* (`mail.send@1`), and removing or updating a provider that others need is **blocked**, with the dependents named. Not implemented; `provides` is accepted but unused.
3. **Tag format.** Is `plugin/<id>/vX.Y.Z` confirmed, and does the release workflow publish plugins from those tags?
4. **Plugin signatures.** Do internal plugins use the same Sigstore keyless signature as core releases?
5. **Isolation.** A plugin runs as the same user as the agent, with the same systemd sandbox. A user per plugin, or a stricter sandbox, is open (see [Runtime](runtime.md), "Security").
6. **Skill parameters.** JSON Schema is the candidate (#68); also undecided: several risk levels per skill, and how the assistant discovers a server's skills.
