# Remote agents

Besides the built-in agent, a server can run `regulus agent` and be managed from the hub. The agent always connects **out** to the hub (gRPC over TLS 1.3), so the managed server needs no open port.

1. **Turn on the listener.** Remote agents are off by default. In the dashboard, **Servers** says so and someone with `servers.manage` turns them on with a port (8443 by default): the hub starts listening at once, keeps it after a restart and records it in the audit log (`GET`/`PUT /api/agents/listener`). Alternatively set `agent_listen: ":8443"` (or `--agent-listen`, `REGULUS_AGENT_LISTEN`) and restart the hub; the configuration always wins and cannot be changed from the dashboard. Open that port in the firewall only towards the servers you manage.
2. **Make a token.** An admin opens **Servers → Add a server** in the dashboard, optionally names the server and gets the command to run, with a one-time token valid for one hour (`ttl` in the API changes that), the port and the **CA fingerprint** of the hub (the same thing `POST /api/enrollment-tokens` returns). The token is shown once.
3. **Enroll the agent** on the other server, once. On Ubuntu or Debian the easiest way is the installer command shown by the dashboard (see [Installation](installation.md)); by hand, with the binary already there:
   ```sh
   regulus agent --hub HUB_HOST:8443 --token TOKEN --ca-fingerprint sha256:... --data-dir /var/lib/regulus-agent
   ```
   The agent first checks that the hub's certificate is signed by a CA with that fingerprint (so the token is never sent to an impostor), creates its own key, and the hub signs its certificate. The key stays on the agent. From then on the agent only needs `--hub` and the saved credentials; the same options can go in a YAML file (`--config`: `hub`, `data_dir`, `log`, `plugins_dir`) or in `REGULUS_AGENT_*` variables. `plugins_dir` (off by default) makes the agent run the plugins of that directory; see [Plugin runtime](plugins/runtime.md).
4. **Revoke** an agent from its server page or with `DELETE /api/agents/{id}`: its connection is closed and it can never connect again; the process stops with an error. To use that server again, delete its data directory and enroll with a new token.

How it is protected: the hub creates its own certificate authority at the first start (`pki/` in the data directory, `ca.key` readable by the owner only). Tokens are random, stored only as a hash, single use, expire after an hour and are rate limited (10 wrong attempts per 15 minutes per address). Every connection needs the client certificate of an enrolled, not revoked agent, and the agent id comes from the certificate, not from what the agent says. Client certificates last five years; there is no renewal yet. A token that was shared by mistake is listed under **Servers → Add a server → Pending enrollment tokens** (or `GET /api/enrollment-tokens`) and can be cancelled there (`DELETE /api/enrollment-tokens/{id}`); an enrollment that fails because of a bad request or a database error does not use the token up. Creating and cancelling tokens, renaming and revoking agents need `servers.manage` and go to the audit log.

**Versions.** An agent and the hub agree on a protocol version when the agent connects (the agent sends the range it speaks, the hub picks the highest both understand). On top of that the hub accepts agents of **its own release series and the previous one**: the same minor version, one minor behind (hub 0.3.x takes 0.3.x and 0.2.x), or any release of the previous major. Updating the hub first is therefore safe: its agents keep working until they are updated too. The **Servers** page marks agents that are behind the hub, or ahead of it (update the hub).

**An agent that is further behind is not lost.** A server that was switched off for months, while the hub moved two release series on, is still enrolled: the hub admits it in an **update-only session** (`Welcome.update_only`). In that session it speaks only the *update contract* (heartbeats, `Ping`, `ApplyUpdate`, `FetchUpdate`; see [Versioning](versioning.md)), which never changes meaning in any protocol version; its metrics are ignored (an agent that knows the field stops collecting them) and no other command is sent to it. The **Servers** page says "too old for this hub: connected only to be updated", and the server is a candidate on the **Updates** page like any agent that is behind: the rollout takes it **straight to the hub's version**, whatever releases it skipped. Only an agent that has no certificate, or that is *newer* than the hub, is still refused (the second with a message to update the hub); it is listed as `incompatible` with the reason. A refusal because of versions does not stop the agent service: it keeps asking, every 30 seconds at most (with a random spread, see below), so it connects by itself once the agent or the hub is updated. A revoked agent still stops for good. Development builds are never judged.

What this does not cover: an agent so old that it cannot verify the signature of today's releases (for example a build from before the repository moved, see [Updates](updates.md)). It is admitted and offered the update, but refuses the files; that server needs `install.sh` once. An agent installed before the update feature existed has no helper and needs the same.

## Supported platforms

