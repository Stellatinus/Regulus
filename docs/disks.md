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
