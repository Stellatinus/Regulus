# Changing a node's type, and guided installation

**Status: PROPOSAL, awaiting the maintainer's agreement. Nothing on this page is implemented**, and no code may be written for it until the maintainer agrees (AGENTS.md, "New areas start with a design issue"). It answers the two design issues [#164](https://github.com/Stellatinus/Regulus/issues/164) (changing a node's type from the dashboard, with the command line kept) and [#165](https://github.com/Stellatinus/Regulus/issues/165) (guided installation: "add a node of this type" gives the exact steps and checks each one). It builds on [node-types.md](node-types.md) (#160), the capability and permission lists (#169, #170, draft model in `internal/capability`), the role model (#104) and the promotion flow (#105). The prototype of the screens is `design-drafts/node-types-v2.html`.

Names are provisional (glossary, "Naming plan"). The hub **assigns** a capability; the word *grant* is not used for that, because the glossary keeps it for the delayed rules of the development node (#168). "Node" is the design word for any machine in the group; the UI and the command line today say **server**, so the commands below use `server`. If the naming plan adopts *node*, the commands follow.

What exists today and is used as the starting point (checked on 2026-11-19): permissions `servers.read` and `servers.manage` (`internal/auth/permissions.go`); audit events in `snake_case` (`agent_enrolled`, `agents_listener_on`, …); the agent reads `/etc/regulus/agent.yaml`, which the installer writes as `0640 root:regulus`, so the agent process (user `regulus`) can read it and **cannot write it**; the agent protocol has three command kinds (`Ping`, `SetIntervals`, `ApplyUpdate`) and `Welcome` carries no type. Nothing in the hub or the agent knows a node type.

## Part 1: changing a node's type (#164)

### One operation, three front ends

There is **one operation in the hub**: *set the assignment of a node* (its preset and its per-node overrides, as in `capability.Resolve`). The dashboard, the console and the `regulus` command line are front ends to it; none of them has logic of its own. The hub also computes, for every node and every target type, **whether the change is possible and, if not, why**, so that the dashboard's greyed-out buttons, the console's "not available" lines and the CLI's error messages are the same answer.

A change to or from **redundant hub** is not this operation: it is a role transition of #104 and goes through the promotion flow of #105 (see "Role or capability" below). The dashboard offers it in the same place, and the button starts #105.

#### HTTP API (proposed, in `/api/openapi.yaml`)

| Method and path | What it does | Permission |
|---|---|---|
| `GET /api/agents/{id}/assignment` | the node's preset, overrides, the capabilities the hub **assigns**, the ones the node **allows** (as last reported), the **effective** set, the change in progress (`pending`, `applied`, `refused`, `failed`) and the assignment version | `servers.read` |
| `GET /api/agents/{id}/assignment/options` | for each target type: `available` (bool), `reason` (a closed code, below), `fix` (the command to run, and where), `effects` (what changes: listener opened, data kept, …) and `confirm` (`simple` or `step-up`) | `servers.read` |
| `PUT /api/agents/{id}/assignment` | sets preset and overrides; body `{preset, enable[], disable[], allow[], deny[]}`; header `If-Match: <assignment version>` so two administrators do not overwrite each other; `?dry_run=true` returns the result and the effects without saving | `servers.assign` (new) |
| `DELETE /api/agents/{id}/assignment/pending` | cancels a change that the node has not applied yet (it was offline) | `servers.assign` |

The hub refuses (`409`) what `options` says is not available, with the same `reason`; the client never decides alone.

**Permission.** A new permission, **`servers.assign`**, given to `admin` only by default. `servers.manage` (rename, revoke, enrollment tokens, given to `operator`) is not enough, because making a server a public gateway is a change in exposure, not housekeeping. The redundant hub keeps the permission of #105 (proposed `hub.promote`). Changes that open a listener (gateway capabilities) and every hub promotion need **step-up re-authentication** (the password again, #279) once it exists; until then a confirmation dialog.

#### Command line and console

On the hub machine, through the local socket the console already uses (actor `console` in the audit log, as `reset-admin` does today):

| Command | What it does |
|---|---|
| `regulus server list [--json\|--plain]` | servers with type, effective capabilities and change state |
| `regulus server type NAME [--json\|--plain]` | the same as `GET …/assignment` and `…/options` for one server |
| `regulus server type NAME TYPE [--enable CAP] [--disable CAP] [--dry-run] [--yes] [--no-input]` | the same as `PUT …/assignment`; without `--yes` it shows the effects and asks; with `--no-input` and no `--yes` it refuses |
| `regulus server type NAME --cancel` | cancels a pending change |

From another machine a script uses the HTTP API. The console of the hub gets the same operation in **Actions** (a server list with "change type", each unavailable item with its reason, as the console already does for its other actions).

On the **node**, the second key (below):

| Command | What it does |
|---|---|
| `regulus agent allow list [--json\|--plain]` | the allowed capabilities |
| `sudo regulus agent allow add CAP…` / `remove CAP…` | edits `allow` in `/etc/regulus/agent.yaml` (atomic replace, like `config set`) and reloads the agent |
| `sudo regulus agent config set KEY VALUE` | the other local settings of this page (`backup_dir`, `accept_promotion`), written the same way; today `config set` exists only for the hub's file |
| `install.sh … --allow CAP` (repeatable) | writes the list at install time |

The node's console shows the list in **Settings** and edits it there (only on the node, as root).

#### Audit events (snake_case, like the existing ones)

| Event | Actor | When |
|---|---|---|
| `server_assignment_requested` | the user, or `console` | a change was saved by the hub (detail: old and new preset and overrides; a dry run is not logged) |
| `server_assignment_applied` | the node id | the node confirmed it (detail: effective capabilities) |
| `server_assignment_refused` | the node id | the node refused (detail: the reason code, for example `not_allowed`) |
| `server_assignment_failed` | the node id | the node accepted but could not apply (for example the port is in use) |
| `server_assignment_cancelled` | the user | a pending change was cancelled |
| `server_allow_changed` | the node id, "reported by the node" | the node reported a different allowed list than last time |

### The allowed capabilities on the node (the second key)

- **Name:** `allow`, a list of capability names in `/etc/regulus/agent.yaml`; environment `REGULUS_AGENT_ALLOW` (comma separated), flag `--allow` on `regulus agent` and `install.sh`. UI label "Allowed capabilities" / "Funzionalità consentite".
- **Default: empty.** A node installed without `--allow` agrees to nothing that needs consent.
- **What may be listed:** only capabilities for which `capability.NeedsConsent` is true: today `relay`, `notifications`, `authentication`. A type name is accepted as a shorthand on the command line and **written as capabilities** (`--allow gateway` writes `relay`; `notifications` and `authentication` must be named one by one, because each changes the threat model). Unknown names are refused, like unknown keys in the configuration file.
- **How it is set:** at install time (`--allow`), or later on the node with `regulus agent allow add|remove`, the node's console (Settings), or by editing the file. Every path needs root on the node.
- **The hub can never change it.** There is no message in the protocol that carries it from the hub to the node, and the file is not writable by the agent process (`0640 root:regulus`), so neither a compromised hub nor a bug in the agent can add an entry. The node **reports** the list to the hub (in `Hello`, and again after a reload); the report can only **narrow** what the node does, never widen it, so it does not contradict "the node does not declare its type" in node-types.md (option C there).
- **Both keys are checked twice:** the hub computes *effective = assigned ∩ (allowed ∪ capabilities that need no consent)*, and the node enforces the same rule locally: it never opens a listener the file does not allow, whatever the hub sends.
- **Removing an entry on the node** stops the capability at once (the listener closes), the node reports it, the hub records `server_allow_changed` and shows the capability as "assigned, not allowed by the node". The hub's assignment is kept, so adding the entry back restores it without a new change on the hub.

Two other local consents exist and are shown in the same place, but are **not** entries of `allow`:

- **`accept_promotion`** (`token` by default, or `autonomous`): the setting of #105 for the redundant hub. Proposed: changeable only on the node, like `allow`.
- **`backup_dir`**: the directory where a backup node keeps data. `backup-storage` opens no listener, so it does not need consent in the sense of `NeedsConsent`; but the hub cannot invent a place on someone else's disk, so the capability **cannot be applied** until the node has a `backup_dir` (an existing directory, owned by `regulus`, on a filesystem with the free space the type requires). The node reports its free space.

### Role or capability ([#104](https://github.com/Stellatinus/Regulus/issues/104))

| Change | Kind | How |
|---|---|---|
| compute ↔ backup ↔ gateway ↔ development | **capability change** on the role `agent` (a different preset) | this operation |
| one capability on or off (for example `notifications` on a gateway) | **capability change** (an override) | this operation |
| agent → redundant hub | **role transition** agent → candidate → `hub-redundant` | #105 (token on the node, or `accept_promotion: autonomous`) |
| redundant hub → agent | **role transition** (demotion) | #105 `demote` |
| anything involving the **active hub** | role transition of #104 / #109 | never this operation; the dashboard says "changing the active hub is a planned switch (#109)" |
| pure-hub on or off | attribute of the hub roles (#104) | not this operation |

Open: whether a redundant hub may also carry agent capabilities (a redundant hub that is also a gateway). Proposal for the first version: **no**; a node with a hub role has no preset, which keeps the pure-hub rule simple.

### A type change while the node is connected

1. The hub saves the new assignment with a new **assignment version** (a counter per node) and records `server_assignment_requested`. The change is `pending`.
2. If the node is connected with a protocol version that knows it, the hub sends a new command **`Assign{version, capabilities[]}`** (a new `Command` kind: a protocol version bump, N / N-1 rules of [versioning.md](versioning.md)). The full effective list is sent, not a difference, so a lost message cannot leave the node half-changed.
3. The node checks every capability against `allow` and its local preconditions (`backup_dir`, a free port), applies what it can (opens or closes listeners, starts or stops accepting backups) and answers `CommandResult` plus the effective list it now runs. The hub marks the change `applied`, `refused` (with the reason) or `failed`.
4. If the node is **offline**, the change stays `pending` and the dashboard says "applies when ceres connects". `Welcome` gets a new field with the current assignment, so a node that returns gets it at once. A pending change can be cancelled.
5. A node in an **update-only session** (#212) or on an older protocol gets nothing: the change stays `pending` with reason `agent_too_old` ("update ceres first").
6. A node restarted from scratch (new enrollment) gets its assignment from the enrollment token (Part 2) or starts as `compute`.

### Reverting, per type

Turning a capability off **moves no data** (node-types.md); the table says what happens to what is already there. Moving data to another node is #171.

| From | Revert to compute | Data and state | Consent on the node |
|---|---|---|---|
| **Access gateway** | immediate: the node closes the listener and drops the relayed connections; phones move to the next address of their list (#155) | none: the gateway holds no secrets (#161) | `allow` keeps `relay`; the dashboard suggests `sudo regulus agent allow remove relay` on the node, but cannot do it |
| **Backup node** | in two steps: (1) at once, it stops accepting new backups; (2) the data already stored stays on the disk, **read-only**, and stays listed as a restore source until the administrator chooses: *move it* (#171), *delete it* (typed confirmation of the node name, audited), or *keep it detached* | the backups; never deleted by the revert itself | `backup_dir` stays; deleting it is the node owner's choice |
| | refused (`only_backup_target`) while it is the only backup target of a server, unless the administrator confirms that those servers will have no backup | | |
| **Development node** | open in #168. Proposal: leaving the development type **raises** what the node may do, so it waits like a grant (the mandatory delay of #168) | grants of the development node are revoked | — |
| **Redundant hub** | `regulus promote demote` (#105); refused while it is the active hub | its replica is deleted after the demotion is confirmed (it is a copy) | `accept_promotion` unchanged |
| **Compute → anything** | — | workloads stay (#113); a type that does not allow `workloads` is **refused** (`has_workloads`) while workloads run, rather than stopping them silently | — |

### What the dashboard shows (node inspector, "Type and capabilities")

- The **type** (preset name) and, for hub roles, the role chip.
- A table of the capabilities that need a decision, with three columns: **hub** (assigned or not), **node** (allowed or not, as last reported, with when), **effective**. A row whose two keys disagree is marked ("assigned, not allowed by the node").
- The local consents: `allow`, `accept_promotion`, `backup_dir` and free space, all read-only, with "set on the node".
- A change in progress: `pending` (and why: offline, too old), `refused` or `failed` with the reason.
- **Change type**: one row per target type. Available ones are buttons. Unavailable ones are shown, **not hidden**, with the reason and the fix, because hiding them would leave the administrator wondering whether the feature exists:

| Reason code | Shown text (English) | Fix shown |
|---|---|---|
| `not_allowed` | ceres does not allow `relay` | run on ceres: `sudo regulus agent allow add relay` |
| `no_backup_dir` | ceres has no backup directory | run on ceres: `sudo regulus agent config set backup_dir /srv/regulus-backups` |
| `not_enough_space` | 12 GB free, the backup type needs 50 GB | free space or choose another directory |
| `needs_token` | ceres accepts promotion only with a token | run on ceres: `sudo regulus promote request`, then enter the token here |
| `agent_too_old` | ceres runs 0.3.9; this needs 0.6.0 | update ceres (Updates page) |
| `has_workloads` | 3 workloads run on ceres | move or stop them first |
| `only_backup_target` | ceres is the only backup target of 4 servers | add another backup node first, or confirm |
| `is_active_hub` | this is the active hub | a planned switch (#109) |
| `no_permission` | you need `servers.assign` | ask an administrator |
| `not_designed` | this type is not designed yet | — |

An offline node is **not** a reason to refuse: the change is saved as pending.

**The confirmation** lists the effects (for a gateway: "ceres will accept connections from the internet on port 443"), shows **the two keys** (hub: you, now; node: allowed since a date, or missing) and enables the confirm button only when both are turned. Changes with `confirm: step-up` ask the password (#279). After confirmation the inspector shows the change as pending until the node applies it.

## Part 2: guided installation (#165)

### What the administrator sees

**Servers → Add a server** becomes a wizard: (1) **choose the type** (compute, backup node, access gateway, redundant hub; types not designed yet are listed as unavailable), (2) **preflight**: what the machine needs, (3) **ordered steps**, each with where it runs (on the hub, on the new node, outside Regulus), the **command to copy**, **what to expect**, and its **status**. The same steps are printed by `regulus server add --type gateway [--plain|--json]` on the hub and shown in the console (Actions → Add a server).

### One source for the steps

Each type's steps are a YAML file embedded in the binary: `internal/guide/steps/<type>.yaml` (package name provisional). It is the only source:

- the **hub** serves it, rendered for one session, at `GET /api/install-guides/{type}?lang=it` and drives the wizard and the console with it;
- the **documentation** is generated from it (`make guides` writes `docs/install-guides/<type>.md` in English; CI fails when they differ, like `make proto-check`);
- the **configurator** (#167, a static page) reads a JSON export of the same files (`make guides` also writes it), so it renders the same steps without the hub.

Proposed schema (version 1):

```yaml
schema: 1
type: gateway                       # a preset of internal/capability, or "redundant-hub"
title: {en: Access gateway, it: Gateway di accesso}
needs_regulus: ">=0.6.0"
preflight:                          # what the machine needs; see the table below
  - id: public-address
    text: {en: "A public IPv4 or IPv6 address", it: "Un indirizzo IPv4 o IPv6 pubblico"}
    verify: manual
  - id: memory
    text: {en: "At least {{min_ram}} of memory", it: "Almeno {{min_ram}} di memoria"}
    verify: {kind: auto, check: node.memory_at_least, args: {bytes: 536870912}}
steps:
  - id: listener
    where: hub                      # hub | node | outside
    title: {en: Turn on remote agents, it: Attiva gli agenti remoti}
    command: "sudo regulus config set agent_listen :{{agent_port}}"
    expect: {en: "The hub listens on port {{agent_port}}", it: "Il hub ascolta sulla porta {{agent_port}}"}
    verify: {kind: auto, check: hub.agent_listener_on}
    on_fail: {en: "…what to do…", it: "…cosa fare…"}
  - id: install
    where: node
    needs: [listener]
    title: {en: Install and enroll with the relay allowed, it: Installa e registra consentendo il relay}
    command: >-
      REGULUS_AGENT_TOKEN={{token}} sh install.sh --version {{version}} --role agent
      --hub {{hub_address}} --ca-fingerprint {{ca_fingerprint}} --allow relay
    secret: [token]
    expect: {en: "The installer ends with: enrolled as {{node_name}}", it: "…"}
    verify:
      - {kind: auto, check: node.enrolled}
      - {kind: auto, check: node.allows, args: {capability: relay}}
  - id: cloud-firewall
    where: outside
    title: {en: Open port {{relay_port}}/tcp in the provider's firewall, it: "…"}
    command: null                   # nothing Regulus can run: the provider's console
    verify: manual
```

Rules of the format:

- **Placeholders** are `{{name}}` from a **closed list** (`hub_address`, `agent_port`, `version`, `ca_fingerprint`, `token`, `node_name`, `relay_port`, `backup_dir`, `min_ram`, `min_disk`). No conditions, loops or functions, so Go, the TypeScript dashboard and the configurator render it with the same few lines. An unknown placeholder fails a test.
- **Texts carry both languages in the file** (`en` and `it`); a test fails when one is missing, empty, or has a different set of placeholders. Rejected: keys into the dashboard catalogs (`en.ts`, `it.ts`), because the console (Go tables) and the configurator could not read them, and the steps would have three sources again.
- **Commands are never translated**: `command` is one string, the same in every language; only `title`, `expect`, `on_fail` and preflight texts are.
- **Checks** are names from a closed registry implemented in Go (`hub.agent_listener_on`, `node.enrolled`, `node.connected`, `node.version_matches`, `node.allows`, `node.capability_applied`, `node.memory_at_least`, `node.disk_free_at_least`, `node.backup_dir_ok`, `gateway.reachable_from_hub_network`, `gateway.tls_ok`, `gateway.forwards_to_hub`). A file that names an unknown check fails a test. The configurator, which has no hub, shows every check as "the hub checks this".
- `needs` orders the steps; a step whose `needs` are not done is `blocked`.

### Verification per step

| Kind | Who verifies | Status shown |
|---|---|---|
| `auto` | the hub, from what it already knows (registry, `SystemInfo`, metrics, the node's reports) or by a probe it runs | `passed` (with when), `checking`, `failed` with the check's reason and `on_fail` |
| `manual` | the administrator presses "Done"; the hub records who and when (audit `install_step_confirmed`) | `confirm manually`, then `confirmed by admin` |
| none | informational | — |

The hub re-runs `auto` checks while the wizard is open and when the node connects, so a step turns green without a click. A **manual** step is never shown as verified: the text says "confirmed by you, not checked". Honesty about what the hub can see:

- **"Reachable from outside"** cannot be proved from inside the group. The hub connects to the gateway's public address from its own network (`gateway.reachable_from_hub_network`), which proves the address and the TLS, and catches most mistakes; but a hairpin NAT can pass while the internet fails. The step stays **partly manual**: "test from a phone on mobile data" (later the Regulus Access app can report its own attempt). Rejected: an external probe service (a third party would learn the group's addresses).
- Firewall rules of the host can be checked only once the firewall manager exists (#290); until then that step is `manual`.

A **wizard session** is stored on the hub (type, intended node name, steps and their status, who started it, the enrollment token's **id**, never its value). It expires with the token; an expired session can be resumed with a new token.

### Preflight requirements per type (proposed, numbers to measure)

| Type | Machine | Network | On the node it must allow | Checked |
|---|---|---|---|---|
| Compute | Ubuntu or Debian with systemd, amd64 or arm64 | outbound TCP to the hub's agent port | nothing | arch and memory: auto after enrollment |
| Backup node | as compute; a dedicated filesystem recommended; free space ≥ `min_disk` (default to decide in #166) | outbound to the hub only | `backup_dir` set | free space: auto (`node.disk_free_at_least`, `node.backup_dir_ok`) |
| Access gateway | a small VM is enough (1 vCPU, 512 MB proposed) | **a public address**; one inbound TCP port (#161 decides which) free on the host and open in the provider's firewall | `allow: relay` | public address: manual; port: partly auto |
| Redundant hub | at least the hub's memory and twice the hub's data size; the **same release** as the active hub | reaches the hub; reachable by the other nodes for a failover (#109) | `accept_promotion` or a token (#105) | release and resources: auto; the rest belongs to #105, #106 |

Before enrollment the hub knows nothing about the machine, so preflight is first run **on the node**: proposed `sh install.sh --preflight --type gateway`, which checks what it can and installs nothing. After enrollment the hub re-checks from `SystemInfo` and the metrics.

### The type travels with the enrollment token

When the wizard makes the enrollment token it stores the **intended preset** next to the token (the hub's key, given by an administrator with `servers.assign`). When a node enrolls with that token the hub assigns the preset at once; the node still applies only what its `allow` permits, so a gateway enrolled without `--allow relay` appears as "assigned, not allowed by the node" and the wizard's step fails with the fix. A plain **Add a server** token keeps meaning `compute`. A redundant hub is not assigned by a token: its last step is the promotion of #105.

### Secrets

- The enrollment token rules do not change (single use, one hour by default, stored hashed, rate limited).
- The wizard shows the token **once**, inside the command of the install step. After a reload, or for anyone else opening the same session, the command shows `REGULUS_AGENT_TOKEN=…` with "shown once: make a new token", which cancels the old one and shows the new one once.
- The command uses the environment form (`REGULUS_AGENT_TOKEN=… sh install.sh`), so the token is not in the process list of the node, as [installation.md](installation.md) already recommends.
- The CA fingerprint is not a secret and is always shown.
- Tokens never appear in the generated documentation, the configurator, URLs, logs, audit details or the session record. `--json` output of `regulus server add` includes the token once, in a field named `token`, and nothing else does.

## Steps per type (first version)

| Type | Steps |
|---|---|
| Compute | 1 remote agents on (auto) · 2 install and enroll (auto) · 3 connected and reporting (auto) |
| Backup node | 1 remote agents on (auto) · 2 install and enroll (auto) · 3 set `backup_dir` on the node (auto: `node.backup_dir_ok`) · 4 enough free space (auto) · 5 the hub applies `backup-storage` (auto) |
| Access gateway | 1 preflight on the node (manual) · 2 remote agents on (auto) · 3 install and enroll with `--allow relay` (auto) · 4 open the port on the host firewall (manual, auto with #290) · 5 open the port in the provider's firewall (manual) · 6 the hub applies `relay` (auto) · 7 reachable and TLS correct from the hub's network (auto) · 8 forwards to the hub (auto) · 9 test from a phone on mobile data (manual) |
| Redundant hub | 1 install and enroll as compute (auto) · 2 same release as the hub (auto) · 3 promotion: `regulus promote request` on the node, token on the hub (#105) · the rest belongs to #105 to #109 |

## Open questions

1. The permission name (`servers.assign`) and whether `operator` gets it for the light changes (backup) but not for exposure (gateway).
2. Whether `accept_promotion` should move into the same `allow` list (one place for every local consent) or stay a separate setting as in #105.
3. A redundant hub that also carries agent capabilities: proposed no for the first version.
4. Leaving the development type: the same delay as a grant (#168)?
5. Minimum resources per type: the numbers above are guesses until measured on a real small VM (see the `[Test]` issue).
6. Whether `install.sh --preflight` is enough before enrollment, or the wizard should offer a downloadable check script without the package.
