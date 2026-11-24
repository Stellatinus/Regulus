# Managers, backends and apps

**Status: design only. Nothing on this page is implemented.** It records the vocabulary and the direction agreed on 2026-11-19, so that the design issues of epic #286 start from the same words. Where a point is still open it says so.

Regulus will do more than observe servers. It will manage some of their functions (RAID, the firewall, containers, a VPN, Wake-on-LAN…) and run third-party software (Plex, Radarr, Sonarr, TriliumNext…). Four words were in use for overlapping things: *extension*, *plugin*, *add-on*, *snippet*. They are replaced by four words with four different meanings.

## The four terms

| Term | What it is | Is Regulus code? | Examples |
|---|---|---|---|
| **plugin** | A program that extends Regulus: a separate process speaking gRPC on a local socket, described by `plugin.yaml` ([plugins](plugins/README.md)). Unchanged. | yes | the code of a backend (when it is not built in), #163 |
| **manager** | A function of a server that Regulus manages, with one or more backends to choose from. | yes (the part that shows and changes its state) | RAID, firewall, containers, VPN, wake |
| **backend** | The real tool behind a manager. **One active backend per manager per server.** Regulus does not write its own version of what a backend already does. | no, it is an existing tool | mdadm, ufw, docker, tailscale |
| **app** | Third-party software that runs **in containers** on a server, described by a declarative manifest and taken from a catalog. | no | Plex, Radarr, Sonarr, TriliumNext |

Not used: **extension** and **add-on** (synonyms of plugin: two words for one thing), and **snippet** (no feature needs it; the word is free if a feature for saved commands or configuration fragments is ever designed).

