# Network visibility: open ports and the traffic map

**Status: PROPOSAL, awaiting the maintainer's agreement. Nothing on this page is implemented**, and the kernel facts it relies on have not been checked on a real machine yet (see [What has not been verified](#what-has-not-been-verified)). It answers the questions of two design issues:

- [#282](https://github.com/Stellatinus/Regulus/issues/282) **Open ports**: every listening port of every server, the address it listens on, and which process owns it.
- [#283](https://github.com/Stellatinus/Regulus/issues/283) **Traffic map**: the real communication links of each node, whether they are in use, and where outbound traffic goes.

They are designed together because they read the same kernel tables, need the same privilege decision (#60) and the same permission rules. The visual prototype is [`design-drafts/network-v2.html`](../design-drafts/network-v2.html) (example data, machine-room v2 style).

Today the agent reports only interface byte counters (`NetworkMetrics`, read from `/proc/net/dev`), and the dashboard leaves virtual interfaces out of the network total (`dashboard/src/networkInterfaces.ts`, #225). Nothing below exists in the code.

## Summary of the proposal

| Question | Proposal | Rejected |
|---|---|---|
| Data source | The kernel's socket tables through **`sock_diag` netlink** (what `ss` uses), with `/proc/net/{tcp,tcp6,udp,udp6}` as a fallback. Unprivileged. | Packet capture, eBPF, conntrack as the default source |
| Who owns a port, without root | The socket's **user** (always) and its **systemd unit or container** (from the socket's cgroup, on recent kernels). The process id and name only for the agent's own processes. | Giving the agent `CAP_SYS_PTRACE`; running it as root |
| Who owns a port, with privilege | One fixed, opt-in operation of a root helper (#60) that maps socket inodes to process names. | `sudo ss -p` (blocked by `NoNewPrivileges`, and output parsing) |
| Traffic volume | Per-connection TCP counters (`tcp_info`: bytes acked, bytes received) sampled every report interval; deltas summed per destination. UDP: endpoints only, no volume. | Interface counters as "in use" (a guess) |
| Regulus's own traffic | Counted **exactly** inside Regulus (agent connection, update download, replication) and labelled by kind. Everything else is *other* or *unknown*, never a guessed protocol. | Labelling by port number |
| One collector | One network-state collector in the agent; inventory (#62) shows its data and does not collect ports again. | A ports collector plus an inventory ports section |
| Hub storage | Ports: current state plus a change log (not a time series). Traffic: summaries per destination per interval (5 min for 48 h, 1 h for 30 days), inside a byte budget. | Raw connections; metrics-style samples of ports |
| Protocol | New optional messages and fields only; the agent sends them only when `Welcome` asks. No protocol version bump. | A new RPC; putting ports into `MetricsReport` |
| Names | Address only by default. Reverse DNS by the hub, opt-in, labelled "reverse name". | Sniffing DNS queries or TLS SNI |
| Privacy | Destination addresses are **off by default** per node; the hub enables them, the node can refuse locally. | Always-on destination logging |
| Permissions | `ports.read`, `traffic.read`, `destinations.read`, `traffic.configure`; admin only by default; reads audited. | Showing it to everyone with `servers.read` |

## 1. Collecting without root

The agent runs as the unprivileged `regulus` user with an empty capability set and `NoNewPrivileges=true` (`packaging/regulus-agent.service`). What that user can read:

| Source | What it gives | Readable by `regulus`? |
|---|---|---|
| `/proc/net/tcp`, `tcp6`, `udp`, `udp6` | Every socket of the agent's network namespace (the host's): local and remote address and port, state (`0A` = listening), owner **uid**, socket **inode**, queue sizes. No byte counters, no process. | yes, all sockets of all users |
| `sock_diag` netlink (`NETLINK_SOCK_DIAG`, `inet_diag`) | The same, filtered in the kernel (only listeners, only established), plus `tcp_info` (`tcpi_bytes_acked`, `tcpi_bytes_received`: Linux 4.1+; `tcpi_bytes_sent`: 4.19+), the `IPV6_V6ONLY` flag, and the socket's **cgroup id** (Linux 5.8+, to verify). | yes for listing and `tcp_info` (this is how `ss -tlni` works for a normal user, to verify); `ss -p` needs more, below. **Needs `AF_NETLINK`** in the unit's `RestrictAddressFamilies`, which today allows only `AF_UNIX AF_INET AF_INET6`. |
| `/proc/<pid>/fd/*` | Which process holds which socket inode (this is what `ss -p` and `lsof` read). | **only for processes of the same user** (the kernel's ptrace read check). For everyone else: no. |
| `/proc/<pid>/cgroup`, `/proc/<pid>/comm` | The unit or container of a process, its short name. | yes, unless `/proc` is mounted with `hidepid=` |
| `/sys/fs/cgroup` (cgroup v2) | Maps a cgroup id (the directory's inode) to a path such as `system.slice/postgresql.service` or `system.slice/docker-<id>.scope`. | yes (read-only under `ProtectControlGroups`) |
| `/etc/passwd` | uid to user name | yes |

So, **without privilege**, for every listening port the agent knows the address, the port, the protocol, the user (`postgres`, `root`, `www-data`) and, on kernels that report the socket's cgroup, the **systemd unit or container** that owns it (`postgresql.service`, `docker.service`, `user@1000.service`). That answers "which service is this?" for most servers. It does **not** know the process id or the executable of another user's process. The owner of each row records how it was found: `process` (exact), `unit` (from the cgroup), `user` (uid only), `unknown`.

**The privileged part (opt-in, tied to #60).** Exact process names for every socket need to read other users' `/proc/<pid>/fd`. Options:

| Option | Verdict |
|---|---|
| **A fixed operation of a root helper**: input none, output a map *socket inode → pid, process name (`comm`), executable path, unit*, only for sockets the agent asked about, nothing else (no command lines, no environment). Same pattern as the update helper: fixed, audited, argument-free. | **Proposed**, as one of the privileged actions of #60, off by default, granted per node by an administrator. |
| Give the agent `CAP_SYS_PTRACE` (or `CAP_DAC_READ_SEARCH`) | Rejected: `CAP_SYS_PTRACE` lets the agent read the memory of every process, which is root in practice. |
| `sudo ss -tlnp` from a `sudoers.d` rule | Rejected: `NoNewPrivileges=true` blocks `sudo`; parsing `ss` output is fragile. Revisit only if #60 chooses `sudoers.d` for everything. |
| Run the agent as root | Rejected by the privilege model (#46). |

**Cost.** One netlink dump of the listening sockets every 30 s, and one of the established TCP sockets every traffic interval (the metrics interval, 10 s by default), is a few hundred small records on a typical server: well under a millisecond of CPU (estimate, to measure on a Raspberry Pi class machine; see the [Test] issue). Memory: the flow table is capped (4,096 entries, about 1 MB). A listener that lives for less than the poll interval can be missed; the page says "sampled every 30 s".

**Rejected sources:**

- **Packet capture** (`AF_PACKET`, libpcap): needs `CAP_NET_RAW`, costs CPU per packet on small machines, and sees content, which Regulus must not collect.
- **eBPF** (per-connection accounting, short connections, exact process): the best data, but needs `CAP_BPF` and `CAP_PERFMON` (or root), a kernel with BTF, and a large amount of code. Revisit as a privileged tier if sampling proves too coarse.
- **conntrack** (`/proc/net/nf_conntrack`, netlink events): needs `CAP_NET_ADMIN`, exists only with netfilter loaded, counts bytes only with `nf_conntrack_acct=1`. It is the right source **on a firewall node** (section 9), not on every server.
- **Interface counters** for "in use": they cannot tell which link carried the bytes. They remain the **total**, and the map shows the part of the total it could not attribute.

## 2. Open ports (#282)

For every server, every listening TCP socket and every bound UDP socket:

| Field | Source | Note |
|---|---|---|
| protocol | `tcp`, `udp` | |
| address, port | socket table | `0.0.0.0`, `::`, `127.0.0.1`, a specific address |
| **exposure** | computed by the agent | `loopback` (127.0.0.0/8, `::1`), `lan` (a specific address on a physical interface), `all` (`0.0.0.0` or `::`), `container bridge` (an address on `docker0`/`br-*`), `vpn` (an address on `wg*`, `tailscale*`). `::` without `IPV6_V6ONLY` also covers IPv4 and is shown once, as `all`. |
| owner | user, unit or container, process when known | with the owner source (`process`, `unit`, `user`, `unknown`) |
| Regulus | yes when the listener is Regulus itself (hub HTTP `listen`, agent port), known exactly from its own settings | the check of #197 runs here: a Regulus listener on a non-loopback address without TLS is flagged |
| expected | declared, acknowledged or unexpected | section 8 |
| first seen, last seen | hub | |

**Exposure is where it listens, not whether it is reachable.** A firewall can still block a port that listens on all interfaces. The column says "listens on"; reachability needs the firewall manager (#290) and is a later column ("allowed by firewall: yes, no, unknown").

**The hub-wide page** (**Network → Open ports**): one table of all ports of all servers, filterable by port, server, process or unit, protocol and exposure ("reachable from outside the machine" = `lan`, `all`, `vpn`; "loopback only"); a **group by port** view that shows, for each port, on which servers it is open and with which owner, so that servers that should be alike and are not stand out; and the **change log**.

**Change log.** The agent sends its full set of listeners when it changes (and in full every 15 minutes, so the hub can correct a missed change); the hub compares it with the stored state and writes one row per difference: `appeared`, `gone`, `owner changed`, `exposure changed`. The first snapshot of a newly enrolled agent is one `baseline` row, not forty `appeared` rows. A port that appears and disappears more than 6 times in an hour is collapsed into one `flapping` row. An `appeared` row for an unexpected port is the event a future alert rule can use (#112 for the delivery channel; the rule itself needs an alerting design, which does not exist yet).

**Read-only.** Regulus reports ports; it never closes or opens one. Opening and closing belongs to the firewall manager (#290) and the firewall node (#284).

## 3. Traffic map (#283)

### What a link is

A **link** is a line on the map between two endpoints, summarising every connection observed between them in an interval. Endpoints are: a node (hub, redundant hub, any agent), the **local network** (an address in a private range that is not a known node), a **VPN peer** (an address on an overlay range, 100.64.0.0/10 or a `wg` peer), and the **Internet** (everything else). Each link has:

- **kind**: what the traffic is, with how Regulus knows it (below);
- **state**: `down` (an expected link is missing, for example the agent link of an offline node), `idle` (the connection exists but carries only keepalives, under 512 B/s on average), `in use` (more than that), `unknown` (no data for the interval, for example an agent too old to report);
- **direction and rate**: bytes out and in over the last interval, from the node's side;
- **since**: how long the link has been up.

### Kinds, and labelling honestly

| Kind | How Regulus knows | Exact? |
|---|---|---|
| **agent protocol** | The agent's own gRPC connection to the hub. The agent counts the bytes in its dialer and knows the 5-tuple; the hub counts its side. | **exact** |
| **update download** | On an agent: the `FetchUpdate` stream over the agent connection, counted by the agent. On the hub: its HTTPS download of the release from GitHub, counted in its own HTTP client, with the host name it dialed. | **exact** |
| **replication** | The hub-to-redundant channel (#107), once it exists; Regulus opens it and counts it. | **exact** (future) |
| **plugin** | A socket whose owner is a plugin process; plugins are children of the agent and run as the same user, so their sockets are readable. | exact owner, sampled bytes |
| **other** | Any other socket, with the owner Regulus could read (process, unit, user). | sampled; owner as known |
| **unknown** | Traffic on the interface counters that no socket accounts for (UDP volume, short connections, containers behind NAT, kernel WireGuard). Shown as one grey "unattributed" share of the node's total. | it is the remainder, labelled as such |

Never inferred: a protocol from a port number. The page may show `443 · usually HTTPS` as a **hint**, in lighter text, never as the kind.

### Direction

A connection whose local port is one of the node's listeners is **inbound**; any other is **outbound** (the node opened it). This is how `ss` users read it too; it is right for ordinary services and clients.

### Outbound destinations

For traffic that leaves the group (VPN peers and the Internet), when destination collection is on for the node (section 6): remote address, remote port, protocol, **name if known** (section 5), owner (process, unit or user), bytes out and in, number of connections, first and last seen, per interval. "Where they went" means **destinations, never content**: no payload, no URL path, no DNS query log, no TLS server name.

### Avoiding double counting (containers, VPN, overlays)

- **Count sockets, once, in the namespace where they live.** The agent reads the host's namespace. A connection is counted from its socket, never also from the interfaces it crosses.
- **The total** stays the interface total of #225 (virtual interfaces excluded: `veth*`, `docker*`, `br-*`, `virbr*`, `cali*`, `flannel*`, `cni*`, `wg*`, `tailscale*`, VLAN sub-interfaces…, the same list as `dashboard/src/networkInterfaces.ts`). The **unattributed** share is total minus attributed, never below zero.
- **VPN.** Traffic to a VPN peer is a VPN link. The tunnel's own outer traffic (`tailscaled`'s UDP socket, labelled *VPN transport*) is the same data again, so it is drawn but **not added** to the outbound total. Kernel WireGuard has no user socket: its outer bytes appear only as unattributed.
- **Containers.** A container's connections live in its own network namespace and leave the host through NAT, so they are not host sockets. Until the container manager (#289) exists they are part of the unattributed share. With it, the agent reads each container's namespace (`/proc/<container pid>/net/tcp`, readable without privilege on current kernels, to verify) and labels the flows with the container name; container namespaces and host namespace never overlap, so nothing is counted twice.

## 4. Relation to the server inventory (#62)

**One collector, not two.** #62 lists "listening ports with the owning process where readable" under its network area. The proposal: the agent gets one **network-state collector** (sockets, listeners, flows, interface addresses) and the inventory **shows** its listeners on the server's Inventory page instead of collecting them again. Ports are not part of the inventory snapshot because they change often and need a change log; the inventory snapshot is taken on request. Interface addresses (also in #62) are needed here for the exposure and the endpoint classification, so this collector reads them and the inventory reuses them too.

Order: open ports can ship before the inventory (both are in M4); the inventory issue is updated to point at this collector.

## 5. Names, reverse DNS and privacy

| Option | Verdict |
|---|---|
| **Address only** | **Default.** |
| **Reverse DNS (PTR) by the hub**, cached 24 hours, labelled "reverse name" | **Proposed, opt-in per group** (`traffic.configure`). It is often not the name that was asked for (a CDN address resolves to the CDN), and every lookup tells the DNS server which addresses the group talked to. Done by the hub, not by each agent, so one cache serves all and an agent makes no extra traffic. |
| **Names Regulus knows exactly** | Always: a known node is shown by its node name; the hub's own update source is shown by the host name it dialed. |
| The name the process asked for (DNS queries) | Rejected for now: needs packet capture or the resolver's logs (`resolvectl monitor` needs root), and it is a log of every name the machine looked up. |
| TLS server name (SNI) | Rejected: needs packet capture and reads into the traffic. |

**What is stored:** per interval, address, port, protocol, name (only if the opt-in is on), owner, bytes, connection count, first and last seen. **Never stored:** payload, URLs, DNS queries, SNI, process command lines or environment.

**Who the data is about.** On a server it describes services; on a machine a person uses (a development node, a desktop) a destination list is close to a browsing history. Monitoring the connections of employees is regulated in several countries; the administrator who turns it on is responsible for that, so the default is off, turning it on is audited, and the node can refuse.

## 6. Permissions and audit

These are **user permissions** (`internal/auth`), not node permissions (#170).

| Permission | Allows | Default |
|---|---|---|
| `ports.read` | The Open ports page, a server's ports, the change log. | `admin` only. It maps the attack surface, like the inventory (`inventory.read`, #62). |
| `traffic.read` | The traffic map and the links table: kinds, states, rates, owners, and the outside endpoints **as totals** (Internet, local network, VPN). | `admin` only |
| `destinations.read` | The list of outbound destinations (addresses, names, ports). | `admin` only |
| `traffic.configure` | Turning destination collection on or off per node, reverse names, the idle threshold. | `admin` only |

Custom roles (#61) can give `traffic.read` without `destinations.read`: someone who watches link health need not see where people went.

**Two keys for destinations** (the same idea as #164): the hub turns collection on per node (`traffic.configure`), and the node can forbid it locally (`network.destinations: false` in `agent.yaml`, which the hub cannot change). Deny wins. Without destinations, the map still shows every link and the outside totals, and the agent sends no remote addresses for outside traffic at all.

**Audit** (authenticated events, the 100,000-row class of #200): turning collection on or off and the reverse-name setting (`traffic_collection_changed`), every view of a node's destinations (`destinations_viewed`, at most one row per user, node and hour), exports, and "refresh now". Views of the ports list are audited like inventory reads (`ports_viewed`, one row per user and hour).

## 7. Hub storage, retention and budget

The metrics history is in memory today (`internal/hub/metrics.go`); ports and traffic summaries are **stored in the database**, so the page works after a hub restart and while an agent is offline ("as of" the last snapshot). One migration per backend (SQLite and PostgreSQL).

| Table (proposed) | Content | Retention and limits |
|---|---|---|
| `listening_ports` | current state, one row per listener per agent | replaced by each snapshot; at most 1,024 rows per agent |
| `port_changes` | the change log | 180 days, and the newest 5,000 rows per agent (oldest dropped first) |
| `traffic_5m` | summaries per agent, 5-minute bucket, kind, endpoint class, destination (when on), port, owner: bytes out/in, connections, last seen | 48 hours; at most 256 rows per agent per bucket (the rest merged into one "other destinations (N)" row) |
| `traffic_1h` | the same, rolled up hourly | 30 days |
| `reverse_names` | address, name, time | 24 hours (only when the opt-in is on) |

**Budget (in line with #244).** Rough size: 20 agents × 288 buckets a day × 50 rows ≈ 290,000 rows a day, about 30 MB at ~100 bytes per row; 48 hours plus 30 days of hourly rows stays under 150 MB. A global byte budget for the traffic tables (default 256 MB, a setting) drops the oldest buckets first, and the hourly rollup runs before the 5-minute rows are dropped. These numbers are estimates to confirm by a test that fills the tables.

**Live state** (the map "now"): the latest traffic report of each agent, in memory like the metrics, bounded by the same per-agent caps.

**Replication** (#106): whether port history and traffic summaries are copied to a redundant hub is open there; the proposal is "ports state and change log yes (small, useful after failover), traffic summaries no (regenerated)".

## 8. Expected ports per node type

| Option | Verdict |
|---|---|
| **Declared sources only** | **Proposed.** A port is *expected* when something Regulus knows declares it: Regulus's own listeners (exact, from its settings); a node type preset (from the capabilities of #169: the hub's dashboard and agent port, the access gateway's relay port); an app manifest (#288 declares ports); a manager that opened it (#290). An administrator can also **acknowledge** a port on a node ("known", with a note; audited). Anything else is **unexpected** and stands out. |
| A built-in list of "usual" ports per distribution (22 for SSH, 53 for a resolver…) | Rejected: a guess that turns noise into silence; the administrator acknowledges SSH once per type instead. |
| Alert on every new port | Rejected: without a baseline it is noise from day one. |

Expected entries are rules (`tcp/22 on all nodes of type compute`, `udp/41641 by tailscaled`), not single rows, so that a reinstall does not need a new acknowledgement. The type presets wait for #169; acknowledging works without it.

## 9. Relation to #197 and to the firewall (#284, #290)

- **#197 (do not listen on plaintext HTTP on all interfaces by default).** The ports page makes the result of #197 visible on every server: the hub's HTTP listener is marked as Regulus's own, with the warning "plaintext on a non-loopback address" when there is no TLS. It is a view of the same rule, not a replacement for the default.
- **The firewall manager (#290)** changes the firewall of one server. The ports page links to it from a port ("allow / block in the firewall", where the manager exists) and later shows whether the firewall allows the port. Read-only here, write there.
- **The firewall node (#284).** If outbound traffic goes through a firewall node, that node is the **best source for the outside view**: its conntrack sees every connection of every node, including containers, short connections and devices with no agent, with exact bytes. Then the map takes outside destinations from the firewall node (marked "seen at the firewall") and keeps the per-node sockets for the owner. The same connection, seen at the node and at the firewall (after NAT), is matched by its tuple and counted **once**, with the firewall's bytes. The privileged collection on the firewall node belongs to #284.

## 10. Agent protocol change (sketch)

Following [versioning.md](versioning.md): **add only, no `protocol.Version` bump, update contract untouched.** The agent sends the new messages **only when the hub asks in `Welcome`**, so an old hub never receives them (an old hub would ignore an unknown payload anyway: its `handle` switch has no default case). An old agent ignores the new `Welcome` field and answers the new command "unsupported command"; the dashboard says "agent too old for network data (needs 0.x)".

```proto
// AgentMessage: two new payloads.
message AgentMessage {
  oneof payload {
    // 1–4 unchanged
    NetworkState network_state = 5;
    TrafficReport traffic = 6;
  }
}

// Welcome: what the hub wants. Absent (an old hub) = nothing.
message Welcome {
  // 1–7 unchanged
  NetworkCollection network = 8;
}

message NetworkCollection {
  bool ports = 1;
  bool traffic = 2;
  bool destinations = 3;          // the hub's key; the node's local setting can still refuse
  google.protobuf.Duration traffic_interval = 4;
  uint32 max_flows = 5;
}

// Sent when the set of listeners changes, and in full every 15 minutes.
message NetworkState {
  google.protobuf.Timestamp collected_at = 1;
  repeated Listener listeners = 2;
  repeated InterfaceAddress addresses = 3;   // for exposure and endpoint classes
  OwnerVisibility visibility = 4;            // what the agent could read: uid, cgroup, process, privileged
}

message Listener {
  Transport transport = 1;        // TCP, UDP
  bytes address = 2;              // 4 or 16 bytes
  uint32 port = 3;
  bool v6only = 4;
  Exposure exposure = 5;          // LOOPBACK, LAN, ALL, CONTAINER_BRIDGE, VPN
  Owner owner = 6;
  bool regulus = 7;               // the agent's or hub's own listener
}

message Owner {
  uint32 uid = 1;
  string user = 2;
  string unit = 3;                // systemd unit or scope
  string container = 4;
  string process = 5;             // comm, when readable
  uint32 pid = 6;
  OwnerSource source = 7;         // UNKNOWN, USER, UNIT, PROCESS, PRIVILEGED
}

// One per traffic interval.
message TrafficReport {
  google.protobuf.Timestamp start = 1;
  google.protobuf.Timestamp end = 2;
  repeated Flow flows = 3;
  uint32 dropped_flows = 4;       // over max_flows, merged
  bool destinations_refused = 5;  // the node's local setting
}

message Flow {
  Direction direction = 1;        // OUTBOUND, INBOUND
  Transport transport = 2;
  bytes remote_address = 3;       // empty for outside traffic when destinations are off
  uint32 remote_port = 4;
  uint32 local_port = 5;
  Owner owner = 6;
  FlowKind kind = 7;              // UNKNOWN, AGENT_PROTOCOL, UPDATE_DOWNLOAD, REPLICATION, PLUGIN, OTHER, VPN_TRANSPORT
  uint64 bytes_out = 8;
  uint64 bytes_in = 9;
  bool bytes_known = 10;          // false for UDP
  uint32 connections = 11;
  google.protobuf.Timestamp last_seen = 12;
}

// Command: one new kind.
message Command { oneof kind { /* 2–4 unchanged */ RefreshNetwork refresh_network = 5; } }
message RefreshNetwork {}
```

Also: the hub's limits (`internal/hub/limits.go`, `sanitizeReport`) get caps for the new lists and strings; an update-only session drops both messages; golden files are added for `NetworkState` and `TrafficReport`; `buf breaking` passes because nothing is renumbered. The names above are a sketch; the implementation issue fixes them.

**Packaging change:** `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK` in `regulus-agent.service` and `regulus.service` (the hub's built-in agent). Without it the collector falls back to `/proc/net/*`, loses the cgroup id and the `tcp_info` counters, and says so (`visibility`).

## Implementation issues to open

| Issue | Milestone (proposed) | Depends on |
|---|---|---|
| Agent: network-state collector (listeners, exposure, owner from uid and cgroup), `AF_NETLINK` in the units | M4 | this design |
| Protocol: `NetworkState`, `TrafficReport`, `Welcome.network`, `RefreshNetwork`, limits, golden files | M4 | this design |
| Hub: open ports storage, change log, API, `ports.read`, audit | M4 | the two above |
| Dashboard: Open ports page (table, filters, group by port, change log), EN/IT | M4 | hub API |
| Expected ports: acknowledgement and Regulus's own listeners; type presets after #169 | M4 (presets: none, follows #169) | #169 for presets |
| Privileged socket owners through the root helper | M4, with #60 | #60 |
| Agent: traffic sampler (`tcp_info` deltas, flow table, exact labelling of Regulus's own traffic, endpoint classes) | M4 | collector |
| Hub: traffic storage, rollup, budget, destination two keys, reverse names opt-in, `traffic.*` and `destinations.read`, audit | M4 | sampler, #244 |
| Dashboard: Traffic map, links table, destinations panel, EN/IT | M4 | hub traffic API |
| Containers: published ports and per-container flows through the container manager | M6 | #289 |
| Firewall node as the source of the outside view | none, follows #284 | #284 |

## What has not been verified

Nothing on this page has run. In particular, from documentation and memory, not from a test on Ubuntu 22.04/24.04/26.04 under the hardened unit: that `sock_diag` lists other users' sockets and returns `tcp_info` to an unprivileged user; the kernel version that adds the cgroup id; that `/proc/<pid>/net/tcp` of a container process is readable without privilege; the behaviour with `hidepid=`; the CPU and memory cost on a Raspberry Pi. A `[Test]` issue covers them.

## Open questions

1. Are the four permissions the right split, or should `ports.read` simply be `inventory.read` (#62)? The proposal keeps them apart because ports have a change log and alerts, and a role may need ports without users and packages.
2. Destination collection: off by default on every node, or on by default for node types that are servers and off for the development node (#168)?
3. Should the node-side refusal be an opt-out (proposed) or an opt-in like the gateway's listener (#164)?
4. Retention numbers (180 days, 48 hours, 30 days, 256 MB) are proposals.
5. Replication of ports and traffic to a redundant hub (#106).
6. The alert on an unexpected new port needs an alerting design that does not exist yet.
