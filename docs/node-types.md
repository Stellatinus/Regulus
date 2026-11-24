# Node types

The legend of the kinds of machine in a Regulus group. **Design draft:** only the **hub** and the **agent** (including the built-in agent) exist in the code today. Every other type below is *proposed*; nothing here is implemented (checked against the code on 2026-11-13). Names are provisional: the maintainer decides the final nomenclature later (see the [glossary](glossary.md), "Naming plan"). The design issue is [#160](https://github.com/Stellatinus/Regulus/issues/160); the role model it builds on is [#104](https://github.com/Stellatinus/Regulus/issues/104).

"Node" is used here for the design discussion only, as in #103: any machine in the group. Do not use it in code or UI yet.

## The rule that does not change

**Inside, every node connects the same way:** a `regulus` process on the machine opens an outbound, mutually authenticated gRPC connection to the active hub and is identified by the certificate the hub's CA issued to it ([architecture.md](architecture.md), sections 2 and 3). A node type does not add a new kind of connection to the hub. It adds **what the node is expected and allowed to do**, expressed as a role plus capabilities (proposal of #104: an enum plus capability flags, not unrelated booleans), so a new type does not require rewriting the others.

The exceptions are explicit and each has its own design issue: a node that **accepts inbound connections** (the access gateway) and **direct links between nodes** (later, see the end).

## The types

| Type | What it is | Talks to | Status |
|---|---|---|---|
| **Hub** (active hub) | Orchestrates the group: dashboard, API, database, CA, updates. Exactly one active per term. | every node (they connect to it) | **implemented** (`regulus hub`) |
| **Redundant hub** (informally "secondary hub"; further ones "tertiary"…) | Passive hub holding a copy of what the hub needs; takes over when the active hub is unreachable, or when forced. A **pure-hub** attribute means it hosts no workloads. | the active hub (replication); after failover, every node | designed in the issues of #103, **not implemented**. More than one redundant hub, with priority, is parked in [#113](https://github.com/Stellatinus/Regulus/issues/113). |
| **Compute node** (standard node) | A server that runs workloads and is managed from the hub. | the active hub only | **implemented** as the agent (`regulus agent`; the hub's own server is the built-in agent). Workload management is [#113](https://github.com/Stellatinus/Regulus/issues/113). |
| **Backup node** | A storage node that keeps the backups of the other nodes. Has a connection **to the hub only** and **cannot contact other nodes**. | the active hub only | proposed in [#166](https://github.com/Stellatinus/Regulus/issues/166). |
| **Access gateway** | A node with a public address that lets the Regulus Access phone app reach the hub from outside the local network. Forwards the phone's connection to the active hub; **mTLS stays end to end between phone and hub**. May later also host a notification server and, separately designed, an authentication server. | the active hub (outbound); phones (inbound, the only inbound listener of the group) | proposed in [#161](https://github.com/Stellatinus/Regulus/issues/161) (epic [#152](https://github.com/Stellatinus/Regulus/issues/152)). |
| **Development node** | A node for developer and AI tools (for example Claude Code). Reaches the hub only, **never other nodes**, and cannot use the hub's shared resources unless an administrator writes a specific **grant**, whose confirmation is deliberately slow (a mandatory wait, enforced by the hub). | the active hub only | proposed in [#168](https://github.com/Stellatinus/Regulus/issues/168). What can be enforced outside Regulus (the host's own network) is open there. |
| **AI node** | A node that offers local AI (for example a server running Ollama) to other nodes. | the active hub, and possibly **directly** to the nodes that use it (see below) | idea only, not designed. |

A node has **one** type (the role); capabilities are switched on one by one by an administrator on the hub. An access gateway is a normal enrolled node plus the gateway capability, so it is rebuilt from nothing by enrolling again.

## Who decides a node's type (proposal of #160, my call, not agreed)

Today the only "type" is the installer's `--role hub|agent`, written to `/etc/regulus/role` for the package scripts; the binary ignores it, and neither the `agents` table nor `Hello` carries a type ([architecture.md](architecture.md), section 1). Options considered:

| Option | How | Verdict |
|---|---|---|
| **A. The hub assigns it** | Type and capabilities are stored in the hub's registry (a new column or table next to `agents`), set by an administrator, and sent to the node (in `Welcome`, or as a command). | **Proposed.** Same principle as the agent id, which comes from the certificate and never from what the agent says. Changing a type needs no new certificate, so it works with the N / N-1 rule and without certificate renewal (#128). |
| B. In the certificate | A certificate extension names the type. | Rejected for now: changing a type would mean reissuing the certificate, and renewal does not exist yet (#128). |
| C. The node declares it in `Hello` | The node says what it is. | Rejected: a compromised or misconfigured node could claim a capability. |

**Two keys for exposure.** A capability that changes what a node exposes locally (the access gateway's inbound listener) needs **both**: the hub grants it **and** the node opts in locally (a setting, off by default, like `agent_listen`). A hub alone cannot make a server open a port.

**Role and types.** The **role** stays the exclusive enum of #104 (`agent`, `hub`, `hub-redundant`, plus the transient `candidate`); a **node type** such as "backup node" or "access gateway" is a named **preset of capabilities on the role `agent`** (compute node = no extra capability; backup node = `backup-storage`; access gateway = `relay`, later `notifications`). The UI and docs say "type"; the code keeps role plus capability set, so a new type is a new preset, not a new code path. "A backup node cannot contact other nodes" needs no rule today, because nodes never contact each other (all traffic goes through the hub); the rule matters when direct links exist (below).

## Changing a type later, and the command line (proposal, [#164](https://github.com/Stellatinus/Regulus/issues/164))

The intended flow: install the hub, enroll **only standard agents**, then decide from the dashboard that one becomes the redundant hub, another a backup node, another an access gateway, and undo it later. The **command line always stays** a way to do the same: advanced installs and scripts use it, and the dashboard and the CLI are two front ends to one operation.

It follows the two-key rule above, so what the dashboard can do depends on the node having agreed:

| Change | Node side (second key) | Weight |
|---|---|---|
| Backup node | reports free space and a storage directory; no new exposure | light |
| Access gateway | an **allowed-capabilities list on the node**, empty by default, set at install (`--allow gateway`) or later on the node; the hub never changes it. Opening a firewall is outside Regulus. | medium |
| Redundant hub | the promotion flow of [#105](https://github.com/Stellatinus/Regulus/issues/105): the one-time token on the node, or its "accept promotion autonomously" setting (off by default) | heavy: the whole epic #103 |

None of this exists in the code. Related: guided installation ([#165](https://github.com/Stellatinus/Regulus/issues/165): from the hub, "add a node of this type" gives the exact steps and checks each one) and a possible future **configurator**, a static web page where one describes a home lab or a company and gets the node list and the steps ([#167](https://github.com/Stellatinus/Regulus/issues/167), only evaluated, not decided).

The proposed design is on its own page, [node-type-changes.md](node-type-changes.md): the single assignment operation behind the dashboard, the console and `regulus server type`; the node-side `allow` setting (empty by default, never changed by the hub); undo rules per type; what the dashboard shows and why an action is not offered; and, for #165, the step files shared by the wizard, the console, the documentation and the configurator. It is a proposal waiting for the maintainer.

## Capabilities (proposed)

| Capability | Meaning | Used by |
|---|---|---|
| `relay` | forwards encrypted phone connections to the active hub; holds no secrets | access gateway |
| `notifications` | hosts the push path for phones; messages carry no secrets | access gateway (second step) |
| `authentication` | serves authentication for other programs; **holds keys on a public machine, so it needs its own threat model** | access gateway (later, separate design) |
| `backup-storage` | accepts backup data from the hub | backup node |
| `replica` | receives the replicated data of the hub | redundant hub |

The set is not final; it is what the issues mention today.

## Defaults per type (proposal)

**One binary, one kind of machine.** Every node runs the same `regulus` binary, so in principle a node can be anything; a type is a **preset**: which capabilities are on and which permissions it has. Two lists are needed, one for *what a node does* (capabilities, [#169](https://github.com/Stellatinus/Regulus/issues/169)) and one for *what it may reach or use* (node permissions, [#170](https://github.com/Stellatinus/Regulus/issues/170); not the user permissions of `internal/auth`). Defaults can be overridden **per node**. Anything sensitive is **denied by default**. These tables are a starting point to argue about, not a decision.

Capabilities on by default (✓ on, – off; `consent` = assigned by the type, but effective only once the node agrees in its `allow` list, empty by default; `opt-in` = off until the administrator turns it on **and** the node agrees). Without the node's agreement a `consent` or `opt-in` item is "assigned, not allowed by the node" and is not on: the draft code (`capability.Resolve`) lists it as withheld, and `Has`/`Allows` report it off (#317).

| Capability | Compute | Backup | Access gateway | Development |
|---|---|---|---|---|
| `report` (system information, heartbeat, metrics) | ✓ | ✓ | ✓ | ✓ |
| `self-update` | ✓ | ✓ | ✓ | ✓ |
| `inventory` (read-only machine information, #62) | ✓ | ✓ | ✓ | ✓ |
| `workloads` (#113) | ✓ | – | – | limited (open in #168) |
| `privileged-actions` (#60) | ✓ | – | – | – |
| `plugin-runtime` (#19) | ✓ | – | – | – |
| `backup-storage` | – | ✓ | – | – |
| `relay` | – | – | consent | – |
| `notifications`, `authentication` | – | – | opt-in | – |

Node permissions by default (all denied unless shown):

| Permission | Compute | Backup | Access gateway | Development |
|---|---|---|---|---|
| `reach-hub` | ✓ | ✓ | ✓ | ✓ |
| `reach-peers` (direct links, #113; none exist today) | – | – | – | – (a grant, never a default) |
| `shared-resources` (none exist yet) | – | – | – | – (a **delayed grant**) |
| `egress-network` (what the node may reach outside) | unrestricted | hub only | hub, plus inbound | restricted (open in #168) |
| `inbound-listener` | – | – | consent (only while `relay`, `notifications` or `authentication` is on) | – |
| `receive-backups` | – | ✓ | – | – |
| `privileged-actions` | ✓ | – | – | – |

The hub and the redundant hub are *roles*, not presets of the agent (#104), so they are not in these tables.

## Backup node: the model (#166)

**Agreed.** A backup node keeps the backups of the others. It connects **to the hub only**, like every node, and **cannot contact any other node** (the rule above). It is **not** the redundant hub: that one holds a copy so it can take over (#106); a backup node stores files it cannot read.

1. **What is backed up.** First, the **hub's own archive** ([Hub backup](backup.md), #253): database, `secret.key`, the CA. Then the **restic repositories** of the backup manager ([Managers](managers-and-apps.md#backup-of-a-servers-data-295), #295) for app data and the data the administrator adds. The nodes' **configuration** is part of the hub's database, so it is in the first. **Never backed up:** the plain metrics history by default (#387), caches and downloaded releases, and the data of a container that its app marked as not worth it. Container *definitions* are data of the hub, container *data* is the backup manager's.
2. **How data reaches it: always through the hub.** No short-lived link between nodes. In the first version the hub **pushes the encrypted archive to the node over the node's own agent connection** (a streamed command, like `FetchUpdate` the other way round). Server data with restic is a **second step**: the hub **relays** the repository traffic between the server's agent and the backup node; direct links between nodes stay "later" (below) and are not needed for this.
3. **Encryption and keys.** Encrypted **before it leaves the source**: the hub archive by `age`, a restic repository by restic. **The keys stay with the hub** (encrypted with `secret.key`) and, for restic, are shown once to the administrator; the backup node holds only ciphertext, so a stolen backup node reveals nothing. The node checks a **SHA-256 of each file** on receipt, and the hub asks it for them again when it checks a restore.
4. **Schedule, retention, space.** The sender decides schedule and retention (the hub archive: [Hub backup](backup.md); a server's restic: its own). The node enforces a **quota** (a setting, default 90% of the storage directory's size, never filling the disk) and reports free space. When it is **full**, it refuses new files and the hub raises `backup_node_full` ([alerts](alerts.md)); when it is **offline**, the hub keeps the archive locally (in `data_dir/backups/`) and tries again, and `backup_node_offline` is raised after the configured time. Old files are removed by the **hub's** retention instruction, not by the node's own idea.
5. **Restore.** Started by an administrator on the hub, **level 3** of the model (typed name, delay, step-up, never an API token). Restoring the **hub's archive** is the console restore of [Hub backup](backup.md) on a stopped or fresh hub: **a restore never starts a second active hub**. Until terms exist (#108) the restore asks the administrator to confirm that the old hub is off; with terms the restored hub starts as a candidate and does not take the active role while another active hub holds the term. Restoring a server's data is a restic restore that the hub relays and the backup manager starts.
6. **Capability and permissions.** The capability is `backup-storage`, under the **two-key rule**: the hub grants it **and** the node reports a storage directory and its free space and has agreed (`--allow backup-storage`, empty by default). The permission to configure destinations is `backup.destinations.manage` (admin, step-up); using a destination needs `backup.create`.
7. **Regulus Access (#153).** **A restore is never available from the phone.** The phone may show the state of backups (last time, size, failures) and receive the failure notification; starting a restore needs a browser or the console.

**Rejected:** direct node-to-node transfer; keys held by the backup node; a backup node that can delete or rewrite files by itself.

**Implementation issues** (backlog, after the hub's own data model is settled by #106): (1) The `backup-storage` capability and the node's report of directory and free space. (2) The streamed `StoreBackup` and `FetchBackup` commands and the checksum rules. (3) The quota and `backup_node_full` and `backup_node_offline` conditions. (4) The relay of restic traffic (after #295). (5) `[Test]` with a real backup node and a hub restore.

## Firewall node: the options and the choice (#284)

**Agreed: a later node type built on the firewall manager; nothing built now, and the first step is the firewall manager of one server (#290).** The four options of #284, compared:

| Option | Verdict | Why |
|---|---|---|
| **A. A node type "firewall"** | **Chosen, later.** | A dedicated machine on the path with its own capability and permission set (#169, #170) matches how the other types work, and it can keep filtering when the hub is down. |
| B. A plugin | Rejected | Managers are not plugins in this series ([Managers](managers-and-apps.md#the-model-of-managers-and-backends-287)); a packet filter is exactly what must not depend on plugin isolation. |
| C. A function of the access gateway | Rejected | The gateway faces the Internet and holds nothing secret **on purpose**; adding egress control for the whole group to the most exposed machine would make it the one that matters most. |
| **D. Only read an existing firewall** | **Chosen now.** | Regulus shows the state of the firewall it finds (the host firewall of each server, #290) and of a firewall node's nftables once it exists; it does not manage OPNsense or pfSense. |

- **Engine and rule model.** **nftables** on Linux, driven the way the firewall manager drives it: Regulus renders a **ruleset it owns** (an `inet regulus` table) from a small rule model, validates it with `nft -c`, and loads it atomically. A rule is `{source: node or node type, destination: address, network or name, port, protocol, action, log}`; the **permissions of #170** (what a node may reach) are the source of the allow-lists, translated per source node; the default for a node type that says so is **default deny egress** with the allow-list. A change is **reviewed before it applies**: the dashboard shows the diff of the ruleset and the nodes whose traffic it changes.
- **Routing.** Regulus does **not** reconfigure other nodes' default routes in the first version: a wrong route cuts a node off, and routing belongs to the router and DHCP the administrator already runs. The firewall node **reports whether each node's traffic goes through it** (from the traffic map, #283) and shows the nodes that bypass it. Per-node routes and proxy (HTTP or SOCKS) or DNS-based methods are possible later and each needs a setting on the other node that its own `allow` list accepts.
- **Safety.** A change is **level 2** with the automatic revert (60 seconds) run **by the firewall node itself**, so it works when the hub cannot be reached; the out-of-band path (#112) is how the administrator hears about it. The ruleset always keeps the rules that let every node reach the hub and the hub reach the nodes (the helper refuses a ruleset without them).
- **Failure.** nftables rules are kernel state: **the firewall node keeps enforcing its last ruleset without the hub**. When the **firewall node is down**, the traffic that went through it stops (fail **closed**) unless the administrator marks a rule set **fail open** per rule set (default closed for `strict` sets, open for the others); the link to redundancy (#103): two firewall nodes with a shared address (keepalived or VRRP) are **not built**, and the page says a single firewall node is a single point of failure. A node without a hub behaves as in #111 and keeps the last rules.
- **Interaction.** The firewall's counters and `log` rules are the best source for "where did they go" (**traffic map**, #283); the open-ports view (#282) shows what the rules protect.
- **Privileges.** Loading rules needs root or `CAP_NET_ADMIN`: through the root helper of the managers (#60), never a plugin.

**Implementation issues.** (1) The firewall manager of one server (#290) first (M6). (2) The `firewall` node type as a preset of capabilities, after #169 and #170 (backlog). (3) The ruleset renderer and validator for an `inet regulus` table (backlog). (4) The report of which nodes' traffic goes through it (backlog, with #283). (5) Per-rule-set fail open or closed (backlog). (6) `[Test]` with a real gateway machine (backlog).

## Changing a type and migrating ([#171](https://github.com/Stellatinus/Regulus/issues/171))

Turning a capability on or off moves no data. Some changes do need a **migration**, the backup node first: the general method is always "enroll a new node, give it the type, move the data and the assignments, switch over, retire the old one", with checks and a way back. Not designed yet.

## Direct links between nodes (later)

Two nodes may need to work closely: an application on a compute node calling an AI node that runs Ollama is faster without going through the hub. This is **deliberately left for later** (parked with the other types in #113). The rule to keep: such a link is **authorised by the hub** (the hub says which node may call which, and issues the credentials), not discovered by the nodes, so that revoking a node at the hub also cuts its links.

## Where each type is designed

| Topic | Where |
|---|---|
| Roles, transitions, redundant hub, pure-hub | [hub-redundancy.md](hub-redundancy.md), #103 to #113 |
| Access gateway, phone access | [regulus-access.md](regulus-access.md), #152 and its children |
| Role model and capabilities | #104, #160 |
