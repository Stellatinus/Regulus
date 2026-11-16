# Versioning: one program, several contracts

Regulus ships **one executable** (`regulus`). The hub, the agent, the console and the root update helper are subcommands of it (see [Architecture](architecture.md)). The question "how many modules do we have, and how long does each one live?" is therefore not about files to download. It is about **contracts**: the places where two pieces that may run **different versions** talk to each other. Each contract has its own version, its own rule for change and its own check in CI.

The release version (`v0.3.0`) says *what is installed*. A contract version says *who can still talk to whom*. They move independently: most releases change no contract at all.

## Why not separate modules

Splitting `regulus` into separately downloaded modules (hub, agent, a communication module, the dashboard) was considered (#227) and **rejected for now**:

- **The size saved is small.** The executable is one static Go binary; the dashboard is embedded in it. An agent downloads one archive of a few tens of MB once per update.
- **The compatibility matrix grows.** With N modules that update on their own, every pair is a contract to test. With one program, the only mixes that exist are the ones listed below, and each is tested.
- **A module still has to be updated.** A separate "communication module" that lives longer than the rest only moves the problem: the day it must change, it has the same problem the agent has today. What makes a part long-lived is a **contract that does not change**, not a separate file.
- **One signature, one helper.** Every installed file must be verified by the root helper. One archive means one verification path (Sigstore, checksums), which is where the security work is (`docs/updates.md`).

What *is* split is the **plugins**: they are separate programs with their own versions, because they really are developed and released apart from the core ([Plugins](plugins.md)).

This decision can be revisited if a real need appears (for example an agent for a device too small for the full binary); see [Design decisions](decisions.md#versioning-contracts-instead-of-modules-227).

## The contracts

| Contract | Between | Versioned by | Where | Rule |
|---|---|---|---|---|
| **Update contract** | hub ↔ agent of *any* release | never changes meaning; `protocol.UpdateContract` = 1 | `proto/regulus/agent/v1`: `Hello`, `Welcome`, `Reject`, `Heartbeat`, `Command` with `Ping` and `ApplyUpdate`, `CommandResult`, `FetchUpdate` | frozen: fields may be added, nothing removed or changed, in any protocol version |
| **Agent protocol** | hub ↔ agent | `protocol.Version`, `protocol.MinSupported` (negotiated in `Hello`/`Welcome`) | `proto/regulus/agent/v1`, `internal/protocol` | add only; a change an old peer cannot follow raises `Version` |
| **Release series window** | hub ↔ agent | the release version (`update.CheckCompat`) | `internal/update/compat.go` | full service for the same and the previous minor; older agents get the update contract only |
| **Helper files** | hub or agent (unprivileged) ↔ root helper, and helper ↔ the next version of the helper | JSON shape | `request.json`, `result.json`, `connected.json` (`internal/update/apply.go`), `pending.json` (`pending.go`) | add only; a reader ignores unknown fields |
| **Database schema** | the hub ↔ its own data, across versions | migration number | `internal/storage/migrations` | forward only; a hub refuses a schema newer than it knows (a rollback restores the copy, #205, #247) |
| **Release layout** | the release workflow ↔ installed hubs, agents and the helper | asset names | `checksums.txt`, `checksums.txt.sigstore.json`, `regulus_<version>_<os>_<arch>.tar.gz`, tag `v<version>` | never renamed; new assets may be added |
| **HTTP API** | dashboard, scripts ↔ hub | the path (`/api/...`) and `openapi.yaml` | `internal/hub/openapi.yaml` | the dashboard is embedded, so it always matches; scripts get additive changes only |
| **Plugin API** | a plugin ↔ the host | `plugin.api` in the manifest | [Plugins](plugins.md) | its own range, independent of the release version |

### The update contract: why it is frozen

An agent can be switched off for a long time. When it comes back, the hub may have moved on by several release series and even dropped the protocol version that agent speaks. Before #212 such an agent was refused, and the only way back was a terminal on that server.

Now the hub always keeps one way in: an enrolled agent that is too old is admitted in an **update-only session** (`Welcome.update_only`). In it, the hub speaks only the update contract, which has the same meaning in every protocol version that ever existed. It sends `ApplyUpdate` with its own version, and the agent fetches that release over the same connection. The update goes **straight to the hub's version**, whatever releases were skipped.

The rules that keep this working:

- `protocol.UpdateContract` stays at 1 (a test fails otherwise). When `MinSupported` is raised, the agents below it still get the update-only session through `protocol.NegotiateUpdateOnly`.
- The messages of the update contract are only ever extended. A new field must be optional for both sides. CI's `buf breaking` check (below) refuses a renumbered, retyped or removed field.
- An update-only session carries nothing else: the hub drops the agent's metrics and refuses every command but `Ping` and `ApplyUpdate` (`ErrUpdateOnly`).
- What it cannot fix: an agent that cannot verify today's signatures (built before the repository moved, see [Updates](updates.md)), or one installed without the update helper. Those need `install.sh` once.

### The agent protocol

The agent sends the range it speaks (`min_protocol_version`, `protocol_version`) and the hub picks the highest version both speak (`protocol.Negotiate`). The agent checks that the version the hub picked is one it speaks, and drops the connection otherwise.

- **Adding** a field, a message, a command kind or an RPC is compatible and needs no new version. A peer that does not know it ignores it (an unknown command is answered "unsupported command").
- **Raising `Version`** is for a change of meaning that an older peer would misread. Keep the old behaviour for agents that negotiate the old version until `MinSupported` is raised.
- **Raising `MinSupported`** drops full service for old agents. They still reach the update contract.

### Helper files

The root helper of version N writes `pending.json`. After a power cut, the helper that reads it may be the one of version N+1 (the new executable is in place), or the reverse after a rollback. These files are therefore contracts between versions too: fields are only added, and a reader ignores what it does not know (`TestPendingMarkerIsReadAcrossVersions`).

## What CI checks

- **`buf breaking`** against the main branch on every pull request (`make proto-breaking`, the `proto` job). A renumbered, retyped or removed field fails the build (#227).
- **Golden messages** (`internal/protocol/testdata/v1/*.binpb`): bytes encoded by protocol 1 of `Hello`, `Welcome`, `MetricsReport`, `Command` with `ApplyUpdate`, `CommandResult` and `FetchUpdateRequest`. They are decoded by today's code and the fields compared. A file is written once and never regenerated (`go test ./internal/protocol -run Golden -update-golden` only creates missing files). A change that would misread the bytes of an old agent fails the test.
- **The update contract stays at 1** and the update-only negotiation is tested (`internal/protocol`). The hub admits a too-old agent only for the update and takes it to its own version (`internal/hub`).
- **An unknown command** sent to an agent is answered "unsupported command", not a crash (`internal/protocol/contract_test.go`).

Not checked yet: a real old binary against a new hub, which is what a release-to-release end-to-end test would add (#233).

## When you change something

1. Changing a `.proto` file: add fields, never reuse a number. Run `make proto` and `make proto-breaking`.
2. Changing the meaning of something an old peer would misread: raise `protocol.Version`, keep the old path for the old version, and add a golden file for the new message.
3. Never change the meaning of the update contract. If it is truly needed, write a design issue first.
4. Changing a helper file: add fields only.
5. Write the change in `docs/decisions.md`.
