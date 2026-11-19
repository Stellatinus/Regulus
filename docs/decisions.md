# Design decisions

The choices behind Regulus that the code alone does not explain: what was decided, why, and what is still open. The README says *how* things work; this file says *why they are that way*. When a decision changes, update this file in the same pull request.

## Tech stack: Go and React

Hub and agent are written in **Go**, the dashboard in **React with TypeScript**. The alternatives considered were: hub in C# with the agent in Go, everything in C#, Rust, and Node.js.

Go was chosen because:

- the agent must stay light even on a small VPS (512 MB of RAM);
- it builds a **single executable with no runtime** to install, so one package serves both roles;
- the agent opens an **outbound** connection to the hub, and Go's gRPC support handles that well;
- plugins run as **separate processes speaking gRPC**, so they are not tied to Go: a plugin can be written in C# or any other language with gRPC.

Any memory or size figures quoted while choosing were orders of magnitude, not measurements.

## License: Apache-2.0

Apache-2.0 was preferred to AGPL-3.0. A permissive license makes it easier for others to write and distribute plugins (#23); AGPL would protect the project more but would discourage third-party plugins. Changing the license after outside contributions is hard, so this choice is tied to the plugin strategy.

## Platforms

Ubuntu Server comes first because it is the system used for the first real tests. The order is:

1. Ubuntu LTS 22.04, 24.04 and 26.04, amd64 and arm64, `.deb` package (26.04 amd64 is the one tested on a real machine so far, #81; whether 22.04 stays is open in #89);
2. Debian, with the same `.deb`;
3. RHEL-like distributions, `.rpm` package.

Other distributions with systemd can use the `tar.gz` archive. An apt repository or PPA for Ubuntu is postponed.

## Roadmap: phases and milestones

The README describes four **phases**; the work is tracked in six **milestones**, M1 to M6. Issue titles still carry the phase they were first written for (`[Phase 1]`, …), so a title and a milestone can disagree: the milestone is what counts.

| Milestone | Content | Phase |
|---|---|---|
| M1 | Foundations: skeleton, hub core, agent-hub contract, CI, release workflow | 1 |
| M2 | Hub with built-in agent and dashboard: authentication, users, widgets, Ubuntu packaging | 1 |
| M3 | Remote agents: enrollment, multi-server dashboard, agent installer | 2 |
| M4 | Separate hub and agent, protocol versioning | 3 |
| M5 | Update system | 3 |
| M6 | Plugins | 4 |

Some issues were moved after they were written, so their title names an earlier phase:

- **Two-factor authentication (#49) in M3**: it should exist before one dashboard manages several servers.
- **Passkeys (#50) in M4**: WebAuthn needs HTTPS and a public domain (`public_url`), which come with the separate hub.
- **User management (#48) and credential reset (#51) in M2**: without a reset, an administrator who is locked out cannot get back in.
- **Roles (#61) and the terminal console (#52) in M3**; **privileged actions (#60) and server inventory (#62) in M4**; **AI Support (#63) in M6**.

Third-party plugins (#23) have no milestone: they come after the plugin system is stable.

## Releases and channels

Three channels: **stable** (`v0.3.0`), **beta** (`v0.3.0-beta.N`; `-rc.N` counts as beta) and **alpha** (`v0.3.0-alpha.N`; any other pre-release counts as alpha, so an unknown kind never reaches a more cautious channel). Each channel also offers the more stable ones: alpha sees everything, beta sees betas and releases, stable only releases. A hub starts on the channel of the version it runs.

Releases are published from the Actions tab (**Publish release**), not by typing tags, so that versions always follow SemVer order: a version goes through its alphas, then its betas, then the release, and no alpha comes after a beta of the same version. The workflow refuses a commit on which CI has not passed. It must start the release workflow **on the tag**, because the hub only trusts a signature made by `release.yml@refs/tags/<tag>`.

## Updates

**Goal: install once.** After the first installation, an administrator should never have to install anything by hand from a terminal: hub, agents and (later) plugins are updated from the dashboard. Today this holds for hub and agents. Still missing: plugin updates (#19, #20) and two exceptions [updates](updates.md) lists (an agent installed before the update feature, and a release that changes the systemd units or the configuration file, #254).

**One piece at a time.** It must be possible to update an agent, the hub or a plugin on its own. Versions may require others: agents and hub negotiate the protocol and the hub accepts agents of its own release series and the previous one (#17, [remote agents](remote-agents.md), "Versions"); plugins declare the core versions they need with `requires` (#19, [plugins](plugins/README.md)).

**Who decides.** The administrator chooses the mode in the dashboard: `manual` (tell me), `download` (download and verify, then wait for my approval) or `automatic` (install inside a maintenance window). The default for a new installation is **`download`**: nothing is installed without an administrator's approval.

**How the privileged step works.** The hub runs as the unprivileged user `regulus`. Installing a new binary is done by a root helper started by a systemd path unit (`regulus-update.path`, `regulus-agent-update.path`), which the installer enables unless `--no-self-update` is given. The helper acts only on a request naming a version, and checks everything again itself. Releases are signed with **Sigstore keyless** (GitHub Actions identity). An earlier design used a `sudoers.d` rule and minisign or cosign keys and disabled self-update for package installations; that design was replaced by the current one. Whether `sudoers.d` or a root helper is better for the other privileged actions (services, packages, Docker) is still open in #60.

## The repository moved (audit issue #231)

The repository moved from `CodexCircle/Regulus` to `Stellatinus/Regulus`. Releases are signed with the identity of the release workflow of the repository that ran it, and the hub, the agents and the root helper accept only `update.Repository` (`internal/update/signature.go`), so the move needed a change in code, not only in links:

- **One name in Go code:** `update.Repository` feeds the release feed (`hub.ReleaseRepo`) and the signature identity. `packaging/install.sh` (`REPO`) and the documents repeat it; `TestNoStaleRepositoryName` fails when the old address is left in any text file, and `TestInstallerUsesTheReleaseRepository` checks the installer.
- **`v0.2.0-alpha.1` is signed by the new repository** (checked in its Sigstore certificate), but the binary of that release still trusts the old identity. A server running it **cannot update itself** to any later release: reinstall with `install.sh` from the new release (see [Updates](updates.md)). Releases up to `v0.1.0-alpha.1` were signed by the old repository.
- **Why not trust both names:** a name that can be registered again by someone else would let a stranger sign releases that every installed hub accepts. Only the current repository is trusted.
- **The Go module path stays `github.com/CodexCircle/Regulus`** on purpose: it is an import path, nobody downloads the module from it, and renaming it touches every import and the generated code (regenerating needs `make proto`). It can be renamed later in one mechanical pull request; until then it is the only place where the old name remains, together with the product name "CodexCircle Server Manager", which the maintainer will decide on separately (see the naming plan in the [glossary](glossary.md)).

## Updates: the rollback restores the SQLite database (audit issue #205)

A rollback that put back only the program failed in the one case it exists for: the new version migrated the database and then did not come up, and the previous version refuses a database with a migration it does not know (`storage/migrate.go`). For SQLite the root helper now takes a consistent copy before the swap (`storage.SnapshotSQLite`), refuses the update if it cannot, and puts the copy back on a rollback only when the schema moved (`storage.RestoreSQLite`, after stopping the service and removing the write-ahead log, which belongs to the file being replaced). It accepts losing what the new version saved after the copy, and says so in the result. For PostgreSQL it does nothing and the documents tell the administrator to back up first: a server cannot be copied from the helper, and the real answer there is a rule that a release's migrations are ones the previous release can live with, which is not written yet (the strict "unknown migration" check stays).

## Enrollment tokens: list, cancel, and one transaction (audit issue #202)

A token could not be listed or cancelled, and `Enroll` deleted it before signing the certificate and inserting the agent, so a malformed request or a database failure burned it. Now `Enroll` reads the token, signs, and then deletes the token and inserts the agent in one transaction; the delete must affect exactly one row, so two requests with the same token cannot both enroll (the loser signed a certificate that nobody receives). A token is named in the API by the first 16 hex digits of its hash: no migration (a rollback of the hub after a migration is the hard case, see the update decisions), and the prefix cannot be turned back into the token. The lifetime is an optional `ttl` in the creation request (1 minute to 7 days, default one hour), not a hub setting, because it depends on how the admin will hand the token out. Not done: a lifetime field in the dashboard form (the API has it).
## Browser security headers (audit issue #201)

Every response of the hub's HTTP port carries a content security policy that allows only the hub's own origin (plus `data:` images, for the QR code of the two-factor setup), and forbids framing, plugins and a changed `<base>`; also `X-Frame-Options: DENY`, `nosniff` and `Referrer-Policy: no-referrer`. HSTS (one year, no `includeSubDomains`, no `preload`: those can lock a whole domain out and are the operator's call) is sent only when the request came over TLS or the proxy says so in `X-Forwarded-Proto`; a browser ignores HSTS over plain HTTP, so believing that header from anyone is harmless there. Not done: the `__Host-` cookie prefix. It needs `Secure`, so the cookie name would depend on whether the request came over HTTPS (the hub also serves plain HTTP) and every reader would have to try both; `HttpOnly`, `SameSite=Lax` and `Secure` behind HTTPS already cover what it adds for a cookie that is set only by the hub. The CSP was checked in Chromium with the built dashboard (login, live stream, the two-factor QR code): no violation. It was not tried in Firefox or Safari, where `connect-src 'self'` for the WebSocket should behave the same (Safari before 15.4 did not match `ws:` with `'self'`).

## Resetting one's own credentials is refused (audit issue #203)

`CreateReset` and `ResetTwoFactor` refuse the caller's own account (`403`). Both used `mayManage`, which lets a user manage themselves, so a stolen admin session could make its own reset link or strip its own second factor without the password the normal flows ask for. We chose to refuse rather than to ask for password and code in these paths, because the normal flows already exist (`ChangePassword`, `DisableTwoFactor`), the dashboard already disables these buttons for oneself, and an admin who lost everything has `regulus hub reset-admin` on the machine (it does not go through these two methods). A stolen session can still create another admin; step-up re-authentication for users, roles, enrollment and updates is not done and is tracked in its own issue.

## The client address behind a reverse proxy: `trusted_proxies` (audit issue #196)

`X-Forwarded-For` and `X-Forwarded-Proto` are believed only when the peer of the connection is listed in `trusted_proxies`; the client is the rightmost entry that is not a trusted proxy. The default is an empty list, so a hub with nothing in front of it ignores both headers, which also closes the old hole where any client could mark its own cookie `Secure` or not. The setting is a list, so it is read from the YAML file and `REGULUS_TRUSTED_PROXIES` but is not in the console settings table (`regulus config set` edits scalars). The `Forwarded` header (RFC 7239) is not read. The agent port is not affected: it has no proxy in front (mutual TLS).

## The audit log: time retention and a separate budget for failures anyone can cause (audit issue #200)

The audit log was capped at 10,000 rows with a second `DELETE` after every insert, and a failed login or an invalid enrollment token (the agent port is reachable by anyone) counted like a user change, so a few thousand requests from many addresses (the limiters are per address, an IPv6 /64 each) pushed out the history an administrator needs. Now an insert only inserts. `auth.Service.Run`, started by `Hub.Serve`, prunes when the hub starts and every hour: events older than 90 days go, and then the oldest ones beyond a budget per class: the newest 5,000 for `login_failed` and `agent_enroll_failed`, 100,000 for everything else (a backstop only; those events need a login or the machine). We chose separate budgets in the same table over the issue's other option, one row with a count per repeated failure, because it needs no migration, keeps one row per attempt with its address, and gives the same guarantee. The cost: between two prunes a burst is bounded only by the limiters, and the rows of one flood are not merged. The list of unauthenticated events is `auth.unauthenticatedEvents`; a new event that a stranger can cause must be added there (the fleet's `agent_enroll_failed` is spelled in auth and kept equal by a test in `internal/hub`). The pruning queries were run on SQLite and on PostgreSQL 16.

## Agents that are too old: an update-only session (audit issue #212)

Before, an agent more than one release series behind (or speaking a protocol the hub dropped) was refused, and updates travel over that same connection: a server that was off for a few months could only be brought back with a terminal and a new enrollment token. Options weighed, and why:

- **Force every agent to update before the hub** (the hub refuses to update while one is behind): one switched-off machine would freeze the updates of the whole network. Rejected.
- **Widen the window** (accept 3 series instead of 2): only moves the cliff. Rejected.
- **Let the agent fetch the newest release from the internet by itself**: agents need no internet today (they get the release from the hub over mTLS), and many are not allowed to reach it. Rejected.
- **Chosen: an update-only session on a frozen update contract.** The hub admits an *enrolled* agent that is too old with `Welcome.update_only`; in that session only `Hello`, `Welcome`, `Heartbeat`, `Ping`, `ApplyUpdate`, `CommandResult` and `FetchUpdate` are used, and their meaning never changes in any protocol version (`protocol.UpdateContract` stays 1, tested). The update goes straight to the hub's version. An old agent that does not know the field keeps sending metrics; the hub drops them, because their format may have changed. The hub refuses every other command for that agent (`ErrUpdateOnly`).
- **Still refused:** an agent with no certificate (nothing enrolled, nothing to update), and an agent newer than the hub (the hub is the one to update).
- **Not solved by this:** an agent that cannot verify today's signatures (a build that trusts the old repository identity) or that has no update helper: `install.sh` once. Both are said in the documents and the dashboard.
- **Not verified on a real machine:** a real agent of an old release against a new hub (#233 would add it as an end-to-end test).

## Versioning: contracts instead of modules (#227)

The maintainer asked whether Regulus should be split into more modules (for example a separate, long-lived communication module) to make downloads smaller and parts last longer. Decision: **one executable, and a list of contracts, each versioned and checked on its own** ([versioning.md](versioning.md)). A separate module would only last longer if its interface never changed, and that is a property of the contract, not of the file; with one file there is one signature and one install path, and the compatibility matrix is the short list of contracts instead of every pair of modules. Plugins are the exception, since they are released separately ([plugins](plugins.md)).

CI now runs `buf breaking` against `main` (`make proto-breaking`), and golden files of protocol 1 messages are decoded by today's code (`internal/protocol/testdata/v1`). The golden files were written by the code of this pull request, while the protocol is still at version 1 and no field has changed number or type since the first release; they freeze that state from now on, they are not bytes captured from an old binary. The agent now checks that the protocol version the hub picks is one it speaks.

## Updates: an interrupted update is settled when the machine starts (audit issue #246)

A power cut between the swap and the confirmation left a new version that nobody had watched; if it did not work, systemd restarted it for ever and the previous version came back only at the next update request. Choices:

- **Two steps, because of systemd ordering.** A unit ordered before `regulus.service` cannot restart or start it (the job would wait for the unit that asks), and a service that has not started cannot be judged. So `regulus-update-recover.service` (before) does only what needs no running service, and `regulus-update-confirm.service` (after) runs the existing `update-apply --recover`, unchanged: the health check after a power cut is the same code path as after a killed helper, not a second implementation.
- **A shell script in front** (`/usr/lib/regulus/update-recover`), because the recovery must not depend on the file it may have to repair. It does one thing without the executable: put the kept copy back when `/usr/bin/regulus` does not answer `version` (an empty file counts as broken, although a shell "runs" it). Everything else stays in Go, where it is tested.
- **Why not always roll back at boot** (simpler, no second step): it would throw away an update that is fine, and with it what the new version saved, each time the power fails during the 90 seconds of the health check.
- **A new version gets two starts of the machine, within an hour.** Without a limit, a version that takes the machine down would do so for ever. Without the hour, a marker left for days by a confirm step that never ran (masked, or broken on a system we did not test) would make the third reboot restore a database that is weeks old. Counting only close starts keeps the first protection without the second risk. A clock that went backwards counts as close.
- **A lock** in the state directory serialises the helpers, now that one can be started at boot while a request arrives.
- **Both new units run as root without the sandbox** of `regulus-update.service`, like its `ExecStopPost`: they exist to repair, and a sandbox mistake that cannot be seen without a real systemd (#81) would disable the repair exactly when it is needed.
- **`pending.json` is a contract between versions:** written by the version that was installed, possibly read by the one that replaced it. Fields are only added and a reader ignores what it does not know (the rule is written in `internal/update/pending.go`, with a test).
- **Not verified on a real machine:** unit ordering, the condition, and a real hard power-off (#240).

## Updates: reading the release list (audit issue #213)

One page of 30 releases could be filled by plugin releases (planned in the same repository, `plugin/<id>/vX`) or by many pre-releases, and a stable hub would then report "up to date" for ever. The list is now read 100 at a time, until a page holds a stable release of the core (the newest stable is then known, and so is every pre-release published after it), the list ends, or 5 pages were read. Only the first page is asked conditionally: if it did not change, nothing did. `/releases/latest` was not used because the beta and alpha channels need the pre-releases too, and one paginated path is simpler than two.

Withheld or stale answers (a "freeze") cannot be told from "no new release" without signed metadata that expires. That is **not done**: the hub now records when a check last **succeeded** and the Updates page warns after three days without one, which covers the common case (a hub that lost its internet access). A signed `latest.json` with an expiry is left for a later issue.

## Remote agents from the dashboard

Remote agents can be turned on from the dashboard (#90), so that enrolling a second server needs no terminal on the hub. The choice is kept in the database, because the hub runs as `regulus` and cannot write `/etc/regulus/config.yaml`. `agent_listen` in the configuration always wins: when it is set the dashboard shows it and cannot change it. Turning the listener on needs `servers.manage`, the same permission as making enrollment tokens, since one is useless without the other; it is audited. Turning it off from the dashboard is not offered yet.

## Verification status

No release has been published yet, and several parts have only been tested by unit and integration tests: the signed release, the `.deb` on a clean Ubuntu system, the agent installer on a second server, and installing and rolling back an update with real systemd. They will be checked together on a VM (#81). Until then, try the first release on a test server.

## Language: English only, GUI in English and Italian

Decided by the maintainer on 2026-10-29 (#120). Code, comments, commits, issues, pull requests and documents are in English only. The GUI (dashboard and console) supports English and Italian in every version, alpha included. The Italian sections that used to follow the English text in the README, `CONTRIBUTING.md`, `SECURITY.md`, this file and [plugins](plugins/README.md) were removed: one source of truth, no double maintenance, no drift. The Italian text stays in git history. Issues opened before this decision stay bilingual. The GUI texts come from catalogs: the dashboard's `dashboard/src/i18n/messages/` and the console's `internal/console/strings.go` and `strings_it.go` (#125, #191; see CONTRIBUTING.md). The Italian is a draft that the maintainer has not reviewed yet.

## Database: SQLite by default, PostgreSQL by configuration

Done in #122. Choices, so they are not rediscovered:

- **Driver:** `github.com/jackc/pgx/v5` through its `database/sql` adapter. Pure Go (the binary stays static, no cgo), MIT licence (compatible with Apache-2.0). It was already in the module graph as an indirect dependency.
- **One place knows the dialect.** Option (a) of the issue: **separate migration directories per backend** (`migrations/sqlite`, `migrations/postgres`, same numbers and names) with a test that compares the resulting schema, plus a small **`Rebind`** in the PostgreSQL connection wrapper that turns `?` into `$n`, so none of the 80 call sites changed. Rejected: one portable SQL set plus a rewriter (SQLite `STRICT`, `INTEGER PRIMARY KEY`, `COLLATE NOCASE` and the table rebuild of migration 9 cannot be written once).
- **Portable SQL in the code:** `INSERT OR REPLACE` became `INSERT ... ON CONFLICT (user_id) DO UPDATE`; `LastInsertId` (not supported by pgx) became `INSERT ... RETURNING id`; unique violations are recognised by `storage.IsUniqueViolation` on both; parameters in the select list of an `INSERT ... SELECT` are cast (`CAST(? AS TEXT)`) because PostgreSQL cannot infer their type.
- **Time and value representation:** unchanged. Timestamps stay ISO-8601 text and flags stay integers in PostgreSQL too, so every code path is identical on both backends. Row ids are `BIGINT` identity columns. Cost: no `timestamptz` arithmetic in SQL, and a later move to native types is a migration of its own.
- **Case-insensitive names** (`COLLATE NOCASE` in SQLite) use the **`citext`** extension in PostgreSQL, created in `public` with `public` kept on the search path. Alternatives rejected: nondeterministic ICU collations (not usable in foreign keys before PostgreSQL 18), and `lower()` indexes (would need every query and the `roles` foreign key changed).
- **Migrations on PostgreSQL:** transactional DDL; the same "refuse a newer schema" rule; a session **advisory lock** around the whole run. The `-- regulus:no-foreign-keys` mechanism is **not** needed there (the constraint is dropped in place), so it is ignored on PostgreSQL.
- **Connections:** SQLite keeps one connection. PostgreSQL uses a pool (10 open, 4 idle, 30 min lifetime, 5 min idle) and waits up to 60 s for the server at start. A running hub does not exit when the database goes away; it logs the change of state once and reports it in `regulus status` and the console and in the dashboard (#140): a banner fed by `GET /api/database`, a public endpoint with one boolean, because sessions live in the database and an endpoint that needs a login cannot report that the database is down. `/healthz` is deliberately unchanged (the installer, the smoke test and the update helper wait for `ok`). The banner texts are in English and Italian in one file until the translation mechanism of #125 exists.
- **Password:** environment only (`REGULUS_DATABASE_PASSWORD`, normally `/etc/regulus/regulus.env`, `0640 root:regulus`). It is deliberately **not** a YAML key, so `config.yaml` (which `regulus config set` rewrites and which may be copied around) can never hold it and the strict YAML parser rejects it. Shown as `********` when set.
- **Open questions of the issue, answered:** minimum PostgreSQL **14** (Ubuntu 22.04; tests run on 14 and 16); default `sslmode` is **`prefer`** (works for a local server; `verify-full` plus `database.sslrootcert` for a remote one); schema is **`public` by default**, configurable with `database.schema` (which must exist).
- **Check command:** `regulus db check` (name confirmed here), see [Database](database.md).
- **CI:** a PostgreSQL service job runs the whole suite with `REGULUS_TEST_DATABASE_URL`; each test gets its own schema (`storagetest`).
- **CI coverage (#141, partly):** the PostgreSQL job is a matrix over **14 and 16** (check names carry the version, for example `Go tests on PostgreSQL (14)`; a branch protection rule that requires the old name `Go tests on PostgreSQL` must be updated). A second job, `Go tests on PostgreSQL with TLS (verify-full)`, generates a CA and a certificate for `localhost` with openssl, starts `postgres:16` with `ssl=on` (key `0600`, uid 999) and runs `TestPostgresVerifyFull*` through `storage.Check` and `storage.Open`: the right CA connects over TLS; a wrong host name (127.0.0.1), a root that is not the issuer and a missing root file are refused; the password is not in any message. **Still not verified:** an install on a real VM, `apt` upgrading an existing PostgreSQL, a database on another host (real network, real certificate chain), `verify-ca`, and PostgreSQL versions other than 14 and 16. #141 stays open for those.

## Installer: the database choice (#123)

- **Scope.** By default the installer **connects to an existing PostgreSQL** that the administrator prepared (`docs/database.md` has the SQL). Installing it locally is the opt-in `--db-local` (#139, below).
- **Flags, not a URL.** A single set of options (`--database`, `--db-host/-port/-name/-user/-sslmode/-sslrootcert`) rather than one connection URL read from a file: each option is validated on its own with a specific error, and the values map one to one to the `REGULUS_DATABASE_*` settings. The password has no option: `--database-password-file`, `REGULUS_DATABASE_PASSWORD`, or a prompt without echo.
- **Where the password lives.** Only `/etc/regulus/regulus.env` (written `0640`, then `root:regulus` by the package's post-install script), in double quotes with `\` and `"` escaped as systemd and `regulus db check` read them. Never `config.yaml`, which the hub refuses to read a password from anyway.
- **The check runs before the install.** The issue asked for the check after the install; the installer does it *also* before, with the binary extracted from the verified `.deb` (`dpkg-deb -x`) and the settings passed in the environment of that one process, so a wrong password or a missing database stops the run while nothing has changed. The check after the install, with the installed binary, reports and fails the installer with instructions but does not roll back the package.
- **Post-install script when the database is unreachable.** It prints the result of `regulus db check` and the way to fix it and **never fails the package**. The service is still enabled and started: the hub waits 60 s for the database, exits, and systemd restarts it every few seconds after that (`Restart=on-failure`), so it comes up by itself when the database does.
- **Upgrades.** An installation is recognised by the installed package, the SQLite file or database settings in the environment file. Without `--database` its backend is kept and nothing is asked or written; naming the other backend stops with a pointer to #124 before anything is downloaded.
- **Not verified:** the installer has not run on a real VM with PostgreSQL (#81), and not with `apt` upgrading an existing PostgreSQL install. The script is tested with stubs for `dpkg-deb` and the binary, and by hand against a PostgreSQL 16 with SCRAM authentication using the real binary (wrong password stops, right password writes the file; a password with quotes, a backslash and `$` round-trips).

## Regulus Access: direction (not final; epic #152)

Agreed in conversation on 2026-11-13, **proposed**, to be confirmed in the design issues of #152. Nothing is implemented. Recorded here so the reasoning is not lost:

- **The phone connects to the hub, not to the nodes.** It is always the client, so mobile networks and NAT do not matter, and no node opens a new port. Identity is a device certificate from the group's CA with the key in the Android Keystore; mTLS over TLS 1.3 already encrypts, so no extra encryption layer. mTLS does not depend on the phone's address.
- **Rejected for the first version:** a public Access listener on every node (multiplies the attack surface), and a mandatory overlay network (Tailscale, WireGuard): not needed for security. The overlay stays an optional way not to expose anything.
- **Away from home: the access gateway** (#161), a public node that forwards the phone's connection to the active hub. mTLS stays end to end phone to hub; the gateway holds no secrets. It follows a failover to the redundant hub. Its extra capabilities: notifications (second step) and authentication for other programs (later, **separate design**, because it would make a public machine hold keys).
- **At home: the local address first** (#162), as an ordered address list with the CA fingerprint checked on every path; no discovery in the first version.
- **Node types** are one role plus capabilities, and every node keeps the same connection to the hub inside; see [node-types.md](node-types.md) (#160). Direct node-to-node links are left for later and, when they exist, are authorised by the hub.
- **Passkeys (#50)** stay for the web login; Access uses the device key.
- **Free-tier cloud servers:** a plugin to measure use and optionally keep a server active (#163) is only built after the provider's current terms are read.

## Installer: a local PostgreSQL (#139)

- **Opt-in, `--db-local`** (implies `--database postgres`), plus **`--db-reuse`** for an existing role and database. The default stays "connect to what you prepared".
- **Creates only its own role and database** over the local socket as the `postgres` system user, on `127.0.0.1`; it never alters an existing role or database. It **refuses** when either exists, because it could not know their password and must not reset it; `--db-reuse` is the explicit "I know, use them" and then needs the password given, never generated.
- **The password** is 32 letters and digits from `/dev/urandom` (nothing to quote in SQL or in the environment file), sent to `psql` on **standard input**, never an argument, never printed, stored only in `/etc/regulus/regulus.env`.
- **Order, so a second run works.** After the verified download the installer creates the role and database, then **writes the settings**, then runs the check with the binary from the package. If the check fails the password is already saved, and a re-run finds `REGULUS_DATABASE_TYPE` in the environment file and keeps it (nothing is created twice) instead of finding a role it has no password for. A failure leaves PostgreSQL, the role and the database in place; the message says so.
- **Re-run on an existing installation:** `--db-local` is ignored with a note; on a SQLite installation it is refused like `--database postgres` (#124).
- **Tested** by `scripts/test-install.sh` with stubs for `apt`, the `postgres` user and `psql` (including that the password is on no command line), and by hand: the real functions against a throwaway PostgreSQL 16 cluster (role and database created and owned by the role, `citext` enabled, login over TCP with the generated password, a second run refused). **Not verified:** `apt-get install postgresql` on a real Ubuntu VM, PostgreSQL 14 from 22.04, a cluster that is stopped or on another port, and the whole installer end to end with a release (#81, #141).

## GUI translations: dashboard mechanism (#125, proposal in progress)

- **No library:** two typed catalogs (`dashboard/src/i18n/en.ts` is the source, `it.ts` is `Record<keyof en, string>`), so a missing Italian text is a compile error, plus a test for extra keys, empty texts and placeholder mismatches. Keys are stable dotted names, not the English text. No plural machinery (two keys where a count matters); dates and numbers through `Intl`.
- **Language:** the saved choice (`localStorage`, `regulus.language`), else the browser's (`it*` is Italian), else English; a switch in the header; `<html lang>` follows. Components work without the provider (the detected language), which keeps small tests simple.
- **Console:** one `strs` table per language in `internal/console/strings.go`, chosen once at start by `UseLocale` from the first set of `LC_ALL`, `LC_MESSAGES` and `LANG` (C-library order); machine formats (`--plain`, `--json`) are never translated. Texts shown after an action became table fields (templates with `%s`).
- **Console, the rest of the texts (#191):** health-check names and details, durations (`3h 12m`, `2g 2h 5m` in Italian), "… ago", the systemd and agent state words, the setting origins and descriptions, and the small literals (`(empty)`, `Problem`, the tab label, the reasons an action is unavailable) come from the table. A health check carries both languages: `Name` and `Detail` stay English for `--json` and `--plain` (whose key is a fixed `check.<key>`, no longer derived from the name), `Title` and `Text` follow the locale; `uptime` and the agents' heartbeat in `--plain`/`--json` stay English the same way. English setting descriptions are not copied into the table: `internal/config` stays their single English source (it also feeds `config list --json`), and the table has only the Italian ones, checked against `config.Keys()` by a test. The help line is built from parts (`HelpMain`, `HelpReset`, `HelpQuit`) instead of cutting an English substring out of it. The parity tests now compare the fmt verbs in order (`%d`, `%s`), and the maps key by key.
- **`cmd/regulus` texts are CLI, English only (#191).** What the command prints itself, the usage and flag help, the `regulus: …` errors, the notes on standard error after `config set`, and the reason the console did not open, is the command-line interface, not the GUI, so the language rule keeps it in English: one text to search for in issues and logs, and the same as the errors that come from Go and `internal/config`, which have no translation. The reports meant for people that the commands print on standard output, the text of `regulus status` and of `regulus config list`, come from `internal/console` and follow the locale.
- **Migration** is one pull request per page group; the first slice moved the header, the navigation and the database banner. The console gets an Italian table and a test in a later pull request. Still open: who reviews the Italian wording (a first draft was written by the assistant), and a per-user language setting stored on the hub.

## Plugins: manifest and runtime (#19)

Choices made while writing the runtime, so they are not rediscovered:

- **Processes and gRPC on a Unix socket**, as #19 and the tech stack decided. Not the Go `plugin` package (same address space, a crash is the host's crash, exact build match needed) and not WebAssembly (no access to the system, which plugins such as Docker need).
- **The plugin serves, the host connects.** The host names the socket path (`REGULUS_PLUGIN_SOCKET`) in a directory of its own, mode 0700, so no other user can reach the socket, and a plugin can be written in any language with a gRPC server. The host checks the plugin's identity (`Describe`) against the manifest before anything else.
- **The contract is two calls** (`Describe`, `Health`). What a plugin can do is a separate design step (open question 1 in [plugins](plugins/README.md)), not invented here.
- **Restart rules** like systemd's: exponential pause from 1 s to 1 min, reset after a minute of running, and **failed for good** after more than 5 stops in 10 minutes, until the agent restarts. A broken plugin then costs nothing more.
- **Process group per plugin**, `SIGTERM` then `SIGKILL` after 10 s, and the group is killed also when the plugin exits by itself: a plugin cannot leave processes behind.
- **Clean environment**: a plugin gets only the variables it needs, never the agent's (which may hold settings or, on the hub, the database password).
- **The manifest is strict** (unknown keys refused), like the configuration file, and `<X.Y.Z` in `requires` also leaves out the pre-releases of X.Y.Z, unlike plain SemVer.
- **Directory = id, links refused** for plugin directories, and a command that leads outside its plugin directory after following links is refused.
- **Agent first.** The supervisor is wired into `regulus agent` (`plugins_dir`, off by default). The hub's wiring needs its settings and status pages and is left for a follow-up.
- **Not done, on purpose:** signatures, installation, enforcement of permissions, a user per plugin. Until #20, the plugins directory must be writable by root only: who can write it decides what runs.

## Managers, backends and apps (epic #286, proposed)

Agreed in conversation on 2026-11-19. **Proposed**: the names and the direction are decided, the design of each part is in the issues of #286. Nothing is implemented. The full page is [managers-and-apps.md](managers-and-apps.md).

- **Four words, four meanings.** *Plugin* (code that extends Regulus, unchanged), *manager* (a function of a server Regulus manages), *backend* (the real tool behind a manager), *app* (third-party software in containers). **Rejected words:** *extension* and *add-on* (a second name for plugin), *snippet* (no feature needs it).
- **Manager, not capability.** *Capability* is already in the glossary for what a node type does (#104, #160, #169). One word with two meanings would make "does this node have the firewall capability?" ambiguous.
- **Use existing tools.** Regulus does not write its own RAID, firewall, container runtime or VPN. One **active backend per manager per server**. The firewall is the example: nftables is the kernel layer and ufw and firewalld sit on top of it, so Regulus detects the one the distribution uses instead of offering four equal choices; iptables is not supported. LVM is not an alternative to mdadm (arrays and logical volumes are different layers), so storage and volumes is a separate manager (#293) from RAID (#285).
- **Apps run in containers**, with their data in a declared volume. **Rejected:** installing the software natively in a dedicated volume as a service. It would need an installation, update and cleanup method per program that Regulus would have to know. The container runtime is a backend (Docker or Podman, chosen by the administrator), not a hard-coded choice; the common format is the Compose specification. Kubernetes is out of scope for now. This answers the first item of #113 in direction; the details are #289.
- **Apps are not plugins.** They need no gRPC and no Regulus API. An app that needs something from the host (an open port, a VPN) asks the matching manager and never changes the host itself.
- **Docker was described as a plugin** (the "plugins such as Docker" remark under *Plugins: manifest and runtime* above). With this vocabulary Docker is a **backend** of the container manager; whether the code that drives it is built in or a plugin is open (#287).
- **Milestones.** The model (#287), the apps (#288), the container manager (#289) and the three managers the maintainer named (firewall #290, VPN #291, wake #292) are in **M6**: they define how plugins are used and need #261 first. The other managers (#293 to #300) and the first apps (#301) have no milestone yet.
- **Not decided:** built in or plugin for the part that changes state; how privileged actions work (#60); which guards each dangerous operation needs; observe or manage per manager (RAID proposes *observe first*, #285); whether a node type enables managers by default.

## Open questions

- Should the other privileged actions use a `sudoers.d` rule or a root helper like the updates? (#60)
- Plugin features, dependencies, signatures and isolation: see the open questions in [plugins](plugins/README.md).
