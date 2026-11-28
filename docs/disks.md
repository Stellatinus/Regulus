# Disks: identity that follows a disk (#148)

Part of the inventory epic (#147). Regulus gives every physical disk an **identity key** that belongs to the drive, so one history can follow it when it moves between servers. A disk has no MAC address, so the key comes from what the drive itself reports.

## What the agent reads

For every physical disk, without privileges (`internal/agent/physdisks.go`): the serial number, the WWN (SATA, SAS) or EUI-64 / NGUID (NVMe), the vendor and the model, the size and how it is attached, from `/sys/block/<name>/` (`device/vendor`, `device/model`, `device/serial`, `device/wwid`, `wwid`, `eui`, `nguid`, the SCSI VPD page 0x80 for the serial) and, for what sysfs lacks, udev's database in `/run/udev/data`. It skips loop, RAM, device-mapper and software RAID volumes (their members are reported on their own), optical drives and devices with no medium. An agent in a container reports no physical disks: `/sys` shows the host's. The report carries them in `MetricsReport.physical_disks` with their own version, `disk_identity_version` (1); both are added in agent protocol 1 without a new protocol version, so an older hub ignores them and an older agent sends none ([Versioning](versioning.md)).

## The identity key

The hub decides the key (`internal/diskid`), in this order, and stores which kind it used:

1. the **WWN, EUI-64 or NGUID**, as lowercase hexadecimal (`wwn:5000c500a1b2c3d4`, `eui64:…`, `nguid:…`);
2. the **model plus serial number** (`model-serial:st4000vn008|za1abc23`).

Never used: the device name (`sda`, `nvme0n1`), a filesystem or partition UUID, the hostname. They change when a disk is moved, renamed or reformatted, and a test checks that two reports that differ only in the device name give the same key.

A disk is **not identifiable**, and gets no key, when it has no reliable identifier: nothing reported (`no-identifier`), a serial that is a filler (`0`, `000000`, `To Be Filled By O.E.M.`: `placeholder-serial`), a disk a hypervisor presents with no WWN (`virtual`: its serial is made up or copied from a template), or a USB enclosure with no WWN (`usb-bridge`: the serial is the bridge's). Such a disk is tracked **per server and slot** only (`slot:<agent>:<device>`) and its record goes when the slot does. Two disks of one report with the same identifier are a **conflict**: both are flagged, both are `duplicate` and not identifiable. There is no guessing.

## What the hub does

The report is not kept in the metrics history. When the disks of an agent change, the hub (in the writer of `seen.go`, never on the agent's stream) matches each disk to a known identity and stores it in `disks`. A disk that was last reported by **another server** is a **move**, recorded in `disk_moves` (the newest 5000 are kept). The migration only adds tables and is expand-only ([Updates](updates.md#rollback-and-the-schema-range)).

- `GET /api/agents/{id}/disks`: the disks of a server, then those it no longer reports.
- `GET /api/disks/moves?limit=`: the moves, newest first.

Both need **`inventory.read`**, a permission of its own: serial numbers identify hardware. `admin` and `operator` have it, `viewer` does not, and the serial is not in the metrics a viewer reads.

## One disk reported by two servers (#382)

When a server reports a disk that the hub has from another server **that is connected and still reports it**, the disk cannot be in two places: a cloned disk, an agent that lies, or a move that has not settled yet. The hub keeps the disk with the server that had it, names the second one (`contested_by` in the API, a new column `disks.contested_by`), flags the disk as **contested** on both servers' pages, and writes **no move**, however many reports follow. The flag ends by itself:

- when the server that had the disk stops reporting it, or goes away, the hub asks the other to report its disks again (its report is otherwise written only when it changes) and that report records the move;
- when the second server stops reporting the disk, the disk is as it was.

The flag is shown only while both servers are connected; a leftover of a server that has gone is not shown. A conflict inside one report (two disks that give the same identifier) is a different thing: both are `duplicate` and not identifiable.

## In the dashboard (#382)

The page of each server (**Servers**, then the server) has a **Physical disks** panel for roles with `inventory.read` (the panel is not drawn, and the API is not asked, for anyone else): for each disk the device, model, serial, size, identity key and its kind (WWN, EUI-64, NGUID or model and serial), and a state (present; not reported now; **not identifiable** with the reason; **conflict** with the other server), then the moves to or from that server. English and Italian. The panel shows the disks of a server that is off too (what it last reported).

## Limits, and what is not done

- **Not verified on real hardware** (#81). The reading is tested against fake `/sys` trees built from the layouts of SATA, NVMe, virtio and USB disks, not against a real disk moved between two machines: #381.
- Agents are trusted for their own inventory. A disk that two **connected** servers report at the same time is flagged (below); a clone that sits in a server that is off is a move when the other one reports it.
- There is no inventory page yet (#62): the identity is shown on the page of each server (below).
- The move is recorded in its own table; the event log of the disk (#150) is not built.
- Serials of cloud volumes (for example an EBS volume reports `vol…` as its serial) are stable but are treated as virtual only when the vendor or model says so.

## The Disks tab, as design v3.2 draws it (#497)

Design v3.2 ([Design system](design-system.md), "Third version") draws a **Disks** part of the node window: for each disk a card with the device and how it is attached, the model, the serial, a health state, the used space, four numbers (temperature, wear, hours of use, reallocated sectors) and three test buttons (short, extended, integrity). Its data is made up. This is what the tab (the plugin, Stellatinus/Regulus-Plugins `plugins/disks`) shows and what it does not:

| In v3.2 | In the tab | Why |
|---|---|---|
| Device, interface, model, serial/ID | **shown**: `device`, `transport`, `vendor` and `model`, `serial` | the agent reads them ([What the agent reads](#what-the-agent-reads)) |
| State pill | **shown**, but the states are the hub's: present, not reported now, not identifiable (with the reason), conflict | v3.2's "Healthy (PASSED)" is a S.M.A.R.T. verdict, not a state the hub has |
| Capacity | **shown** (`size_bytes`) | |
| Used space (%) | **not shown** | the agent reports the use of *filesystems* (the Disk usage widget), not of physical disks; a disk with several partitions, RAID or LVM has no single figure |
| Temperature, wear, hours of use, reallocated sectors, health | **not shown**: one note says why | needs S.M.A.R.T. data the agent does not collect (**#149**); the tab shows nothing rather than an invented value |
| Short, extended and integrity test buttons | **not shown** | a test is a command to a node; no command can be sent to a node from the dashboard ([dashboard.md](dashboard.md)) |
| (not in v3.2) | **shown**: first and last seen, the identity key and its kind, and the moves of disks to and from the server | what the Physical disks panel always showed |

**When S.M.A.R.T. arrives (#149)**, the card gets its four boxes and the health state, and the note goes. What that needs, to be designed in #149 and not before: the agent reading S.M.A.R.T. (it needs a privilege for most devices: an ioctl on the device or `smartctl`, which the agent does not have as an unprivileged user), a field in the report with its own version like `disk_identity_version`, a history on the hub (a table, expand-only) and a new read operation of the plugin's door. The tests need node commands first.

**The tab's place.** The tab sits between General and SSH terminal, as #497 says; v3.2 draws it last (General, SSH terminal, File editor, Disks). #497 and #558 disagree on the order and nothing else depends on it: the maintainer decides, and the order is the core's (`ServerPage.tsx`), not the plugin's.

## Becoming a plugin (the cut is built as proposed; two points are not agreed, #497)

Direction of 2026-11-26 ([Direction](ui-and-apps-direction.md), #497, Stellatinus/Regulus-Plugins#10): the **Physical disks** panel and the moves become the first real plugin, the **Disks** tab (**Dischi**) of the node window. This section is the cut, **as it was proposed and as it is now built**: a tab extension point in the node window, a sandboxed frame, the read door with two read-only operations, and the UI module in regulus-plugins. **Not agreed yet, and not done:** removing the panel from the core page for good (it is only *not drawn* while the plugin's tab is installed: no store release exists yet, so an installation without the plugin keeps its disks screen), and the open points below. Not verified on a real hub or in a real browser frame (a `[Test]` issue is open).

### The cut

| Stays in the core | Moves to the plugin |
|---|---|
| The agent's collection of identities (`internal/agent/physdisks.go`) and the report field | The **screens**: the disks of a node and the moves, as a tab of the node window (a UI module) |
| The identity key and its rules (`internal/diskid`) | The tab's texts, English and Italian |
| The tables `disks` and `disk_moves`, written by the hub's `seen.go` | The plugin's own permission `disks.read` (below) |
| The routes `GET /api/agents/{id}/disks` and `GET /api/disks/moves`, and `inventory.read` | |

**Why this is the smallest cut that really tests the plugin system.** It exercises everything a first plugin needs (installation from the store, a tab added to the node window, a permission, a read of core data, two languages) without making a plugin own a database: a plugin has no migration mechanism, only a data directory ([Runtime](plugins/runtime.md)), and the identity history is exactly the kind of data that must survive a plugin being removed or updated. The collection stays in the core because it must run on every agent, and under the rule that plugins are installed on the hubs only (#496) a plugin cannot run there.

**Consequence: the plugin is a UI-only plugin** (`components.ui`, no `hub` program), so its manifest in regulus-plugins is `components: {ui: {module: ...}}` and the `hub` component of the draft goes away unless a need for hub logic appears. The draft in regulus-plugins has a `hub` component only because the host side is not designed; this proposal removes it.

### What the core must offer first (all open)

1. The SDK importable from another repository (#463). A UI-only plugin needs no Go SDK, so **this does not block a UI-only Disks**, but it blocks every other plugin.
2. A **tab extension point** in the node window, and a **widget loader** that runs the module in the sandboxed iframe of [Plugins](plugins/README.md#how-widget-code-is-isolated-from-the-session-315) (#21, #315).
3. The **read door** (#261): the module asks the dashboard, over `postMessage`, for the disks of node X; the dashboard makes the call to the **existing routes** and the hub checks both the plugin's approved permission and the viewer's own (the narrower wins). Nothing writes.
4. **The permission**: `disks.read` in the manifest, **mapped to `inventory.read`** in the core: the plugin's request is granted only to a viewer who holds `inventory.read`, so `viewer` still never sees a serial number. Until a plugin's permissions are enforced (#61) the hub's check of `inventory.read` on the routes is what protects the data, as today.

### Existing installations and older dashboards

- **No data moves**, so nothing is lost: `disks` and `disk_moves` stay where they are, with their history, whether or not the plugin is installed.
- **The routes stay** (`docs/versioning.md`): an older dashboard keeps working, and the plugin reads the same routes.
- **The panel goes from the core page of the node** only when the plugin exists *and* the store can install it (#497). Until then it stays (today it is not drawn when the plugin's tab is installed): removing it earlier would leave the data with no screen. An installation that does not install the plugin has no Disks tab; that is the point of a plugin, and the release notes of that version must say it.

### What is open (the maintainer decides)

1. Is the **UI-only cut** right, or must the plugin own the identity history (then it needs a storage and migration mechanism first, which is a large design of its own)? **Proposed: UI-only.**
2. Should Disks be **installed by default** on a fresh hub, so that the node window does not lose a screen it has today? Proposed: preinstalled by the installer once the store can do it, never silently re-added after an administrator removes it.
3. Does the **move of disks** (a hub-wide list, not per node) get its own entry point, or only a section of the tab? Proposed: a section of the tab, as the issue says ("not a separate page").

Not verified: all of the above is a proposal; the plugin has a draft manifest in regulus-plugins (`plugins/disks`) and no program.
