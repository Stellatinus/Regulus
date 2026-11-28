# The plugin store (design)

**Status: design, with the read side of the store client built (2026-11-26): `internal/store` and `GET /api/store` read and verify the indexes of the sources and say which versions a hub can run (see [Decisions](../decisions.md), "The store backend, read side"). **Installing, updating and removing are built too (2026-11-26), on the hubs only, with a root helper** (`regulus plugin-apply`, [Decisions](../decisions.md), "The store backend, install, update and remove"). Not built: rollback, pinning, the requirements checks, the automatic mode and its "needs approval" state, adding a source from the dashboard, per-plugin channels. **Nothing has run against a real signed index, a real GitHub release, or as root under systemd.** The rest of this page is still design, and the code does not accept the new manifest keys yet (the manifest is read strictly, see [Manifest](manifest.md)). It is the design that `regulus-plugins#1` (lifecycle, index, installation) and `regulus-plugins#3` (other sources) were waiting for. It follows the maintainer's decision of 2026-11-25 (see [Decisions](../decisions.md), "The plugin store: the client here, the catalog there"). Nothing should be coded from it until the maintainer has agreed with it, as for any new area ([AGENTS.md](../../AGENTS.md)).

> **Under review (2026-11-26).** The maintainer decided that plugins are installed on the **hubs only**, and that the redundant hub has the same plugins ([#496](https://github.com/Stellatinus/Regulus/issues/496), [Direction of 2026-11-26](../ui-and-apps-direction.md)). This page still describes a plugin that can run on an agent, and the store sending plugins to agents; that part is not settled and is not to be built before #496 decides what becomes of the agent component.

## The split

"The store" is two different things, and they live in two places:

| Where | What |
|---|---|
| **This repository (Regulus)** | The **store client**: the Store page of the dashboard, reading an index, checking its signature, checking requirements, installing, updating, rolling back and removing plugins, handing them to the agents, and the list of **sources**. The format of the index and the manifest, the plugin API, the runtime and the SDK. |
| **[regulus-plugins](https://github.com/Stellatinus/Regulus-Plugins)** | **Only plugins**, and what is needed to build and release them: their source, their CI, their releases, and the CI that writes and signs the **official index**. No code of the core. |
| **Any other repository** | The same layout and the same index format: an administrator adds its address as a **source** (third-party plugins, regulus-plugins#3). |

The official catalog is therefore **a source like any other**, only configured by default and trusted by a signing identity that is built into Regulus. A person who wants their own plugins copies the layout of regulus-plugins, publishes an index, and gives its address to a hub.

Why the official catalog is not inside this repository is in [Decisions](../decisions.md): the core's updates are trusted by the signature of this repository's release workflow, plugin tags would fill the list of core releases that every hub reads, and each plugin needs its own releases.

## The layout of regulus-plugins

```
plugins/
  <id>/                    one directory per plugin, named after its id
    plugin.yaml            the manifest ([Manifest](manifest.md))
    README.md              what it does, what it needs, how to try it
    CHANGELOG.md           one section per released version
    ...                    source, ui/, tests
  hello-world/             the test plugin (see below), removed later
scripts/                   package a plugin, check a manifest, write the index
.github/workflows/
  ci.yml                   every pull request: manifests, build, tests, for every plugin that changed
  release-plugin.yml       on a tag plugin/<id>/vX.Y.Z[-pre.N]: build, package, release
  publish-index.yml        writes, signs and publishes the indexes
README.md                  the rules below, for people
AGENTS.md                  the same, for AI agents, pointing at this page
```

Rules:

- **A directory is a plugin, and nothing else is.** No shared code between plugins (a plugin builds and is released alone). Common code belongs in the SDK, which belongs to Regulus.
- **`main` is the only long-lived branch**, as in Regulus. Work goes in by pull request, one issue each.
- **Pushing is not publishing.** Only a **tag** publishes. A plugin that has no tag is in no index, however much code it has: that is where a draft, an idea or an experiment lives. Anyone can read the code, because the repository is public, but no hub is offered it.
- **A tag is `plugin/<id>/v<version>`** (the form already proposed in [Plugins](README.md)). CI refuses a tag whose version differs from `version` in that plugin's `plugin.yaml`, a tag on a commit that is not on `main`, and a tag on a commit where CI has not passed, as the core's release workflow does ([Releases](../releases.md)).
- **Each plugin has its own version and releases** and may leave other plugins untouched for a year.
- Releases are made by a workflow (like **Publish release** in Regulus), not by typing tags, so a version always goes through its pre-releases in order. *(The workflow is part of the work, below.)*

### The test plugin

`plugins/hello-world` is a plugin that does nothing useful, on purpose: it is **healthy while its data directory is writable**, counts its starts, and *provides* one capability (`hello.greet@1`). It exists so the store can be built and tried end to end (index, install, update, rollback, removal, a requirement between two plugins) before any real plugin depends on it. A second one, `hello-guest`, that **needs** `hello.greet@1`, belongs to the tests of requirements. Both are deleted once the first real plugin is released; the index must still be able to hold a plugin that has been removed (see "Yanked and removed").

It cannot be built yet, because a plugin outside this repository cannot import the SDK (see "What blocks the work").

## Channels and versions

A plugin's version is SemVer. **Its channel comes from the version, never from a file**, with the same rule as the core ([Decisions](../decisions.md), "Releases and channels"):

| Channel | Version | Who is offered it |
|---|---|---|
| **stable** | `1.2.0` | everyone |
| **beta** | `1.3.0-beta.2`, `-rc.1` | a hub that follows `beta` or `alpha` |
| **alpha** | `1.3.0-alpha.1`, and any other pre-release | a hub that follows `alpha` |

There is **no `dev` channel** (the maintainer's decision, 2026-11-25). Work that is not ready for anyone lives in one of two places:

1. **a directory with no tag** in regulus-plugins: it is in no index at all (a draft, an idea). The code is public, no hub is offered it;
2. **the development repository**, for internal builds the maintainer wants to install on their own hub (below).

### The development repository

A second repository, **`Stellatinus/regulus-plugins-dev`** *(proposed name)*, with **exactly the same layout and rules** as regulus-plugins, its own CI and its own signed index. It is **not special for the store**: it is a *source* that only the maintainer adds, as a `custom` source in their own hub's configuration file, with its signing identity pinned ([Other sources and trust](#other-sources-and-trust-regulus-plugins3)). Nothing about "dev" exists in the index format, the client or the Store page; the client only knows sources.

- **A plugin starts there**, is released there as often as needed (alphas, anything), and is tried on the maintainer's own hub through the ordinary path.
- **When it is ready it is moved to regulus-plugins**: its directory is copied (history can be kept with `git subtree split` or `git filter-repo`), and its first release there is the one the public index lists. The `id` does not change.
- **The same `id` in two sources.** A hub that has both sources must not mix them. An installed plugin remembers the source it came from, updates come only from that source, and the same id offered by another source is shown as such and installed only if the administrator **changes the source** on purpose (the move from the dev repository to the official one is exactly that).
- **Why a repository and not a channel:** no special case in the client, nothing in the public index to hide, nothing that could leak into it by a wrong tag; the cost is that a plugin's history is copied once when it is promoted, and that a second signing identity and a second set of repository settings exist.
- **The development repository must be public** (a hub reads its index without credentials; plugin code is public anyway). It needs the same protections as regulus-plugins on `main` and on its signing environment, or whoever can push there can sign for the maintainer's hub.
- **Sources in the hub's configuration file** (as opposed to added on the Store page) are part of the first store, because this needs them. What waits for the isolation work is *adding a source from the dashboard*, and the allowlist for people other than the maintainer.

Other rules:

- **A hub follows a channel per source, and a plugin may follow another one.** The default is the channel the hub itself follows. An administrator can override it for one plugin (a beta of one plugin on a stable hub). The root update policy's `max_channel` ([Updates](../updates.md)) caps this too, whatever the dashboard says. *(Whether the plugin policy is the same file or another one is open, below.)*
- **0.x is allowed**; a plugin is not forced to 1.0.
- **A plugin has no stable release until it is ready.** A plugin with only alphas appears only to hubs on `alpha`, marked as such.
- **Compatibility is in the index, not found out by running.** Each version lists `api` and `requires` ([Manifest](manifest.md)); the Store page shows a version a hub cannot run as greyed out with the reason, and never installs it.

## The index

The client reads **one index per source**: a signed JSON file, small, at a fixed address, served as a static file (GitHub Pages of regulus-plugins, for example) rather than through GitHub's API, so it does not count against the 60 anonymous requests an hour that limit the core's release list (#213).

```json
{
  "format": 1,
  "source": "regulus-plugins",
  "issued_at": "2026-12-01T09:00:00Z",
  "expires": "2026-12-22T09:00:00Z",
  "plugins": [
    {
      "id": "hello-world",
      "name": "Hello world",
      "description": "Does nothing useful. For testing the store.",
      "kind": "integration",
      "audience": "admin",
      "homepage": "https://github.com/Stellatinus/Regulus-Plugins/tree/main/plugins/hello-world",
      "versions": [
        {
          "version": "0.1.0",
          "channel": "stable",
          "released_at": "2026-12-01T08:30:00Z",
          "api": 1,
          "requires": ">=0.4.0",
          "components": ["agent", "hub"],
          "provides": ["hello.greet@1"],
          "needs": [],
          "permissions": [{"id": "hello-world.greet", "description": "Say hello."}],
          "artifacts": [
            {"os": "linux", "arch": "amd64", "url": "https://github.com/.../hello-world_0.1.0_linux_amd64.tar.gz", "sha256": "…", "size": 1048576},
            {"os": "linux", "arch": "arm64", "url": "…", "sha256": "…", "size": 1040384}
          ],
          "yanked": false
        }
      ]
    }
  ]
}
```

- **It carries everything needed to decide before downloading**: compatibility, requirements, the permissions the plugin asks for (shown **before** installing, as the design in [Plugins](README.md) already says), and a checksum per file. The client never has to unpack a plugin to know what it needs.
- **Signed as the core's `latest.json` is** (#263): Sigstore keyless, the identity of the publishing workflow pinned by the client, `issued_at` and `expires` (21 days) so a replay or a frozen index is noticed, never an index older than the last one verified, and a weekly refresh. The checksums in the index are what make a downloaded file trustworthy; there is no second signature on each file, though a build-provenance attestation may be attached for people ([Releases](../releases.md)).
- **The official identity is built into Regulus**, like `update.Repository`. A workflow that publishes from a branch (not from a tag) is weaker than the core's tag identity: it needs a protected `main`, an environment with required reviewers, and two-factor authentication for whoever can approve. Without them, whoever can push to `main` can sign for every hub. This is the same warning as in [Releases](../releases.md) and it is **not set up**.
- **The format has a `format` number** and follows the rules of [Versioning](../versioning.md): a client ignores fields it does not know, and refuses a `format` it does not speak with a message.

## Requirements

A plugin can need three kinds of things, all listed in the manifest and copied to the index:

| Need | Where | Checked |
|---|---|---|
| **Regulus** | `requires` (a range of core releases) and `api` | already implemented at start; the store checks it before installing |
| **The machine** | `os` and `arch` of the artifacts | the client picks the artifact of the machine; none, no install |
| **Another plugin's feature** | `needs` (new) | when installing, updating, removing and starting |

**Needs are capabilities, not plugin names.** The idea of "this plugin needs mail" is not "this plugin needs the SMTP plugin": a different plugin may send mail some other way. So a plugin **provides** capabilities and **needs** capabilities, each with a number that is the capability's *major version*:

```yaml
# plugin.yaml of the plugin that sends mail
provides:
  - mail.send@1
# plugin.yaml of a plugin that wants to send alerts by mail
needs:
  - capability: mail.send@1
  - capability: sms.send@1
    optional: true          # works without it, with less
```

`provides` already exists in the manifest as a list of names, reserved and unused ([Manifest](manifest.md)). This design gives it its meaning, with `@<major>` in each entry. Capability names are lower-case, dotted, and **the first-party ones are listed in one page in Regulus** (`docs/plugins/capabilities.md`, to write), so two plugins do not invent two names for the same thing. A capability's major version changes only when its contract does.

Resolution rules, for the hub, which decides for every agent:

| Action | Rule |
|---|---|
| **Install** | every non-optional `needs` must be provided by an installed plugin whose capability has the same major. If not, the store says which and offers the plugins that provide it (from the sources it knows), to be installed first, in order. It never installs a half-satisfied set. |
| **Update** | an update that would stop providing something that an installed plugin needs is **blocked**, and says which plugin depends on it. |
| **Remove** | removing a plugin that is the only provider of a capability that another plugin needs is **blocked** (this answers open question 2 of [Plugins](README.md): block, say who depends, do not remove the dependents on their own). |
| **Start** | a plugin whose needs are not met is not started and is shown as **`unmet`** with the missing capability, rather than started and left to fail. |
| **Cycles** | refused at install time, with the cycle named. |

Where a requirement is **used** (how one plugin calls what another provides) is the host side of plugins, **not designed** ([Plugins](README.md), open question 1, #261). Until then `needs` is checked at installation and start and nothing more. It is stated here so the manifest does not change twice.

## Installing, updating and removing

- **The same three modes as the core**: `manual` (tell me), `download` (download and verify, then wait), `automatic` (install in a maintenance window), with the **default `download`**, and an override **per plugin** ([Updates](../updates.md)).
- **Automatic is narrower than "newer".** Even in `automatic` mode, an update is **not** installed on its own when:
  - it is a **new major version**;
  - it asks for **more permissions** than the installed version (the administrator has approved the old set, not the new one);
  - it adds a `needs` that is not met;
  - its channel is not allowed, or the plugin is **pinned**.

  It is shown on the Store page as **needs approval**, with what changed.
- **Pin and rollback.** A version can be pinned. The previous version stays on disk and can be restored with one action; a rollback that does not become healthy is undone, as for the core.
- **On disk** (built): `<plugins dir>/<id>/<version>/`, `<plugins dir>/<id>/current` (a relative link naming the one that runs) and `<plugins dir>/<id>/source.json` (the source it came from). The hub still accepts the flat `<plugins dir>/<id>/plugin.yaml` of a plugin put there by hand ([Runtime](runtime.md), "The plugins directory"). The previous version stays on disk; older ones are removed.
- **Who writes the plugins directory.** The plugins directory must be owned by root ([Runtime](runtime.md), "Security"), and the hub runs as the unprivileged user `regulus`. So, as with the core, **a root helper does the installation**: `regulus plugin-apply`, started by systemd (`regulus-plugin-apply.path` and `.service`) when the hub leaves a request in `<data dir>/plugin-apply/`. **Built (2026-11-26).** The flow:
  1. The hub (`store.Installer`) picks the version and the source, downloads the package of its machine, checks its size and SHA-256 against the signed index, and puts the package, the signed index and its signature bundle in `<data dir>/plugin-store/<id>_<version>/`, then writes `request.json` (`install` or `remove`, plugin, version, source: nothing else) and waits for `result.json` (up to two minutes; a request nobody took is withdrawn).
  2. The helper, as root, trusts **nothing** in those directories. It verifies the **Sigstore signature of the index again** with the signer of the source (the official one is built in; a custom one is read from `plugin_sources` of the root-owned configuration file, never from the request), then the expiry, the plugin API and `requires`, the yank flag and an artifact for its machine. It copies the package into a staging directory only root can write while computing its SHA-256 and checks that against the signed index (the hub could swap the file between two reads), refuses anything unsafe in it (below), checks that `plugin.yaml` says the plugin and version the index said and that every program and module it names is in the package, then moves it to `<id>/<version>/`, switches `current` atomically and writes `source.json`.
  3. What the helper refuses in a package: an entry that is not a regular file or a directory (links, devices, FIFOs), a name that is absolute, not clean, with `..` or `\`, or outside the directory named after the plugin, a duplicate, more than 4096 entries, a file over 256 MiB, more than 512 MiB unpacked (also bounded while it is read, whatever the headers say), and setuid, setgid, sticky and group- or world-writable bits (modes become 0755 for a program, 0644 for the rest). It also refuses a plugins directory that is not owned by root or is writable by others, and a plugin that is already installed from another source or was put there by hand.
  4. The hub then reloads the plugins (`Hub.ReloadPlugins`: the supervisor reads the directory when it starts) and records the action in the audit log (`plugin_installed`, `plugin_updated`, `plugin_removed`, `plugin_change_failed`).
  **Update** is the same install of a newer version from the source the plugin came from and no other (`source.json`); **remove** deletes `<id>/` with every version (the plugin's data directory under `plugins-data` is left, so installing it again finds its state). **A package with only a `ui` component installs and is accepted end to end** (it has no program: the Disks plugin). **Not checked by the helper:** that an index is not older than the last one the hub verified (only the hub keeps that), and `max_channel` of the update policy (open question 2). **Not run as root, under systemd, or against a real release**: the tests run the helper in-process as an ordinary user with the ownership check pointed at that user.
- **To agents**, the hub sends the verified plugin over the agent connection that already exists, as for the core's updates. An agent installs it through its own helper and says so in its next report.
- **Yanked and removed.** A version marked `yanked` is hidden from new installs and offered no more as an update, but **stays in the index and stays downloadable** so a rollback or a pinned hub keeps working. A plugin removed from the catalog (such as `hello-world`) keeps its past versions the same way, with `yanked` set. Deleting a release breaks rollbacks and checksums (the same lesson as #439), so it is not done.
- **A sideload** for developers: installing a package from a local file, through the same helper and the same checks except the index (a package whose checksum is not in a signed index is marked **unverified** and needs the administrator's explicit confirmation, shown in the plugin's state). It is how a build is tried on one machine without any index.

## The Store page in the dashboard

`dashboard/src/StorePage.tsx`, the *Regulus Store* tab of Services. It shows what `GET /api/store` returns and nothing else.

- **A card** (#532) has `[Install] [i]` on its right. **i** is the **information** button (accessible name and tooltip *Information about <plugin>*, English and Italian): it opens the description page (what it does, source, permissions, needs, provides, versions). It replaced the *Details* button one to one. **Install** installs the offered version without opening that page. If the version has **permissions or needs** (the same ones the description page lists), Install first opens a confirmation that lists them and installs only after *Install <plugin>*; a version that asks for nothing is installed at once. The confirmation is never skipped, because the card is not a way around what the description page shows. **Not built yet:** the approval for permissions that *grew* in an update (#519) and the requirements between plugins (#518); today the confirmation shows what the offered version asks for, as the description page does.
- **An installed plugin** shows **Update** (when the offered version is newer) or **Remove** in the same place as Install. Update asks like Install does; Remove always asks and names the plugin. The description page keeps all three actions.
- **The Downloaded switch** (#534) replaced the *All / Installed / Updates* selector. Off shows the whole catalogue; on shows only what is **downloaded on the hub**. *Downloaded* means *installed*: the hub's API (`installed` in `GET /api/store`, read from the plugins directory) knows no plugin that is downloaded and not installed (the helper's staging directory is emptied after an install). If that changes, the switch shows installed *and* downloaded, and the card says which. It is a native checkbox with `role="switch"`, labelled, English and Italian; its state is kept in the browser (`regulus.store.downloaded`); an empty result says *Nothing downloaded yet.*
- **Updates stay visible without their view:** the card shows *Update to X* and the **Update** action, plugins with an update are listed first, and a count (*N to update*) sits next to the switch. The **notification panel** (#533, design) is where updates will also appear; it is not built.
- **A progress bar with the step** (#530). Installing, updating and removing show a bar and the text of the current step, in English and Italian: *Downloading… 40%* (a real percentage: the size comes from the signed index), *Verifying the signature and the checksum*, *Installing* and, for a removal, *Removing*. The steps after the download have no size, so the bar is indeterminate and the step has its name. It is `role="progressbar"` with `aria-valuenow` (only while the percentage is known) and `aria-valuetext`, and the step sits in a polite live region, so a screen reader hears it change. A failure ends the bar in an error state with the reason (*Failed: …*, the same texts as the refusals), and the buttons come on again; an operation that never ends cannot hold the page, because the hub gives up after two minutes (`helper_timeout`, shown as an error, #524). **The state survives a reload and is the same for every administrator**: the page asks the hub (below), quickly while something runs and every five seconds otherwise, and shows an operation it did not start (every action is off while one runs); when another administrator's operation ends, the catalog and the node window's tabs are read again.
  - **Format.** `GET /api/store/progress` (`plugins.manage`) answers `{"progress": null}` or `{"progress": {plugin, action: install|update|remove, version?, state: running|done|failed, step?, percent, code?, helper_code?, message?, started_at, at}}`. `percent` is 0 to 100 while the size is known and -1 otherwise. The hub keeps the operation in memory and keeps a finished one for a minute. A failure carries the codes of the install answers (`code`, and `helper_code` when the root helper refused), never text to show; `message` is English, for the log.
  - **Where the steps come from.** The hub reports *downloading* (counting the bytes it writes), then *verifying* when the package is staged. The root helper (`regulus plugin-apply`) says which step it is in by writing `progress.json` next to `request.json` and `result.json` in `<data dir>/plugin-apply/` (mode 0644, written as the result is, with the id of the request it is for): *verifying* while it checks the index and the package, *installing* while it unpacks and puts the version in place, *removing* for a removal. The hub reads it while it waits for the result and republishes it. The file is information only: if it cannot be written, the change goes on. It lives in the directory that already exists, so **an installation of the previous version needs nothing new** (the helper is the executable, which an update replaces); a hub whose helper is older shows *downloading* and then *verifying* until the result arrives.
  - **Not reported yet:** *Testing* and *Rolling back* (and *Rolled back to version X*) have texts and a place in the format, but nothing reports them: the hub does not test a plugin after installing it (#567) and there is no rollback (#517). A step is shown only when the code is really in it.
- **Not verified:** the page ran in tests against a mocked hub only, as before; the helper's `progress.json` was tested with the helper run in-process, not as root under systemd (#522, #568).

## Kinds and audiences

Two separate questions, two keys. Both are **proposed manifest keys, not accepted by the code yet**.

**`kind`: what a plugin is for.** Used by the Store page to group and describe, not by the runtime.

| `kind` | Meaning | Example |
|---|---|---|
| `manager` | implements a function of a server, a backend of a manager ([Managers, backends and apps](../managers-and-apps.md)) | firewall, reverse proxy, RAID |
| `integration` | provides a capability to other plugins or to the core, with little or nothing of its own to show | SMTP, a notification service, a cloud provider |
| `feature` | gives users something to look at or use | a media overview, a widget |

(`components` already says *where* a plugin runs, `agent`, `hub`, `ui`; `kind` says *what for*. The three kinds are a proposal; adding one later is compatible.)

**`audience`: who is meant to see it.** `admin` (administrators only) or `everyone` (any user who may see the server). **The default is `admin`**, so a plugin that does not say anything is never shown to ordinary users by mistake. A reverse proxy manager is `admin`; a widget of a media server is `everyone`. A plugin that has both (an admin page and a user-facing widget) needs the audience **per dashboard entry**, which depends on how the widget loader describes entries (#21); until then `audience` on the plugin is the **upper bound**, and the loader may only narrow it.

**`audience` is a hint for the dashboard, not a security boundary.** Hiding a button does not stop a request. What a user may *do* is decided on the hub by **permissions**, the plugin's permissions given to roles ([Manifest](manifest.md), "Permissions"; #61), and enforcing them is not done yet. A plugin's data and actions an ordinary user must not reach need a permission that only an administrator role holds, **checked by the hub**, however the dashboard draws it.

### An admin view and a standard view of the dashboard

What the maintainer wants is to give the dashboard to many people: administrators see everything (servers, users, updates, plugins, shell and files), the others see only the applications and the widgets meant for them. This is **a feature of the dashboard, not of the store**, and it deserves its own design issue (below). The direction proposed here, to be confirmed there:

- **A view is derived from permissions and roles**, not a second kind of account. The dashboard already shows its Administration section only to those who have the permissions for it ([Dashboard](../dashboard.md)); this extends the idea to widgets and plugin pages.
- Two named views, **Admin** and **Standard**, which a role is mapped to; a person with an administrator role can switch to the Standard view to see what others see.
- `audience` of a plugin's entries decides in which view they are *offered*; the permissions decide what works.

## Other sources and trust (regulus-plugins#3)

- **Sources are a list in the hub's configuration**: a name, an index address, a **trust level** (`official`, `custom`), the channel to follow, and for `custom` ones the **signing identity** to expect (an identity and an issuer, as in [Releases](../releases.md)).
- **An unsigned or unverifiable index is refused**, not offered with a warning: plugins run as the agent's user, and for now can read everything the agent can ([Runtime](runtime.md), "Security").
- **Adding a source is an act of trust**, and the page says so in plain words: a plugin is code that runs on the servers, and whoever signs the index decides what runs. It needs an administrator, an explicit confirmation naming the signer, and an entry in the audit log.
- **An allowlist of signers** is the same list: only the identities an administrator has added are accepted, and the official one is there from the start.
- **Isolation is the open point.** Until plugins run under a user of their own or a stricter sandbox ([Runtime](runtime.md), open question 5) and permissions are enforced, a third-party source is as dangerous as installing a program as root. Documents say it, and the work for sources other than the official one should wait for those.

## What blocks the work

1. **A plugin outside this repository cannot be built.** The SDK is `internal/plugin/sdk` and the Go module path is still the old organization's name (see [Decisions](../decisions.md), "The repository moved"): Go refuses to import an `internal` package from another module, and a plugin also needs the generated plugin contract (`proto/regulus/plugin/v1`). Before `hello-world` or any plugin can live in regulus-plugins, the SDK and the contract must be an importable, versioned package (a public directory of this module, or a module of its own; a plugin's `go.mod` pins a version). This is a change in this repository, and it also fixes what a "plugin API version" means for a plugin author. That module path is part of what an author would have to type, so the maintainer should decide whether this is the moment to rename it.
2. **A plugin can only run and be healthy today** ([Plugins](README.md), open question 1). The store can install and update plugins before that, but a real plugin needs the host side (#261) and the widget loader (#21).
3. **The root helper** must learn to install plugins (above).
4. **The signing set-up** of regulus-plugins (protected `main`, environment, two-factor authentication) is a repository setting, not a file.

## Open questions

1. **The hosting of the index.** GitHub Pages is proposed because it avoids the API's rate limit; a release asset on a fixed tag is the alternative. Either needs the identity to be pinned.
2. **One plugin policy or the core's.** Does `max_channel` of `/etc/regulus/update-policy.yaml` also cap plugins, or does a plugin have its own file?
3. **Per-plugin channel override** (above) is the proposed answer; the maintainer may prefer a simpler one.
4. **Capability versions.** `@<major>` only, or a range? A major is simplest and enough until a plugin needs more.
5. **Where capability names are registered**, and who may add one.
6. **Per-entry audience**, which waits for #21.
7. **Names of the views** (Admin, Standard) and whether a role chooses its view or the permissions imply it.
8. **Does an `agent` component get a plugin that its server does not need?** The hub could install a plugin only on the agents whose manifest or an administrator's choice names them; the default is a decision for #1.

## Work to do

| Piece | Where | Milestone |
|---|---|---|
| Make the SDK and the plugin contract importable by plugins in another repository | Regulus | M6 |
| Store client design and implementation: index format and reader, signature check, sources, requirements, the Store page | Regulus (`regulus-plugins#1`) | M6 |
| Manifest: `kind`, `audience`, `needs`, versioned `provides` | Regulus | M6 |
| The root helper installs and rolls back plugins | Regulus | M6 |
| The layout, CI, release workflow and index publishing of regulus-plugins; the test plugins | regulus-plugins | M6 *(that repository has no milestones yet)* |
| Sources in the configuration file, with a pinned signer (needed for the development repository): part of the store client | Regulus | M6 |
| Adding a source from the dashboard, and the signer allowlist for people other than the maintainer (`regulus-plugins#3`) | Regulus | after isolation is designed |
| Create `regulus-plugins-dev` with the same layout and CI | the maintainer creates it; the layout comes from regulus-plugins | M6 |
| An Admin view and a Standard view of the dashboard | Regulus | M6, with #21 |