Releases are built for **Linux on amd64 and arm64** only (`.deb`, `.rpm` and `tar.gz`; see [Design decisions](decisions.md#platforms) for the order of the distributions). The installer refuses other architectures. What a server needs, and what it gets:

| Case | Status | What works |
|---|---|---|
| Linux amd64 or arm64 with systemd, on its own machine or a virtual machine | **supported** (Ubuntu LTS first, then Debian, then RHEL-like) | everything |
| Linux without systemd (Alpine with OpenRC, a plain container) | runs, not supported | metrics, from the `tar.gz`; no service file, and **no updates from the dashboard**: the update helper is started by systemd path units |
| Linux container: LXC/LXD, systemd-nspawn, Docker, Podman | runs, limited | see below |
| Another Unix (BSD, macOS) or Windows | not built | built by hand, the agent connects but uses the **basic collector**: time and uptime only |

**What each part of a report needs.** The agent reads files, it runs no command:

| Part | Read from | Without it |
|---|---|---|
| CPU usage | `/proc/stat` | "not available" |
| Load average | `/proc/loadavg` | "not available" |
| Memory and swap | `/proc/meminfo` | "not available" |
| Disks | `/proc/mounts`, then `statfs` on each mount | "not available"; a mount that does not answer is left out ([below](#when-a-disk-or-the-collector-does-not-answer)) |
| Network | `/proc/net/dev` | "not available" |
| Uptime | `/proc/uptime` | "not available" |
| System facts (Hello) | `/etc/os-release`, `/proc/sys/kernel/osrelease`, `/proc/meminfo`, the Go runtime (CPU count, OS, architecture) | the field is left empty |

None of these needs root or a capability; the hardened units (`PrivateDevices`, read-only system) do not hide them.

**The agent says what it cannot read.** When `/proc/stat` cannot be read at start, the agent uses the **basic collector**, which sends only the time and the uptime. Every report names its collector (`linux` or `basic`) and lists the parts it could not read (`MetricsReport.unavailable`). The hub passes both on (`collector`, `unavailable` in `GET /api/agents` and in each metrics point), the **Servers** list and the server page mark the server "limited metrics", and each widget says "not available on this server" instead of showing zeros or an empty chart. "Nothing there" is not "not available": a container with no real filesystem reports no disks, which the Disk widget shows as such. An agent older than these fields sends neither, and looks as before.

**Which disks are shown.** Only local filesystems are reported: ext2/3/4, xfs, btrfs, zfs, f2fs, jfs, reiserfs, vfat, exfat, ntfs, ntfs3 and fuseblk, one entry per device (`realFilesystems` in `internal/agent/collector.go`). Left out on purpose:

- **network filesystems** (nfs, nfs4, cifs, smb3, ...): their space belongs to the server that exports them, which reports it with its own agent; and a share whose server is gone could only show up as a mount that does not answer. An NFS server shows its exported disks; its clients do not;
- **overlay, tmpfs and other pseudo filesystems**: a Docker or Podman container, whose root is overlay, therefore shows "No disks reported". Run the agent on the host to see its disks.

**Containers.** The agent reads `/proc` as it finds it, and Regulus does not read cgroup limits (decided in #230; a follow-up may add them):

- **CPU count** is the CPUs the agent may run on (`runtime.NumCPU`, which follows `cpuset`), not a CPU quota (`cpu.max`);
- **memory, load and CPU usage** are the **host's**, unless the container virtualizes `/proc` (LXC and LXD with LXCFS show the container's own memory, CPU usage and uptime);
- **network** is the container's own interfaces.

The agent reports the container it detects (`container` in `GET /api/agents`: the name systemd writes to `/run/systemd/container`, else `docker` for `/.dockerenv`, `podman` for `/run/.containerenv`), and the System widget shows it with a note that CPU and memory may be the host's. For figures that are the server's own, run the agent on the host or in a virtual machine.

## Limits on what an agent can send

An agent is a remote machine, and one that is compromised or buggy must not be able to exhaust the hub. The hub (`internal/hub/limits.go`) enforces:

| Limit | Value | What happens beyond it |
|---|---|---|
| Size of one message on the agent port | 256 KiB | the stream is refused (`ResourceExhausted`) |
| Streams per connection | 8 | the extra stream is refused |
| Texts (host name, versions, mount points, interface names…) | 256 bytes, no control characters | cut, cleaned |
| Disks and network interfaces per report | 64 each | the rest is cut (logged once per stream) |
| Reports | one per half of the metrics interval | the extra reports are dropped (logged once per stream) |

With one report per interval and one hour of history, a single agent keeps at most about 50 KB per report times the number of reports of that hour (for example about 36 MB at the 10 s default in the worst case, a normal host uses a few hundred bytes per report). The hub does not yet keep a global memory budget across agents; the dashboard port also has header and idle limits (`internal/hub/hub.go`).

## When a disk or the collector does not answer

A network or storage mount whose server is gone can block `statfs` for as long as it stays that way, and no signal ends the call. The agent therefore asks every mount at the same time, waits up to **2 seconds** in total, and sends the report **without the mounts that did not answer** (one warning in the log per mount); a mount that is still stuck is not asked again (a new call per report would only pile up stuck threads) and comes back in the reports as soon as it answers. The whole collection also runs beside the loop that sends the heartbeats: while one is still running the next is skipped, so a collector that never returns costs reports, never the connection (the hub drops an agent that stops sending heartbeats). To try it on a real machine see the `[Test]` issue for hung mounts.

## How an agent reconnects

When the connection to the hub is lost the agent waits and tries again: 1 second, then twice as long each time up to 30 seconds (`min_backoff` and `max_backoff` of the agent runtime), and every wait is a **random time between half of the delay and all of it**, so that agents that lost the hub together (a restart) do not come back together. The delay starts over at 1 second only after a connection that **held for at least 30 seconds**: a hub that accepts the agent and drops it at once does not turn the retries into a loop of one per second. If the hub closes the stream because a newer connection with the **same identity** took over, two machines hold one certificate (a cloned disk or virtual machine, a copied `/var/lib/regulus-agent`): the agent logs that and waits the longest delay each time. Give each machine its own enrollment. To see it on a real machine follow the `[Test]` issue for agent reconnection.
