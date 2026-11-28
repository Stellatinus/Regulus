# Verified systems

Which operating systems Regulus has **been run on**, when, on what machine, and at which level. This is the evidence behind the [supported platforms](../README.md#supported-platforms): a system is *supported* when the releases are built for it, and *verified* when a person ran a release on it and wrote the result down. Only this page says which is which. Why: [Design decisions](decisions.md#verified-systems-three-levels-major-releases-only-and-one-issue-per-system-and-level-89).

## When a system is verified

- **On a stable major release only** (`1.0.0`, `2.0.0`), never on a minor or patch release (`1.1.0`, `1.0.1`). A minor release adds things on top of the core (features, plugins) and does not change what is under it, so it does not need a new verification; a change that touches the core's structure is a major release. The features a minor adds go into the lists below and are run at the next major verification. **No stable release exists yet**, so every run so far is on a pre-release and is *provisional*: it is written here, and the system is verified again on `1.0.0`.
- **One verification per system, architecture and level.** The architecture is **amd64** (not ARM: every x86-64 processor, Intel or AMD) or **arm64** (ARM).
- **Three levels:** **standard** (the minimum the program must do), **advanced** (the advanced functions) and **complete** (everything). Each is a list below; a level is *verified* when every line of it was run on that system and architecture.
- **Every run records:** the **kernel** (`uname -r`), the architecture (`uname -m`), each **machine** (role, physical or virtual and on what, CPU, memory, disk), the release, the date and the tester. Logs and screenshots go in the issue.

## The list

| System | Architecture | Standard | Advanced | Complete | Release | Kernel | Machines | Evidence |
|---|---|---|---|---|---|---|---|---|
| Ubuntu 26.04 LTS | amd64 | **partial**, provisional, 2026-11-24 | not run | not run | pre-releases `v0.1.0-alpha.1` (2026-10-28) and `v0.3.0-alpha.1` (2026-11-24); no stable release yet | 7.0 (reported in #89 for the first run; `uname -r` was not captured, and the second run's kernel neither) | a hub VM on Hyper-V and an agent VM (hypervisor not recorded); CPU, memory and disk not recorded | #81, #89, #443, [Verification status](decisions.md#verification-status) |

**Ubuntu 26.04 LTS amd64, standard: what was run.** The `.deb` as a fresh install and as an upgrade from `v0.1.0-alpha.1`, the unit, the built-in agent and its metrics, the update check, the dashboard and the console, a remote agent on a second machine (online, offline, online again, reconnecting after a restart of the hub). **Not run:** a fresh install of the current release, plugins, an update installed from the dashboard and its rollback on this system, `apt remove` and `purge`; the journals were not kept.

A container, CI or a cloud session is not a verification: the `.deb` was run through real `dpkg` on Ubuntu 24.04 in a container without systemd (2026-11-24) and the package lifecycle runs in CI in `ubuntu:22.04`, `24.04` and `26.04` containers, and neither puts those systems on the list.

### Still to verify

| System | Architecture | Issues (standard, advanced, complete) |
|---|---|---|
| Ubuntu 26.04 LTS | amd64 | #536, #545, #546 |
| Ubuntu 26.04 LTS | arm64 | #537, #547, #548 |
| Ubuntu 24.04 LTS | amd64 | #538, #549, #550 |
| Ubuntu 24.04 LTS | arm64 | #539, #551, #552 |
| Ubuntu 22.04 LTS | amd64 | #540, #553, #554 (also decides whether 22.04 stays) |
| Ubuntu 22.04 LTS | arm64 | #541, #555, #556 |
| Debian | amd64, arm64 | release not chosen yet (#543) |
| Windows Server | amd64 only (no ARM) | not built: Regulus releases are Linux only |
| RHEL-like (`.rpm`) | amd64, arm64 | not supported (#401) |

## Standard

The minimum the program must do. Verification of these requirements lists the system as working at the standard level.

**Machines**

- machines recorded (role, physical or virtual and on what, CPU, memory, disk)
- kernel recorded (`uname -r`) and architecture (`uname -m`)

**One node**

- `install.sh` verifies the checksum and the signature and installs the `.deb`
- `regulus.service` is active and the dashboard answers
- the setup token creates the first admin; sign in and sign out work
- the built-in agent feeds the System, CPU, Memory, Network and Disks widgets
- `regulus --version` and `regulus status` are correct
- the service is back after `systemctl restart regulus`
- the service is back after a reboot of the machine
- `apt remove` keeps the data; installing again starts like a first install
- `apt purge` removes the configuration and keeps the data and the user

**Several nodes**

- remote agents are turned on from the dashboard
- **Servers → Add a server**: the printed command installs and enrolls an agent on a second machine
- the agent shows online and feeds a widget
- the agent reconnects by itself after `systemctl restart regulus` on the hub
- agent machine shut down: shown offline; started: shown online
- an enrollment token used twice is refused; a wrong `--ca-fingerprint` is refused

**Updates**

- the update check reports a newer release
- the update installed from the dashboard succeeds and the hub comes back
- a rollback works

**Plugins**

- the plugin sources show as verified
- a plugin is installed from the store (the password and 2FA dialog is in front of the plugin's window and accepts the code), runs, shows in the dashboard with no "permission denied" on the plugins directory and is removed

**Logs**

- `journalctl -u regulus` on every machine has no unexplained error

## Advanced

The advanced functions. Run after the standard level passed. Verification of these requirements lists the system as working at the advanced level.

**Database**

- `install.sh --db-local` installs PostgreSQL, creates the role and database, and the hub runs on it
- a database on another machine with `sslmode=verify-full`
- a wrong password or a missing database stops the installer before anything changes
- moving the data between SQLite and PostgreSQL (not implemented yet: #124)

**Security**

- HTTPS listener (`tls_cert`, `tls_key`) in a browser; a replaced certificate is served without a restart
- `trusted_proxies` behind nginx and Caddy: the client address is the real one
- two-factor sign-in, a recovery code, and step-up re-authentication on a sensitive action
- users and roles: a viewer cannot do what an admin can
- an API token: bearer works, read-only by default, revoked token refused
- `regulus reset-admin` and the reset of a user's credentials
- the sign-in limit and the session lifetime

**Backup and restore**

- `regulus backup` and `regulus restore` on SQLite, with an enrolled agent
- `regulus backup` and `regulus restore` on PostgreSQL, with an enrolled agent

**Updates**

- an upgrade from the previous release keeps the users, roles, audit log and settings
- an edited `config.yaml` survives the package upgrade
- the channels: alpha, beta and stable offer what they should
- update modes: manual, download-only and automatic
- a rollout to several agents, and an agent that fails is judged after the stability window
- an update interrupted by a power cut is settled when the machine starts
- a rollback restores the SQLite database
- the local update policy file is respected

**Monitoring**

- a scrape of `/metrics` by a real Prometheus
- the metrics history of the last hour survives a restart of the hub
- the audit log records sign-ins, changes and updates

**The system itself**

- disk identity: Physical disks panel, a disk moved between two servers, a contested disk
- container limits (memory, CPU, swap from the cgroup) in a real Docker and LXC container
- an agent on a system without `/proc` reports what it could not read
- `regulus console` in a real terminal: Settings, Actions, the log and the reachability check

**Backup node and switching (not implemented yet: #103)**

- a redundant hub is added and keeps up with the active hub
- switching from the active hub to the backup node, and back, with the agents following
- a network split leaves one active hub at most

## Complete

Everything: all of **Standard**, all of **Advanced**, and the rest below.

**Dashboard**

- every widget shows correct values; the range choice (10 minutes, 1 hour) works
- widgets can be added, removed, arranged and the layout is kept
- Nodes page: cards, compact list and map; the node window
- Plugins page: every state is shown with its error; an invalid directory is listed as invalid
- Users, Updates, Settings and Disks pages
- the live indicator: Connecting, Reconnecting and Hub unreachable states
- English and Italian, with no untranslated text and no broken layout
- the themes
- a phone and a tablet
- Chrome, Firefox and Safari
- keyboard use and a screen reader

**Plugins**

- the example plugin runs on the hub and on a remote agent
- a plugin that crashes is restarted; one that cannot start is shown as failed
- a plugin widget appears on the dashboard
- the plugin store: install, update and remove from a verified source; `/usr/local/lib/regulus/plugins` is `0755` root after an upgrade from a version that left it `0700`

**Installer**

- `install.sh` options: `--version`, `--role agent`, `--database`, `--allow-unsigned`
- an unsigned or wrongly signed release is refused
- the closing text of the installer after a first install and after an upgrade

**Console and commands**

- `regulus config`, including `config set`, and `regulus version`
- the console in English and Italian

**HTTP API**

- `/api/openapi.yaml` is served and the endpoints answer as described

**Containers and other systems**

- an agent in a container and on a machine without systemd (runs, no updates)

## Keeping the lists current

This page is **the reference**: every test of a system follows these lists as written here, not an older copy. Every pull request that adds or changes a feature a person can use, or one that depends on the system under it, adds a line for it in the same pull request, or says why none is needed (the rule is in [AGENTS.md](../AGENTS.md)): in **Standard** if it is a minimum requirement, in **Advanced** if it is an advanced function, and always in **Complete**. The `[Test]` issues are copies made when they were opened, so a line added later is also added to the open ones. A row of the list is judged against the lists of the day it was verified; a line added after that makes the level *partial* until the line is run.

## Where the checks are tracked

The milestone **M7 System verification** holds one `[Test]` issue per system, architecture and level, each with its list as checkboxes. When all its lines are ticked on a stable major release, the row above is updated in a pull request and the issue is closed with it. A failing line keeps the level off the list until it is fixed; the failure is its own issue. To add a system, open the three issues, run them, and add the row; a system that is not built or designed yet starts with a design issue (see [AGENTS.md](../AGENTS.md)); add it to [Platforms](decisions.md#platforms) as well.
