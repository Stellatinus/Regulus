# Verified systems

Which operating systems Regulus has **been run on**, when, on what machine, and how much of it was checked. This is the evidence behind the [supported platforms](../README.md#supported-platforms): a system can be *supported* (the releases are built for it and a defect on it is a bug) without being *verified* (a person ran a release on it and wrote down what happened). Only this page says which is which. Why it is built this way: [Design decisions](decisions.md#verified-systems-a-list-with-a-date-two-levels-and-one-issue-per-system-89).

## The list

One row per **system and architecture**. The architecture is written in two classes, because the arm64 build is not the same work as the amd64 one:

- **amd64** (not ARM): every x86-64 processor, Intel or AMD. The name comes from the architecture, not from the company.
- **arm64** (ARM).

Level: **standard** or **advanced** (see [the levels](#the-levels)); *partial* means some steps of that level were not run, and the row says which. A row appears here only after a person ran a release on that system; the date is the last run, and the verification issue says what was run on every date.

| System | Architecture | Level | Date of the last run | Release | Kernel | Machines | Evidence |
|---|---|---|---|---|---|---|---|
| Ubuntu 26.04 LTS | amd64 (not ARM) | **standard, partial** | 2026-11-24 (first run 2026-10-28) | `v0.3.0-alpha.1` (first run: `v0.1.0-alpha.1`) | 7.0 (as reported in #89 for the first run; the exact `uname -r` was not captured, and neither was the kernel of the second run) | a hub VM on Hyper-V (installed 2026-10-28, upgraded 2026-11-24) and an agent VM (new on 2026-11-24, hypervisor not recorded). CPU, memory and disk were not recorded for either | #81, #89, #443, [Verification status](decisions.md#verification-status) |

**What the 26.04 amd64 row covers, and what it does not.** Run and working: the `.deb` on a fresh install and as an upgrade from `v0.1.0-alpha.1`, the systemd unit, the built-in agent and its metrics, the update check, the dashboard and the console, a remote agent installed on a second machine from the command that **Add a server** prints, enrolled, shown online and offline, and back online after a restart of the hub. **Not run**, so the row says *partial*: a fresh install of the current release, plugins, the update installed from the dashboard on this system and its rollback, `apt remove` and `purge`, PostgreSQL, the HTTPS listener, and the logs and journals of both machines, which were not kept (the results are the tester's reading of the screen). What is missing is the checklist of #536.

Not counted as verification: the `.deb` was also run through real `dpkg` on **Ubuntu 24.04 amd64** inside a container without systemd on 2026-11-24 (see [Verification status](decisions.md#verification-status)), and the package lifecycle runs in CI in `ubuntu:22.04`, `24.04` and `26.04` containers. Those checks are useful and are not a run on a machine with systemd, so they do not put 22.04 or 24.04 on the list.

### Systems still to verify

| System | Architecture | State |
|---|---|---|
| Ubuntu 26.04 LTS | arm64 | supported on paper, never run (#537) |
| Ubuntu 24.04 LTS | amd64, arm64 | supported on paper, never run on a machine with systemd (#538, #539) |
| Ubuntu 22.04 LTS | amd64, arm64 | supported on paper, never run on a machine with systemd; whether it stays is decided by its check (see the decision; #540, #541) |
| Debian | amd64, arm64 | planned, with the same `.deb`; the release is not chosen yet (#543) |
| RHEL-like (`.rpm`) | amd64, arm64 | not supported; its own `[Test]` issue (#401) |
| Windows Server | — | not built (Regulus releases are Linux only); there is no plan to verify it until a build exists |

## The levels

A verification is a person running **a release** (not a build from source) on a clean machine with the system installed, following a checklist, and writing the result down in the issue of that system. A step that was not run is written as *not run*, never left out.

### Standard

The base: the system installs Regulus and the parts work together. **All** of these steps, on one machine and then on two or more:

1. **The machine is described** (see [what is recorded](#what-is-recorded)).
2. **One node.** `install.sh` on a clean machine: the checksum and the signature are verified, the package installs, the unit starts, the dashboard answers, the setup token creates the first admin, the built-in agent shows metrics, the service comes back after a restart and after a reboot of the machine. `apt remove`, then install again, then `apt purge` leave what [Installation](installation.md) says they leave.
3. **Several nodes.** A second machine of the same system joins from **Servers → Add a server**: it installs, enrolls, shows online and feeds a widget. After `systemctl restart regulus` on the hub it reconnects by itself; when it is shut down the hub shows it offline, and online again when it starts.
4. **Updates.** The update check reports a newer release; the update installed from the dashboard succeeds and the hub comes back; a rollback works.
5. **Plugins.** The plugin sources show as verified; a plugin is installed from the store, runs, shows in the dashboard, and is removed.
6. **The logs are clean.** The journal of every machine has no error that the checks above do not explain.

### Advanced

What a group of machines does that one node does not, and the features that depend on the system under them. It is **sketched here and not defined**: the list is written when the hub redundancy epic (#103) and the other features it needs have an implementation to check. What it will cover, as a start:

- a **backup node** (a redundant hub) that keeps up with the active hub, and **switching** between the two, in both directions, with the agents following;
- **PostgreSQL** (`install.sh --db-local`, and a database on another machine) and moving from SQLite;
- the **HTTPS listener**, step-up re-authentication and two-factor authentication on a real browser;
- **backup and restore** of a hub ([Hub backup and restore](backup.md), once implemented);
- features that read the system itself: **disk identity**, container limits, the console on a real terminal, a scrape by Prometheus;
- an **upgrade from the previous release** of an installation that has data.

The advanced check is run after the standard one passed on the same system and architecture, and gets its own checklist in the issue when it is defined.

## What is recorded

Every run records, so that a result can be repeated and a failure traced:

| What | How |
|---|---|
| System | `lsb_release -a` (or `/etc/os-release`) |
| **Kernel** | `uname -r` |
| **Architecture class** | `uname -m`: `x86_64` is **amd64 (not ARM)**, `aarch64` is **arm64 (ARM)** |
| **Each machine used** | its role in the run (hub, agent, backup node), physical or virtual and on which hypervisor or cloud, the number of CPUs and the model, memory, the disk type and size, and whether it is on its own network or behind a NAT |
| Release | the version tag, and whether it was installed fresh or as an upgrade from which version |
| Date | the day of the run, in `YYYY-MM-DD` |
| Who | the tester |
| Evidence | the logs (`journalctl -u regulus`) and screenshots the checklist asks for, attached to the issue. A result without them is written as the tester's reading, as in the 26.04 row |

## Keeping the checklist current

This page is **the reference**: every test of a system, on one machine or on several, follows the [Standard](#standard) steps and [the checklist](#the-checklist-of-one-system) as they are written here, not an older copy. **Every pull request that adds or changes a feature a person can use, or one that depends on the system under it, adds the step to the checklist in the same pull request**, or says why none is needed (the rule is in [AGENTS.md](../AGENTS.md)). The open `[Test]` issues of M7 are copies made when they were opened, so a step added later is also added to them. A row in [the list](#the-list) is judged against the checklist of the day it was verified; a step added after that makes the row *partial* until the step is run, and the page says so.

## The checklist of one system

Every verification issue carries this list, with the steps of [Standard](#standard) in full. A step is ticked only when it was run **on that system and architecture**, and gets a note when it was not run or did not pass; the issue stays open until every standard step is ticked, and the row of this page is updated in the pull request that closes it.

```text
Machines (one line each: role, kind, CPU, memory, disk) …
Kernel (uname -r) …            Architecture (uname -m) …            Release …

Standard
[ ] 1 machines described, kernel and architecture recorded
[ ] 2 one node: install.sh verified and installed; unit starts; dashboard; setup token and first admin
[ ] 2 one node: built-in agent shows metrics; restart; reboot; remove, reinstall, purge
[ ] 3 several nodes: Add a server; the agent installs, enrolls and shows online
[ ] 3 several nodes: it reconnects after the hub restarts; offline and online again
[ ] 4 updates: check; install from the dashboard; rollback
[ ] 5 plugins: sources verified; install, run, show, remove
[ ] 6 logs: no unexplained error on any machine

Advanced: not defined yet
```

## Where the checks are tracked

The milestone **M7 System verification** holds one `[Test]` issue per system and architecture (#536 to #541 for Ubuntu), each with the checklist above. When an issue's standard steps are all ticked, its row is added to [the list](#the-list) (or its level changed) in a pull request, and the issue is closed with it. A system that fails a step stays out of the list until the failure is fixed; the failure is its own issue.

## Adding a system

1. Open a `[Test]` issue in M7 with the checklist; for a system that is not built or designed yet, open a design issue first (see [AGENTS.md](../AGENTS.md)).
2. Run the checklist on real machines and write what was run in the issue.
3. Add the row to [the list](#the-list), with the kernel, the machines and the date, and take the system out of [the table of what is still to verify](#systems-still-to-verify). Say in the pull request which steps were not run.
4. If the system is new to the documents, add it to [Platforms](decisions.md#platforms) too.