Why *manager* and not *capability*: the glossary already uses **capability** for what a node type does by default (`relay`, `notifications`, `backup-storage`; #104, #160, #169). Using it twice would make "does this node have the firewall capability?" ambiguous. *Manager* is also the word the maintainer already used ("container management", "WOL manager").

### How they fit together

```mermaid
flowchart TB
    subgraph Regulus
        M[manager: firewall]
        PL[plugin: optional code of a backend]
    end
    M --> B1[backend: ufw]
    M --> B2[backend: firewalld]
    M --> B3[backend: nftables]
    PL -. may implement .-> M
    CM[manager: containers] --> D[backend: docker]
    CM --> P[backend: podman]
    A1[app: Plex] -->|runs in| CM
    A2[app: Radarr] -->|runs in| CM
    A1 -->|asks, never touches the host| M
```

- A **manager** is a function; a **backend** is a tool; a server has at most one active backend for each manager. A manager that the server cannot support (no `ufw`, `firewalld` or `nftables` at all) is shown as unavailable, not as an error.
- A manager can be **built in** or implemented by a **plugin**. The rule is not decided: reading state is a collector in the core; changing state is dangerous and a candidate for a plugin with explicit permissions and isolation (see the open questions).
- An **app** needs no plugin and no Regulus API. It is data (a manifest) plus containers. If an app needs something from the host, such as an open port or a VPN, it asks the matching manager; the app never touches the host itself.

## Managers

The first column of each row is the manager; the backends are the tools Regulus would use. "Observe" means Regulus reads and shows state; "manage" means it also changes it. The scope of each manager is decided in its own issue, not here.

| Manager | Backends (proposed) | Notes | Issue |
|---|---|---|---|
| **Containers** | Docker, Podman | The runtime of apps. The common format is the Compose specification. Kubernetes is out of scope for now. | #289 |
| **Firewall** (of one server) | ufw, firewalld, nftables | nftables is the kernel layer; ufw and firewalld are front-ends on top of it, so they are not four equal choices. Regulus detects the one the distribution uses (ufw on Debian and Ubuntu, firewalld on RHEL and Fedora) and uses one per server. iptables is not supported. Docker and Podman also write firewall rules. Not the same as the firewall node of #284. | #290 |
| **VPN** | Tailscale first, then WireGuard, NetBird or Headscale | Optional, never required (a mandatory overlay was rejected for Regulus Access). Tailscale runs on the host, so it is a backend, not an app. | #291 |
| **Wake** | none to choose | Wake-on-LAN. A magic packet reaches only the local network, so the hub asks an agent on the same network to send it. A new state, *asleep*, next to offline. | #292 |
| **RAID** | mdadm; ZFS and Btrfs as filesystems | Already designed in #285 (observe or manage, storage or servers). LVM is **not** an alternative to mdadm: mdadm builds arrays, LVM builds logical volumes on top. | #285 |
| **Storage and volumes** | LVM, filesystems | The layer between the disks (#147) and the apps: mounts, logical volumes, free space. | #293 |
| **Shares** | Samba, NFS | | #294 |
| **Backup** | restic, borg | The function on each server. The backup node (#166) and the hub backup (#253) are separate issues; their relation is open. | #295 |
| **Reverse proxy and certificates** | Caddy, Traefik, nginx | Publishes apps with TLS. Nothing is public without an explicit choice. | #296 |
| **UPS** | NUT | Clean shutdown, power-return wake-up. | #297 |
| **System updates** | apt, dnf | The operating system's packages. **Not** Regulus's own updates ([updates](updates.md)). | #298 |
| **Scheduled jobs** | systemd timers, cron | One list of what runs on a schedule on a server. | #299 |
| **Virtual machines** | libvirt (KVM) | For what containers cannot do. A decision "build or not" first. | #300 |

The design of the model itself (what a manager is in code, how a backend is detected and chosen, built in or plugin, privileged actions, guards for dangerous operations) is #287.

## Apps

**Decision (2026-11-19): apps run in containers.** The alternative that was weighed was software installed natively in a dedicated volume as a systemd service. It was not chosen because every program would need its own installation, update and cleanup method, and Regulus would have to know each one. A container gives isolation, a clean removal and one format. The worry about "endless containers" is mostly about appearance: a container costs little, and the dashboard shows an app, not its containers.

What an app is, as proposed (the design is #288):

- A **manifest** built on the Compose specification, so that Docker and Podman both read it. Regulus adds what it needs: id, version, title, the Regulus versions it works with, the volumes it declares, the ports, the environment with **secret references** (never secrets in the manifest), the host resources it asks for (a media folder, a GPU) and the permissions it needs.
- A **data volume** per app, in a directory Regulus owns (for example `<apps dir>/<id>/data`). The volume is where the app's data lives; **removing an app can keep or delete it**, and the administrator chooses.
- A **catalog**: an index with checksums and signatures, like the plugin index of #20, with a `source` field so other catalogs can be added later (trust model: #23).
- The same three **update modes** as the core (`manual`, `download`, `automatic`), per app.
- **Redundancy:** a redundant hub replicates the **definition** of an app, never its data ([hub redundancy](hub-redundancy.md) and #113). The data is the business of the backup manager.

The first apps (Plex, Radarr, Sonarr, TriliumNext; then Jellyfin, Pi-hole, AdGuard Home, Nextcloud, Immich) are chosen in #301, after the manifest exists.

## The model and the scope of each manager

These sections are the agreed design (each issue is linked in its heading). They set what each manager observes, what it may change, and the guards; nothing here is implemented.

### The model of managers and backends (#287)

**Agreed.** Every manager follows the same rules, so that none of them invents its own.

1. **A manager is a contract in the core**, not free code: a named set of **observations** (read state) and **operations** (change state), with a typed shape for its data, versioned like the other contracts ([Versioning](versioning.md)): fields are only added. The dashboard and the console show a manager from that data; nothing in the GUI is written per backend.
2. **A backend is detected on the server**, by the agent: the tool is installed, its service is active, its version is known. The agent reports it as `manager`, `backend`, `version` and `state` (`absent`, `inactive`, `active`) with its other data. **One active backend per manager per server.** When several fit (ufw and firewalld both installed), the administrator chooses and the choice is kept in the hub; Regulus never drives two at once. When the detected tool changes (ufw removed, firewalld installed), the manager shows `backend_changed` and **refuses every change until an administrator confirms the new backend**: the rules the administrator saw belong to another tool.
3. **Observing is in the core, changing is a closed list of core operations.** Reading state is a collector of the agent (no root where the tool allows it: `/proc/mdstat`, `findmnt`, `ufw status` through the helper only when it must). Changing state is a **named operation with validated arguments** (`firewall.allow_port{port, proto, source}`), executed by the root helper of #60, **never a shell and never a free command line**. Managers do **not** run as plugins in this series: a plugin cannot be isolated enough until #267 and #261 exist, and a manager that changes a firewall is exactly what must not depend on them. The decision is revisited when #267 ships.
4. **Privilege.** The helper has one entry point per manager and, in it, a list of operations built into the *verified* executable (the same rule as the update helper: it trusts nothing the agent's user could have written). No operation takes a path or a command from the request unless the manager's page names the directory it may touch.
5. **Guards, by level.** Each operation has a level, fixed in the contract:

   | Level | Meaning | Guard |
   |---|---|---|
   | 0 | reads state | the manager's `*.read` permission |
   | 1 | reversible, cannot lock anyone out (open a port, rename a share) | permission `*.manage`, audit entry |
   | 2 | can cut the administrator or the agent off (a firewall rule, a VPN leave, a network change) | level 1, **step-up**, and an **automatic revert**: the hub keeps the previous state and restores it unless the administrator confirms within 60 seconds (the `commit-confirmed` pattern); the revert runs on the agent, so it works when the hub cannot be reached |
   | 3 | destroys data or cannot be undone (wipe a disk, destroy an array, delete a volume, remove an app's data) | level 2 without the revert, plus **typing the name of the target**, a **delay of 60 seconds that can be cancelled**, and never available to an API token (#388) |

   Every level 1 to 3 operation is an audit event with a code and arguments (what, which server, who).
6. **Observe first.** A manager ships its observation before any operation. Whether and when it gets operations is decided in its own section below, and each starts with level 0 only.
7. **In the GUI and the console.** A server page has a **Managers** section; a manager is **off until an administrator turns it on for that server** (an agent that reports `absent` shows "not installed" and nothing else). Observations are also in the console, read-only. Every text is in English and Italian; the Italian word for manager is *gestione*. Each manager has its own permissions (`<manager>.read`, `<manager>.manage`), listed in `users.perm.*` like the existing ones.
8. **What `provides` in the plugin manifest is for** is unchanged and still unused; managers do not use it.

**Rejected:** managers as plugins (above); one generic "run this command" operation (it would move the guard to whoever writes the command); letting a backend be chosen per operation (two tools would disagree about the state); an automatic switch to a newly detected backend.

**Implementation issues.** (1) The common contract: `ManagerState` in the agent protocol (add-only), detection and the `backend_changed` rule (M6, depends on #261). (2) The root helper for managers: one entry point, the operation list and the level checks (M6, with #60). (3) The commit-confirmed revert on the agent (M6). (4) The **Managers** section in the dashboard and the console, English and Italian (M6). (5) `[Test]` for the revert on a real server (M6).

### RAID (#285)

**Agreed: observe, do not manage. Build no new redundancy.** "RAID" means two things and they stay apart: **storage redundancy** (disks) is observed here; **server redundancy** is the hub design ([Hub redundancy](hub-redundancy.md), #103) plus the backup node (#166), and nothing more is built for other workloads: keepalived, Pacemaker or the platform's clustering are shown if present, not replaced. A cluster manager is out; per-container redundancy stays in the backlog (#113).

| Technology | Regulus | Why |
|---|---|---|
| Linux software RAID (`mdadm`) | **reads** (`/proc/mdstat`, `/sys/block/md*/md/`): array, level, members, `degraded`, rebuild progress, failed member | needs no root, in the collector next to SMART (#149) |
| LVM mirror or RAID | reads with the storage manager (#293) | LVM is not an alternative to mdadm: it builds volumes on top |
| ZFS, Btrfs | **reads** pool or filesystem health, scrub state and degraded devices | read-only commands of the tool, through the helper only if root is needed |
| Hardware controllers | **leaves out** | every vendor has its own tool; not now |
| Cloud and virtual disks | **reports only** that redundancy is the provider's | nothing to read or do |

- **Managing** (create, add or replace a member, start a rebuild) is **not in this series**. If it is ever built it is level 3 of the model (typed name, a delay, no API token) and a decision of its own; no operation exists until then.
- **A RAID member** is a disk like any other: it keeps its disk identity (#148), shown on the array; a replaced member is a line in the lifecycle log of #150 (old identity out, new in, rebuild started and finished).
- **What the administrator sees:** health (`clean`, `degraded`, `rebuilding` with percent, `failed`), the members and their SMART state side by side, and an **alert** when redundancy is lost (`raid_degraded`, a new condition of [alerts](alerts.md), critical).

**Rejected:** managing arrays now; a RAID plugin (see the model); a cluster manager.

**Implementation issues.** (1) The `mdadm` collector and `raid_degraded` data in the report (M6). (2) ZFS and Btrfs health in the collector (backlog). (3) The array view on the Disks page (M6, English and Italian). (4) The `raid_degraded` alert condition (with the alerts work).

### Containers (#289)

**Agreed.** Apps run in containers, and the runtime is a **backend**: **Docker** or **Podman**, one active per server (the rule of the model). The common format is the **Compose specification**. **Out of scope:** Kubernetes and Swarm, until someone needs them (#113 keeps the question).

- **Rootful or rootless.** Docker is normally rootful; Podman can run **rootless**. Regulus runs containers **rootless with Podman when the server has Podman and a user to run them as**, and rootful Docker otherwise. The first is preferred because a container escape then lands in an unprivileged user. Which one is used is shown on the server and chosen by the administrator when both exist.
- **Privilege.** Access to the Docker socket is root. **The agent never gets the socket**, and neither does an app. Every container operation runs in the root helper of #60 as a **closed list**: list, inspect, start, stop, restart, logs, pull an image, `compose up` and `down` **for a project Regulus created** from a validated definition. There is no `docker run` with free flags. With rootless Podman the same list runs as the dedicated user instead of root.
- **What an app can never ask for:** `privileged`, the host network, the host PID or IPC namespace, `cap_add` beyond a short list the manifest can name (`NET_BIND_SERVICE`), devices other than the ones a declared resource gives (a GPU), mounting the Docker or Podman socket, or mounting a path outside the volumes and resources it declared. The helper rejects the definition, not only the GUI.
- **What the dashboard shows and does:** each container and its state, start, stop, restart, logs (the last lines, followed), CPU and memory, the image and whether a newer one exists. No shell into a container in this series (that is the node access design, [Web terminal and file browser](node-access.md)).
- **What Regulus stores** for a container is the **record of #113**: identifier, runtime, image reference with its digest, the definition, the declared volumes, the restart policy and where it came from (an app, or found on the server). It never stores the data, and a container it did not create is **shown, not managed**.
- **Firewall.** Docker writes its own rules and a published port can bypass the host firewall. The container manager reports the ports it publishes and the firewall manager (#290) shows them in their own group, "published by containers", so the picture is not wrong by omission.
- **Installing the runtime.** Regulus **detects** Docker and Podman; it does not install them in this series. Installing a package is the system updates manager's work (#298) and would be a level 2 operation there.

This resolves the first item of #113: the container runtime is a backend chosen per server, Docker or Podman.

**Implementation issues.** (1) Detection and the observation of containers, read only, through the helper (M6). (2) The operation list and the definition validator (M6). (3) The containers section of a server in the dashboard, English and Italian (M6). (4) The ports a container publishes in the report (M6). (5) `[Test]` on a real server with Docker and with rootless Podman (M6).

### Firewall (#290)

**Agreed: observe first, then manage with a lock-out guard.** This is the host firewall of **one server**. The dedicated node that all outbound traffic goes through is #284 and stays separate.

- **Front-ends.** nftables is the kernel layer; **ufw** and **firewalld** are front-ends on top. Regulus **detects the front-end the distribution uses** (ufw on Debian and Ubuntu, firewalld on RHEL and Fedora) and uses that one; **nftables directly only when there is neither**. **iptables is not supported.** If both ufw and firewalld are active it reports a conflict and does nothing.
- **Observe (first version).** The enabled state, the default policies, the rules as the front-end lists them (port or service, protocol, source, allow or deny) and the ports that listen, from the open-ports collector (#282), side by side: "listening but not allowed" and "allowed but nothing listens" are the two useful views.
- **Manage (second step), level 2 operations of the model:** open or close a port, allow or remove a source, enable or disable the firewall. **Never offered remotely:** changing the default inbound policy to deny, or enabling the firewall, **unless the rules that keep the agent's connection and the administrator's source are already in the list** (the helper checks this itself); deleting every rule; and anything on a server where the manager cannot tell which connection is the agent's.
- **Lock-out protection.** The agent connects **outbound** to the hub, so inbound rules do not cut it, but outbound rules and a default-deny egress would. Every change goes through the revert of the model: the previous rule set is restored by the **agent** after 60 seconds unless the administrator confirms from the dashboard. A change that is confirmed stays.
- **Docker and Podman** also write firewall rules. The rules they add are shown **in a separate group, "published by containers"**, marked as not managed by this manager; a port in that group cannot be closed from here, and the page says why (the container publishes it).
- **Open ports and the traffic map (#282, #283):** the same data. A port row in the ports view can offer "allow" or "block" once this manager is on; until then it only shows the state.
- **Firewall node (#284) and node permissions (#170).** A server behind a firewall node still has its own host firewall; the two are independent and the page says which one a rule belongs to. A node's permission to reach the network (#170) is *not* a firewall rule and is not written here.
- **What an app may ask.** An app **declares its ports**; the firewall manager opens them **only after the administrator agrees**, in a dialog that lists each port, protocol and source (default: the local network, never "anywhere"). Removing the app offers to close them.

**Implementation issues.** (1) Detection and the observation of rules and policies for ufw, firewalld and nftables (M6). (2) The listening-versus-allowed view (M6). (3) Operations and the helper checks, with the revert (M6, after the model). (4) The firewall section in English and Italian (M6). (5) `[Test]`: a rule that would lock out is reverted, on a real server (M6).

### VPN (#291)

**Agreed.** An optional manager; Regulus never depends on a VPN (a mandatory overlay was rejected, [decisions](decisions.md), "Regulus Access: direction"). **Tailscale first**, because it runs on the host as `tailscaled` and is therefore a backend, not an app; **WireGuard** (`wg`) second; NetBird and Headscale only if someone needs them. **One active VPN backend per server.** A second one needs a reason and a decision.

- **First operations (level 0 and 2).** Observe: connected or not, this server's overlay address, the peers (name, address, online), whether an exit node is in use. Operate (level 2, with the revert, because leaving or changing the exit node can cut the server off): **join** with an auth key, **leave**, **choose or clear the exit node**. **Stays on the command line:** access rules (ACLs), Tailscale SSH, `serve` and `funnel`, subnet routing, key rotation: they decide who can reach what, and that belongs in the VPN's own admin console.
- **The auth key.** It is a secret that lets a machine join a network. The administrator enters it **once** in a form; the hub holds it **encrypted with `secret.key`**, sends it to the agent over the mTLS stream in the `join` operation, and **forgets it after the operation**: it is never returned by the API, never written to the audit log (which records "joined with a key", not the key) and never logged. A key with an expiry or a single use is recommended on the form. There is no stored "reference" to a key in the first version.
- **Relation to Regulus Access.** An overlay address is **a candidate in the address list** of #155 (the phone may use it when it can); Regulus does not make the phone or the hub depend on it, and the manager never edits the hub's settings. The access gateway (#161) is unaffected.
- **Firewall.** A VPN interface may need rules (allow traffic on `tailscale0`); the manager **proposes** them to the firewall manager (#290), and the administrator agrees, as for apps.
- **Several VPNs.** Not at once in the first version: a server that has both Tailscale and WireGuard active shows both, manages the one the administrator chose, and says the other is not managed.

**Implementation issues.** (1) The Tailscale observation (status, peers, exit node) (M6). (2) Join, leave and exit node as helper operations with the secret handling above (M6). (3) The WireGuard observation (backlog). (4) The VPN section in English and Italian (M6). (5) `[Test]`: join and leave on a real tailnet (M6).

### Wake (#292)

**Agreed.** A manager with **no backend to choose**: the magic packet is standard. The **hub asks an agent on the same local network** to send it. A sleeping server is a new state, **asleep**, next to `connected`, `stale` and `offline`: known MAC address, not reachable, and **not an alarm**.

- **How the hub learns where to send.** Each agent reports, with its inventory (#62), the **MAC address and the IPv4 network** (address and prefix) of each interface that has a default route or is up. The hub keeps them per server even while it is off. **A valid sender** for a target is a *connected* agent that has an interface on the **same IPv4 subnet** as the target's last known address; the hub picks the one that was online the longest.
- **What the administrator can do.** **Wake now** (level 1). **Wake on a schedule** (the schedule lives on the hub and uses the scheduled jobs mechanism of #299). **Suspend or shut down** a server (level 3 of the model, with the typed name and the delay) with a guard of its own: **refused** when the server is the only connected sender for another asleep server, or the only way back in, and the dialog says which.
- **Several networks, no agent on the target's LAN.** It **does not work**, and the page says so instead of sending a packet nobody receives: a target with no valid sender shows "no agent on its network" and no wake button. The way out is a **small dedicated node** (a Raspberry Pi running an agent) on that network, as sender. Routed or directed broadcasts are not used.
- **On demand by an app or a user action** (open a media server and the machine starts): **not in the first version.** The first version wakes from a button or a schedule. When built, it needs a **timeout of 5 minutes** after which the server is `failed` to wake, not asleep, and an alert.
- **UPS and node permissions.** The UPS manager (#297) wakes servers **in an order** when power returns, through this manager. Sending a magic packet is a *node permission* in the sense of #170: only an agent that holds it may send one.
- **Asleep versus offline** everywhere: a server is `asleep` only when an administrator suspended or shut it down through Regulus, or it reported "going to sleep" in its last message; any other missing server stays `offline` and **does** raise `agent_offline` ([alerts](alerts.md)). The dashboard, the console, the API (`status: asleep`) and the notifications (#112) use the same word.

**Implementation issues.** (1) MAC and network in the inventory report (depends on #62) (M6). (2) The `asleep` state in the registry and the API, with the reason it is asleep (M6). (3) Wake now and the sender choice (M6). (4) Wake on a schedule (with #299). (5) The guard on suspend and shutdown (M6). (6) `[Test]` with a real sleeping machine (M6).

### Storage and volumes (#293)

**Agreed: observe first; changing is level 3 and comes much later.** This is the layer **between** the disks (#147: health, identity, usage) and the apps. RAID is #285 and sits *below* it; LVM is **not** an alternative to mdadm: mdadm builds arrays, LVM builds logical volumes on top of disks or arrays. The picture the page draws, bottom to top: **disk → RAID array → LVM physical volume, group, logical volume → filesystem → mount → app volume.**

- **Observe (first version).** Filesystems and mounts (`findmnt`, `/proc/self/mountinfo`: device, type, options, size, free space and free inodes), LVM volume groups and logical volumes (`lvs`, `vgs`, `pvs` with JSON output, through the helper because they need root), and the **health of ZFS pools and Btrfs filesystems** (the same read-only commands as #285). Filesystem free space is the data `disk_full` of [alerts](alerts.md) reads.
- **Changing** (create, grow or remove a volume, format, mount) is **destructive and not in this series.** When built: level 3 of the model for anything that removes or formats, level 2 for growing. ZFS and Btrfs get their own pools and **snapshots** later; they are read only now.
- **Mounts that fail or hang.** A network mount that does not answer must not freeze the collector (the guarantee of #243 already holds: a hung mount is read with a timeout). They show as a mount with state **`unresponsive`** (timeout) or **`failed`** (the mount unit failed), in the same list, and raise a `mount_unresponsive` condition in the alerts when it lasts.
- **App volumes.** Each app has its data in `<apps dir>/<id>/data` (the design of apps, [Apps](apps.md)). A volume on **another disk** is attached by **mounting that disk under the apps directory**, or by declaring a *resource* (a media directory) in the manifest; the container never gets a bigger part of the filesystem than that. The storage manager shows which app uses which mount and refuses to unmount or remove one that is in use.
- **Snapshots and quotas:** later, and only where the file system provides them (ZFS, Btrfs, LVM thin). Not in the first version.
- **Relation to other work.** #147 to #151 stay the source for disks (identity, health, usage per service); this manager reads their data and adds the layers above. #285 supplies the RAID layer. The backup manager (#295) may use a snapshot as a consistent source.

**Implementation issues.** (1) The mounts and filesystems collector, with the mount states (M6). (2) The LVM observation through the helper (backlog). (3) ZFS and Btrfs health (backlog). (4) The layered view on a server's Storage page, English and Italian (M6). (5) The `mount_unresponsive` alert condition.

### Shares (#294)

**Agreed: observe first, then manage a file Regulus owns.** Backends: **Samba** (SMB: Windows, macOS, phones) and **NFS** (Linux); one or both on a server.

- **What Regulus manages:** shares, each with a folder, who may read and who may write, and guest access, which is **off by default** and needs a typed confirmation when turned on. **What stays by hand:** everything else in `smb.conf` and `/etc/exports`.
- **Never overwriting a file it does not own.** Regulus writes **its own include files**: `/etc/samba/regulus-shares.conf` and `/etc/exports.d/regulus.exports`. The first time, it asks to add **one `include =` line** to `smb.conf` (the administrator sees the exact line and agrees, level 2, with the revert); an NFS server reads `exports.d` by itself. A share defined by hand elsewhere is **listed as "not managed"** and never changed. A change is written to a temporary file, checked (`testparm`, `exportfs -r` dry run), then renamed in place.
- **Users.** Samba has its own password database. **Regulus never copies a Regulus password into it**, and never stores a Samba password. A share names **system users or groups**; setting a user's Samba password is an operation where the password is typed in the dialog and goes to the helper once, as standard input, to `smbpasswd`, and is forgotten. NFS has no passwords: it is **host-based**, and a share lists the hosts or networks that may mount it; `*` is refused.
- **Apps.** A media app mounts the same folder a share exposes. The manager **shows which apps use a share's folder** and warns when a share and an app disagree about who may write; an app's volume is never shared by default.
- **Firewall.** The ports a share needs (SMB 445, NFS 2049 and the ports of `rpcbind`) are shown and **opened through the firewall manager (#290), with the administrator's agreement**, never silently, and only for a **local network** source.
- **Exposure.** **No Internet by default**: the manager **refuses** to open a share port to a source that is not a private address, or to enable guest access on a server whose firewall allows those ports from anywhere, and says why. A VPN network (#291) counts as private only when the administrator marks it so.

**Implementation issues.** (1) Observation of Samba shares (`testparm -s`) and NFS exports (M-none, backlog). (2) The include-file writer with the checks and the revert (backlog). (3) The Samba password operation (backlog). (4) The shares section, English and Italian (backlog). (5) `[Test]` with a Windows client and an NFS client.

### Backup of a server's data (#295)

**Agreed.** Regulus **does not write a backup format** for this: it uses **restic** (first; **borg** later) as a backend, chosen per server. It is encrypted, deduplicated and incremental, and has its own checks. The three backup issues are **three things with one scheduler and one list of destinations**:

| | What | Format |
|---|---|---|
| **Hub backup** (#253) | the hub's own state: database, `secret.key`, CA | one `age`-encrypted file ([Hub backup](backup.md)) |
| **Backup manager** (this) | data on each server: app volumes, the agent's data, what the administrator adds | a restic repository |
| **Backup node** (#166) | a node type that **holds** repositories and backups for others | not a format: a **destination** of the manager |

- **Destinations.** A **backup node** (#166), another server over SFTP, an **S3-compatible bucket**, or a local disk (said to protect against a mistake, not against the loss of the machine). The destination list is shared: the hub backup's scheduled files may go to the same places.
- **What is backed up by default:** each app's data volume and the agent's own data directory. **What the administrator adds:** folders, picked from the server's file list. Databases are not read from files (below).
- **Schedule, retention, restore tests.** The schedule is a **scheduled job** (#299) on the server, with a retention (`keep-daily`, `keep-weekly`, `keep-monthly`, defaults 7, 4, 6). **A backup that was never restored is not a backup:** once a month the manager runs `restic check` and **restores a small sample into a temporary directory**, compares its checksums and reports the result; a repository whose last test failed or is older than 45 days is shown as "untested", not "ok".
- **Consistency.** An app declares in its manifest how it is backed up consistently: `stop` (stopped for the copy), `dump` (a command in the container that writes a dump, for example for a database) or `snapshot` (a filesystem snapshot, #293). An app that declares nothing is backed up **as it is**, and the page says it may not be consistent.
- **Keys.** The restic repository password is **generated by the hub**, stored **encrypted with `secret.key`**, and **shown once** for the administrator to keep offline: **losing it loses the backups**, and the dialog says so before it creates the repository. A restore on a new hub needs that password; the [hub backup](backup.md) carries it with `secret.key`.
- **Notification** when a backup fails or is late: the `backup_failed` and `backup_late` conditions of [alerts](alerts.md).

**Implementation issues.** (1) The restic backend: create a repository, run, retention, `check` (backlog). (2) The destination list shared with the hub backup (backlog). (3) The sample restore test (backlog). (4) The app manifest's consistency field (with apps). (5) The backup section, English and Italian (backlog).

### Reverse proxy and certificates (#296)

**Agreed.** Backends: **Caddy** first (the simplest, and it gets certificates by itself), **Traefik** second (it reads container labels), **nginx** later; **one per server**. Nothing is published to the Internet without an explicit choice.

- **Certificates.** ACME (Let's Encrypt): the **HTTP-01** challenge in the first version (needs port 80 reachable) and **DNS-01** later, with a provider token that is a secret handled like the VPN auth key (entered once, stored encrypted, never shown). **Private keys stay in the proxy's own data directory**, owned by the proxy's user and `0700`; Regulus never reads, copies or backs up a private key, and the hub never sees one. Regulus reads the **certificate's end date** (to feed `certificate_ending` of [alerts](alerts.md)) and nothing else.
- **How an app publishes.** In its manifest an app declares `publish: { host, port, path }`, for example `media.example.org` to container port 32400. The manager **shows the request**; the administrator **approves** it (level 1) and picks the **exposure**.
- **Exposure.** Three values, shown on every published app: `public` (Internet), `lan` (private networks only) and `vpn` (the VPN's network, #291). **The default is `lan`.** `public` needs a typed confirmation that names the host, and is refused when the firewall (#290) does not allow 80 and 443 from the Internet, with the reason.
- **Regulus itself.** The dashboard behind a proxy is a case of `trusted_proxies` ([Authentication](authentication.md)). The manager **never edits the hub's settings**, not `trusted_proxies`, not `tls_cert`. A published `hub` entry is a suggestion with the lines the administrator would paste, shown once.
- **Firewall.** Ports 80 and 443 are opened through the firewall manager with the administrator's agreement, for the exposure chosen.
- **The proxy's own configuration** is generated into files Regulus owns (a `regulus.d` directory the proxy includes), never into the administrator's own Caddyfile or nginx configuration, as for shares.

**Implementation issues.** (1) The Caddy backend: detect, observe, the generated include (backlog). (2) The app `publish` request and approval (with apps). (3) Certificate end dates in the report (backlog). (4) DNS-01 providers (backlog). (5) The section, English and Italian (backlog). (6) `[Test]` with a real domain (backlog).

### UPS (#297)

**Agreed.** Backend: **NUT** (Network UPS Tools), which also covers a UPS attached to another machine over the network. **Regulus reads and uses NUT; it never talks to UPS hardware** and never replaces `upsmon`.

- **What it shows** (from `upsc`, level 0): on mains or on battery, charge, estimated runtime, load, the UPS model and its status flags (`OL`, `OB`, `LB`). A server that is a NUT *client* shows the same data from the NUT server.
- **What it does.** Almost nothing by itself: the **clean shutdown of each machine stays `upsmon`'s job**, because it works when the network and the hub do not. Regulus adds two things: **notification** and an optional **shutdown order across servers**, where each server has a priority and the order is *storage last* by default (servers that are NAS or hold data shut down after the ones that use them). The order is a plan the hub starts when the UPS reports `OB` for longer than a configurable time (default 2 minutes) or `LB`; each shutdown is a level 3 operation done by an agent, which may be a server already stopping by its own `upsmon`.
- **Notification.** An `ups_on_battery` condition ([alerts](alerts.md), critical) goes through the channels, and through the out-of-band channel of #112 because the network may be down too. A hub that is itself on the UPS may not be able to send it: that is why the alert is raised at the **first** `OB`, not at low battery.
- **When power returns,** servers **wake in an order** through the wake manager (#292), storage first; machines that are only suspended wake, machines that are off need Wake-on-LAN configured, and the page says which are which.
- **Guard.** Starting the shutdown plan by hand is level 3. **If the hub is on the UPS and the UPS says "low battery"**, the hub **does not try to coordinate**: it sends a last notification and **stops orchestrating**, leaving each machine to its own `upsmon`. A shutdown that would stop the only sender of the notification is the last one in the order.
- **Not in the first version:** configuring NUT (drivers, `ups.conf`, users). The page says what it expects to find.

**Implementation issues.** (1) The NUT observation (`upsc`) and the state in the registry (backlog). (2) The `ups_on_battery` condition (with alerts). (3) The shutdown plan and its order (backlog, after #292). (4) The section, English and Italian (backlog). (5) `[Test]` with a real UPS.

### System updates (#298)

**Agreed.** The packages of the **operating system** around Regulus. This is **not** Regulus's own update system ([Updates](updates.md)): the two have separate pages in the interface (**Updates** is Regulus; **System updates** is the operating system), separate permissions (`core.update` and `system.update`) and separate words ("a Regulus release" and "a package update").

- **Backends:** **apt** (Debian, Ubuntu) first, **dnf** (RHEL, Fedora) second. `unattended-upgrades` and `dnf-automatic` are read as a **signal** (installed, last run) and never configured.
- **Observe first (level 0).** Per server: the number of pending updates, how many are **security** updates, the list of packages with the old and new versions, **reboot required** (`/var/run/reboot-required`, `needs-restarting -r`), the last time the cache was refreshed, and held packages. Read-only commands (`apt list --upgradable`, `dnf check-update`) run as the agent's user or through the helper where root is needed. The inventory's package list (#62) is the same data.
- **Apply (level 2).** **Security updates first**, then all, from a **maintenance window** (the same window setting as Regulus's own updates, shared), **one server at a time with a stop at the first failure**, like the rollout of Regulus's own updates ([Updates](updates.md)): a server counts as done when its package manager exits cleanly and, if it rebooted, it is connected again. Dry run (`apt-get -s`) first, shown to the administrator.
- **Privilege.** Package managers run as root: through the **helper of #60, with fixed operations** (`apt.update`, `apt.upgrade`, `apt.upgrade_security`), no packages named by the request. Never a general root shell.
- **Reboot.** **Never automatic by default.** The page says a reboot is needed; an administrator may allow a reboot **inside the window**, one server at a time, and the apps on it restart by their restart policy (the container manager, #289). A server that hosts the **hub** is rebooted last and only after the administrator confirms: the dashboard goes away for a moment.
- **A Regulus-aware rule.** The manager **never upgrades the `regulus` package** (Regulus updates itself, or the administrator installs it with `install.sh`; a package manager replacing it would break the update helper's record, #254), **holds** `regulus`, and **warns before upgrading** the container runtime (`docker-ce`, `podman`), the kernel and `systemd`, naming the apps that would restart. The administrator can accept the warning per update.

**Implementation issues.** (1) The apt observation (pending, security, reboot) (backlog). (2) The window shared with Regulus's own updates (backlog). (3) The apply operations and the one-by-one run (backlog). (4) The warnings for the runtime and the kernel (backlog). (5) dnf (backlog). (6) The section, English and Italian.

### Scheduled jobs (#299)

**Agreed.** Many managers need things to happen on a schedule (backups, wake-ups, system updates in a window), and administrators have jobs of their own. Today a failed cron job is silent. This manager makes **everything scheduled on a server one list**.

- **Backends.** **systemd timers** first (they have a state and a journal), **cron read-only**: the crontabs are listed and **never rewritten by hand**, the manager does not parse or edit them beyond showing them.
- **Observe first (level 0).** Per server: each timer or cron entry, what it runs, the **last run** (time and result), the **next run**, and the last exit status. A timer whose last run failed, or that did not run when it was due, is marked.
- **Create and edit** (level 1 or 2) only for **jobs Regulus owns**: unit files named `regulus-job-<id>.service` and `.timer` in `/etc/systemd/system/`, with a marker line (`# Managed by Regulus`), written by the helper. A job that is not Regulus's is **shown, never changed**.
- **Alerts.** A job that fails or **misses its time** by more than a grace period raises `job_failed` or `job_missed` ([alerts](alerts.md)).
- **One list for the other managers.** The other managers' schedules **appear in the same list**, whoever runs them: the backup of #295 is a Regulus-owned timer on the server; the wake schedule of #292 and the window of the system updates (#298) live on the hub, so they are shown as hub-side jobs with their next run. The decision is **one view, two places that run**: a server's timers stay on the server (so they run when the hub is down), and the hub's schedules stay on the hub (they need the whole group).
- **What a job may run.** This is the dangerous part and **it stays out of the first version.** In the first version a Regulus-owned job runs **only the operations of a manager** (a backup, a system update), never a command line. A "custom command" job comes later, with: a dedicated unprivileged user, **never root**, a short list of commands the administrator approves, the audit log, and the level 3 guard for creating it.

**Implementation issues.** (1) Observation of timers and the cron listing (backlog). (2) The list in the dashboard and the console, English and Italian (backlog). (3) Regulus-owned jobs for managers: the unit writer (backlog, with #295). (4) `job_failed` and `job_missed` conditions (with alerts). (5) Hub-side jobs shown in the same list (backlog).

### Virtual machines (#300)

**Decision: do not build, for now.** The question was whether virtual machines (libvirt with KVM) are needed, or whether containers and a separate node are enough. They are enough for what the project is for today:

- A **container** covers the software the apps list needs (Plex, Radarr, Sonarr, TriliumNext, Jellyfin, Pi-hole…). What a container cannot do is another **operating system**, a Windows guest or a **router appliance**; those are rare for a home or small-office server and have a good answer in a **separate small machine** that Regulus manages as a node, like any other.
- A virtual machine is a **large surface**: disks (the storage manager), networks and bridges (the firewall manager), backups of VM disks (the backup manager), live resources, hardware passthrough, and an image catalog. Each of those is already a manager that is not built yet; adding VMs first would multiply the work of all of them.
- Nothing in the design **assumes containers are the only workload**: the container record of #113 says "machine or container", the managers are one list, and VM support can be added later as one more manager with libvirt as its backend and the same model (observe first, level 2 and 3 guards).

**Revisit when:** someone needs a guest that no container or machine can replace, **and** the storage manager (#293), the firewall manager (#290) and the backup manager (#295) exist, because a VM is built on all three. There is **no implementation issue** and no milestone. A server without virtualization support (no `/dev/kvm`) would show it, if the manager is ever built.

<!--SECTIONS-->

## What exists and what does not

| Part | Issue | Status |
|---|---|---|
| Vocabulary: plugin, manager, backend, app | #286 | **decided** (this page, the glossary, [decisions](decisions.md)); not in any code or UI string yet |
| Model of managers and backends | #287 | design only |
| Apps: manifest, catalog, lifecycle | #288 | design only |
| Container manager (Docker, Podman) | #289 | design only |
| Firewall, VPN, wake | #290, #291, #292 | design only |
| Storage, shares, backup, reverse proxy, UPS, system updates, scheduled jobs, virtual machines | #293 to #300 | design only, backlog (no milestone) |
| First apps | #301 | backlog |

Milestones: the model, the apps, the container manager and the three managers named by the maintainer (firewall, VPN, wake) are in **M6**, because they define how plugins are used and need #261 (what a plugin can do) first. The other managers have no milestone yet.

## Open questions

1. **Built in or plugin**, for the part of a manager that changes state. One rule for all managers is the goal (#287).
2. **Privileged actions.** Changing a firewall, an array or the Docker socket needs root. Whether this uses the root helper of the updates or a `sudoers.d` rule is open in #60; access to the Docker socket is equivalent to root and needs its own answer (#289).
3. **Guards.** An operation that can cut the administrator off (a firewall rule) or destroy data (an array) needs a guard: confirmation, a delay, an automatic revert, the audit log. What each manager must have is open (#287).
4. **Observe or manage**, manager by manager. RAID proposes *observe first* (#285).
5. **Isolation of plugins** that implement a manager (#267) and the permissions they ask for (#261).
6. **Overlap with node-type capabilities.** A *manager* is something a server does; a *capability* (#104, #169) is what a node type is expected to do. Whether a node type enables managers by default is not decided.
7. **Two issues on backup** (#166, #253) and one manager (#295): one mechanism or three.
8. **Italian labels.** The proposal is *gestione* for manager (for example "gestione firewall"), and *backend* and *app* unchanged. They are in the [glossary](glossary.md) and are not final.
