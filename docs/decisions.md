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

## Dashboard live data: a store with keys per agent (audit issue #224)

The live data of the widgets used to be one React context `{state, connection, now}` rebuilt at every stream message, and each report copied the whole series (up to 3600 points) of its agent; every widget of every agent was drawn again. Now `LiveProvider` fills a `LiveStore` (`dashboard/src/live.ts`) kept outside React. Components read it with `useSyncExternalStore` and subscribe to keys: the list of agents (which changes only when agents come, go or are renamed), one agent, and the status (connection, readiness, the clock). A report of agent A therefore wakes only the widgets that show A. Each agent's reports are in a ring buffer (`PointRing`): appending is constant time, and the 10-minute window a chart draws is cut with a binary search and cached until the next report, so the work per message does not grow with the length of the series; widgets memoise what they derive from that window (`derive` in `BaseWidgets.tsx`). The history is loaded only for agents a widget shows (`useLiveAgent` calls `store.watch`), and only the newest 600 reports (`?limit=`, added to `GET /api/agents/{id}/metrics` for this): enough for 10 minutes at the shortest metrics interval. An agent nobody shows still gets its reports from the stream; after a reconnection its series has a gap, so it is loaded in full when a widget shows it again. Checked by tests (50 agents × 3600 points: a report of one agent draws one widget again, and a timing test compares the cost per message with 36 and 3600 points); not measured in a real browser.

## Dashboard: checked JSON, a root error boundary and reconnection with jitter (audit issue #229)

`request()` in `dashboard/src/api.ts` throws an `ApiError` with code `invalid_response` (and the HTTP status) when a 2xx answer is not JSON: by its `Content-Type`, or because it does not parse. A proxy error page or a captive portal answering 200 used to surface as a bare `SyntaxError`. The live data is checked by small guards (`dashboard/src/shapes.ts`) rather than a schema library: an agent needs an id, a report needs a valid time and every number the widgets read, missing or `null` disk and network lists become empty, and what fails is dropped (a stream event, a report of the history, an agent of a snapshot). The guards check what the widgets read, not the whole API. `RootBoundary` around the app (in `main.tsx`) replaces a white page with a message and a reload button; widgets keep their own boundary. The stream reconnects after a delay that doubles up to 15 s with up to half of it left out at random, so dashboards cut off together do not come back together; the delay returns to the first one only after a connection stayed open 5 s (`STABLE_MS`), so a hub that accepts and closes at once is not hammered, and after the hub's `1008 "too slow"` close the first wait is at least 5 s. Checked by tests with a fake socket and fake timers, not on a real network.

## Resetting one's own credentials is refused (audit issue #203)

`CreateReset` and `ResetTwoFactor` refuse the caller's own account (`403`). Both used `mayManage`, which lets a user manage themselves, so a stolen admin session could make its own reset link or strip its own second factor without the password the normal flows ask for. We chose to refuse rather than to ask for password and code in these paths, because the normal flows already exist (`ChangePassword`, `DisableTwoFactor`), the dashboard already disables these buttons for oneself, and an admin who lost everything has `regulus hub reset-admin` on the machine (it does not go through these two methods). A stolen session could still create another admin; step-up re-authentication (#279, below) now asks for the password again for that and the other sensitive actions.

## Step-up re-authentication for sensitive actions (audit issue #279)

Follow-up of #203: a stolen admin session could still create another admin, hand out permissions, enroll a machine of its own or install an update. Those actions (the table in [Authentication](authentication.md)) now need a proof of the password, and of the second factor when on, no older than **5 minutes**. Choices:

- **A recent proof kept on the session, not the password in every request.** One dialog covers a short series of changes (create a user, then set the role), and the API of each action stays as it was. The proof is a column of `sessions` (`reauth_at`, migration 0014), not memory, so it survives a restart and will be shared by redundant hubs (#103); a session from before the migration has none and is asked once.
- **A login counts as a proof**: the person just typed the password and the code. So an admin who signs in and acts at once is not asked twice. The cost: a cookie stolen within 5 minutes of a login works without asking, which the window accepts.
- **The answer is `403` with `code: reauth_required`**, not `401`: `401` means "no session" and sends the dashboard to the login page. The dashboard handles the code in one place (the API client) and repeats the request once after the dialog, so no page needed its own handling.
- **Checked after the permission**, so a viewer is refused, not asked for a password.
- **A recovery code is accepted** instead of the app code, like the other checks of a logged-in user, and is used up.
- **Which actions:** the ones that give a lasting way in, change what others may do, or install software as root; also revoking an agent, which is destructive and was named in the issue. Renaming, cancelling a token, checking and downloading updates and stopping a rollout are not: they give nothing lasting. The own account already asks (password change, two-factor off).
- **Not verified in a browser.** Go tests cover each endpoint without and with a proof, password and code, a recovery code, the limit; dashboard tests cover the dialog and the retry with a mocked API.

## The client address behind a reverse proxy: `trusted_proxies` (audit issue #196)

`X-Forwarded-For` and `X-Forwarded-Proto` are believed only when the peer of the connection is listed in `trusted_proxies`; the client is the rightmost entry that is not a trusted proxy. The default is an empty list, so a hub with nothing in front of it ignores both headers, which also closes the old hole where any client could mark its own cookie `Secure` or not. The setting is a list, so it is read from the YAML file and `REGULUS_TRUSTED_PROXIES` but is not in the console settings table (`regulus config set` edits scalars). The `Forwarded` header (RFC 7239) is not read. The agent port is not affected: it has no proxy in front (mutual TLS).

## The dashboard listens on loopback by default; HTTPS from the hub (audit issue #197)

`listen` defaults to `127.0.0.1:8080` (built in and in the packaged `config.yaml`); it was `:8080`, every interface, over plain HTTP, so a fresh install exposed the login, the first-admin setup and the enrollment token (with the CA fingerprint) in cleartext to the whole network. We chose loopback over "require an explicit `listen`" because the installer must give a working hub with no question asked (`--yes`, `curl | sh`), and an SSH tunnel is enough for the first admin; the installer and the package print the tunnel command. The hub can now serve HTTPS itself (`tls_cert`, `tls_key`): one pair of PEM files, TLS 1.2 or later, HTTP/1.1 only (the live view is a WebSocket, which browsers open over HTTP/1.1, so HTTP/2 would bring nothing and a second code path), files read again at most once a minute so a renewed certificate needs no restart, a half pair or a key that does not match stops the hub at start. With TLS on, `isHTTPS` is true from the connection itself, so the cookie is `Secure` and HSTS is sent without a proxy. Not done: generating a self-signed certificate (a browser warning on every visit trains people to click through; the tunnel is the better default), ACME, client certificates. The two paths are not in the console settings table (a pair, like `trusted_proxies`).

The hub logs a warning at every start when it serves plain HTTP on an address that is not loopback (the bound address is checked, so `:8080` counts as every interface). It warns also behind a reverse proxy on another machine, because that hop is plain HTTP; the fix is TLS on the hub or a proxy on the same machine.

**Upgrades keep the old exposure, on purpose.** dpkg replaces a configuration file the admin never edited, and the new built-in default applies when `listen` is missing, so an upgrade would have taken a working dashboard off the network silently (including after an update started from that very dashboard). The package's pre-install step therefore adds `REGULUS_LISTEN=:8080`, with a comment, to `/etc/regulus/regulus.env` when the old address was `:8080` or unset and nothing in that file sets it already; it says so on the terminal. An address the admin chose is kept by dpkg anyway. The warning in the log is what pushes such a hub towards TLS. **Not verified on a real machine:** the conffile replacement and the pre-install step during `apt` upgrades from the previous release, a fresh install's printed instructions, and HTTPS in a browser (only Go tests: a self-signed pair served over TLS, reload, refusal of a half pair, the warning).

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

## Certificate end dates: kept, shown and warned about (audit issue #245)

Agent certificates last five years and the CA twenty, with no renewal (#128), and nobody was told when one ends. Choices:

- **The agent's end date is stored, not recomputed.** `agents.cert_not_after` (migration 0010, both backends) is written at enrollment from the signed certificate. Agents enrolled earlier are filled in from the certificate they present on their next connection (only when its serial is the one issued for them); the hub does not keep agent certificates, and computing `created_at + 5 years` would be a guess. Until then the date is unknown and counted in `agents_unknown`. The agent protocol is unchanged.
- **One endpoint for the hub-level dates, one field per agent.** `GET /api/certificates` for the CA, the listener, the soonest and the warnings; `cert_not_after` in the existing agent answers for the Servers page. It needs `servers.manage`, like the other trust settings (tokens, the listener): renewing is that permission's job, and a viewer has nothing to act on.
- **What is warned about:** the CA and agents that are not revoked, within 90 days or already ended. The listener is not: it renews itself 30 days before its end (#204). The warning carries data only; the dashboard writes the text in both languages.
- **One log line a day**, not one per certificate: checked every hour, logged at start and then at most once in 24 hours while something ends soon. A restart logs it again.
- **Not verified on a real machine:** the dates shown on a real install and the daily log line under journald (to be added to the certificate test, #241).

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
- **Agent first.** The supervisor was first wired into `regulus agent` (`plugins_dir`, off by default); the hub followed in #262 (below).
- **Not done, on purpose:** signatures, installation, enforcement of permissions, a user per plugin. Until #20, the plugins directory must be writable by root only: who can write it decides what runs.

## Plugins on the hub and their state (#262)

- **The hub runs its plugins like an agent does.** `plugins_dir` (`--plugins-dir`, `REGULUS_PLUGINS_DIR`), off by default, read at start, also settable with `regulus config set`. The supervisor (role `hub`) starts in `Hub.Serve` beside the HTTP and agent servers and is stopped before `Serve` returns. Sockets in `<data_dir>/plugins-run`, plugin data in `<data_dir>/plugins-data/<id>`.
- **The database password stays out.** The hub's environment holds `REGULUS_DATABASE_PASSWORD`; the supervisor builds each plugin's environment from nothing (it never copies `os.Environ`), and a test runs a plugin on a hub whose environment holds the password and checks what the plugin saw.
- **Agents report their plugins with their metrics**, not in a new message: `MetricsReport.plugins` and `plugins_enabled` (fields 7 and 8), added to protocol 1 without raising `protocol.Version`, because an older hub ignores unknown fields and an older agent sends none (which reads as "no plugins directory"). The hub keeps the last list with the agent and removes it from the stored report, so it does not fill the hour of metrics history. Lists and texts are cut like the rest of a report (64 entries, 256 bytes).
- **`GET /api/plugins` needs `servers.read`**: the state of what runs on the servers is read like the servers themselves, by every role. It says nothing a viewer could use to change anything; the plugins' errors are messages for an administrator (exit status, health message), not secrets.
- **One JSON shape for the hub and for agents** (`id`, `version`, `state`, `restarts`, `last_error`, `since`); the process id is not sent.
- **The console** shows the hub's plugins on the Status tab and in `regulus status`, only when the hub has a plugins directory, so nothing changes for a hub without one. The agents' plugins are in the dashboard only.
- **Not done:** a manifest that is invalid is logged but not listed in the API (the supervisor only reports plugins it could read); no health check in the console for a failed plugin; no event on the live stream when a state changes (the dashboard polls).

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

## Web UI: the machine room direction (#91)

The maintainer chose the "machine room" drafts on 2026-11-19 (only `design-drafts/machine-room-v2.html` counts). This pull request rebuilds the existing pages in that look; it adds no page.

- **No component library.** The pieces the issue lists (badge, confirmation, row menu, chips, command box, notices, cards on narrow screens) are a few small components in `dashboard/src/ui.tsx` and classes in `styles.css`. A library would bring its own look and more code to review, for about ten components.
- **Fonts are bundled** (`@fontsource-variable/archivo`, `@fontsource/ibm-plex-mono`, about 180 KB of woff2 in the binary) because the CSP (#201) is `default-src 'self'`; loading them from Google would also tell a third party who opens the dashboard.
- **Theme and language are saved in the browser** (`regulus.theme`, `regulus.language`; "System" and "Browser language" remove the saved value). Saving them per user on the hub is possible later; nothing else needs it now.
- **Navigation:** a sidebar with two sections, a drawer below 52rem. **Rejected:** a bottom bar (four items today, more with plugins).
- **Small screens:** one column below 40rem, two up to 1024 px, in the saved order, with Move up and Move down instead of dragging (touch and keyboard). The saved layout is the wide one; moving swaps two widgets' positions.
- **Confirmations in place** instead of `window.confirm`: they say what happens, can be styled and translated, and can be tested.
- **Not built:** the isometric room, the alert pins and the load wall of the draft. They need the physical location of nodes and an alert model, which need their own design issues (`design-drafts/README.md`). The "custom build" badge and the solar-system idea in the issue's comments are not part of this.
## Changing a node's type, and guided installation (#164, #165, proposed)

Written on 2026-11-19. **Proposed**: waiting for the maintainer's agreement. Nothing is implemented. The full page is [node-type-changes.md](node-type-changes.md); the prototype is `design-drafts/node-types-v2.html`.

- **One operation, three front ends.** The hub has one operation, *set a node's assignment* (preset plus per-node overrides, as in `internal/capability`). The dashboard, the console and `regulus server type` are front ends to it. The hub also says, for each target type, whether the change is possible and why not (a closed list of reason codes with the command that fixes it), so the three cannot disagree. **Rejected:** working the rules out in the dashboard (they would drift from the CLI).
- **A new permission, `servers.assign`**, admin only by default. **Rejected:** reusing `servers.manage` (given to `operator`): making a server public is a change in exposure, not housekeeping. Opening a listener and promoting a hub also ask for the password again (#279, done for the agent listener; to be applied to these when they exist).
- **The second key is `allow` in `/etc/regulus/agent.yaml`**: empty by default, only capabilities that need consent (`relay`, `notifications`, `authentication`), set with `install.sh --allow`, `regulus agent allow`, the node's console, or by hand, always as root on the node. **The hub can never change it**: no message carries it from the hub, and the agent process cannot write the file (`0640 root:regulus`). The node reports the list, and the report can only narrow what it does. The effective set is *assigned ∩ allowed*, enforced by the hub and again by the node.
- **Backup storage needs a place, not consent:** `backup-storage` opens no port, but it cannot be applied until the node has set `backup_dir` with enough free space.
- **Role or capability.** Changing between compute, backup, gateway and development is a capability change on the role `agent`. Redundant hub is a role transition through #105. The active hub is never changed by this operation (#109).
- **While connected:** a new `Assign` command carries the full effective list (protocol version bump, N / N-1), `Welcome` carries the current assignment, and there is an assignment version per node. An offline node, or one on an older protocol, keeps the change as *pending*.
- **Undoing a type moves no data.** A gateway stops at once. A backup node stops accepting new backups but keeps what it has, read-only, until the administrator moves (#171) or deletes it. Workloads block a change to a type without `workloads`.
- **Guided installation: one source.** One YAML file per type, embedded in the binary (`internal/guide/steps/<type>.yaml`), feeds the dashboard wizard, the console, generated documentation (checked by CI) and a JSON export for the configurator (#167). Placeholders come from a closed list with no template logic. Texts are in English and Italian inside the file; **commands are never translated**. Checks come from a closed list implemented in Go. **Rejected:** keys into the dashboard's catalogs (the console and the configurator could not read them).
- **Checking each step:** *auto* (the hub checks, and checks again while the wizard is open), *manual* (the administrator confirms; shown as "confirmed by you, not checked"), or *partly*. "Reachable from outside" is proved only from the hub's network; the internet test stays manual. **Rejected:** an external probe service (a third party would learn the group's addresses).
- **The type travels with the enrollment token**: the wizard stores the intended preset with the token and the hub assigns it at enrollment. The node still applies only what `allow` permits.
- **Secrets:** token rules unchanged. The token is shown once and never appears in the docs, the configurator, URLs, logs, audit details or the wizard's saved record.
- **Not decided:** the permission name and whether `operator` gets the light changes; whether `accept_promotion` moves into `allow`; a redundant hub that also carries agent capabilities (proposed no); leaving the development type; the minimum resources per type (to measure).

## Web terminal and file browser (#236, #237, proposed)

Proposed on 2026-11-19, **waiting for the maintainer's agreement**; nothing is implemented. The full design, with the threat model, is [node-access.md](node-access.md); the prototype is `design-drafts/node-access-v2.html`.

- **One answer for both.** The terminal and the file browser share one transport, one root helper and one answer to "as which user", under the name **node access** (not related to Regulus Access).
- **As which user: a local account of the node, authenticated by the node (PAM).** A new socket-activated root unit, the **session helper**, checks the account against a root-owned list on the node (`/etc/regulus/node-access.yaml`, empty by default, `root` always refused), runs PAM with the prompts relayed to the browser, opens a real login session and drops to that account before starting the shell or the file worker. `sudo` inside follows the node's own rules, so Regulus never grants root. **Rejected:** a shell as the agent's own sandboxed user (safe and almost useless); an SSH client to the node's `sshd` (needs `sshd` everywhere and keys or passwords handled by the hub; possible later as a second backend); a root shell on a hub grant (a hub compromise would be root everywhere).
- **Core, off by default on both sides ("two keys").** In the single binary; the hub grants the capability `terminal` or `files` per node (audited, step-up) **and** the node opts in locally (`install.sh --allow terminal,files`, `regulus node-access enable …`). The helper units ship disabled, so a node that never opts in cannot be reached even by a compromised hub. **Rejected:** a separate package (one binary, #227) and a plugin for now (the plugin runtime cannot host a root helper and isolation, #267, is not designed); #68 is to be updated.
- **Explicit permissions.** `terminal.open`, `files.read`, `files.write`, `files.delete`, `files.chmod`, `sessions.manage` are not covered by a role with "every permission", so the built-in `admin` does not get them by upgrading; two-factor must be on, and every new session asks for the password and the code (#279).
- **Files only beneath roots listed on the node**, each `ro` or `rw`, resolved with `openat2` (`RESOLVE_BENEATH`, no magic links, no mount crossing) and fd-relative operations; refused on kernels without `openat2` (RHEL 8). No trash; recursive delete names the node and the path.
- **Each session has its own gRPC stream** on the agent's existing mTLS connection (agent protocol 2), so transfers never delay heartbeats; HTTP/2 flow control is the backpressure. Node access is refused over plain HTTP except from loopback.
- **Recorded:** every session and every file change in the audit log, never content; the node keeps its own login records through PAM. **Not recorded:** session contents (no recording in the first version).
- **Residual risk accepted:** after a hub compromise, local passwords typed into the dashboard can be captured; mitigations are on the node (dedicated accounts, PAM TOTP, short lists, the node key off).
- **Milestone:** M4, after this design, #169/#170 and #279 are agreed.
- **Not decided:** two-factor required versus following the global policy; whether listings are audited; xterm.js under the hub's CSP (it injects inline styles); SELinux on RHEL; per-node scoping of user permissions (waits for node groups); detach and reattach.

## Network visibility: open ports and the traffic map (#282, #283, proposed)

**Proposed**, awaiting the maintainer's agreement; nothing is implemented and the kernel behaviour it relies on has not been checked on a real machine. The full page is [network-visibility.md](network-visibility.md); the prototype is `design-drafts/network-v2.html`.

- **Sockets, not packets.** The agent reads the kernel's socket tables through `sock_diag` netlink (what `ss` uses), with `/proc/net/{tcp,tcp6,udp,udp6}` as a fallback, unprivileged. **Rejected:** packet capture (needs `CAP_NET_RAW`, costly, sees content), eBPF (needs `CAP_BPF`/`CAP_PERFMON` and BTF; revisit as a privileged tier), conntrack on every server (needs `CAP_NET_ADMIN`; it is the right source on a firewall node, #284), and interface counters as proof of "in use" (they stay the total).
- **Owner without root:** the socket's user, and its systemd unit or container from the socket's cgroup id. The exact process of another user's socket comes only from one fixed, opt-in root-helper operation (inode → pid, name, executable, unit; no command lines), as a privileged action of #60. **Rejected:** `CAP_SYS_PTRACE` for the agent (root in practice), `sudo ss -p` (blocked by `NoNewPrivileges`), running the agent as root (#46). The units need `AF_NETLINK` in `RestrictAddressFamilies`.
- **Labelling honestly.** Regulus's own traffic (agent connection, update download, replication) is counted exactly inside Regulus and labelled by kind; everything else is *other* with the owner Regulus could read, and what no socket explains is shown as *unattributed*. A protocol is never inferred from a port number.
- **One collector.** A single network-state collector in the agent; the inventory (#62) shows its listeners instead of collecting ports again.
- **Stored on the hub:** current listeners plus a change log (appeared, gone, owner or exposure changed, a baseline row for a new agent, flapping collapsed), not a time series. Traffic is summarised per destination per interval: 5-minute buckets for 48 hours, hourly for 30 days, capped per agent and under a global byte budget (#244). Numbers are proposals. **Rejected:** keeping raw connections.
- **Protocol:** add-only. New `AgentMessage` payloads (`NetworkState`, `TrafficReport`), `Welcome.network` saying what the hub wants, a `RefreshNetwork` command; sent only when `Welcome` asks, so no `protocol.Version` bump and the update contract is untouched.
- **Privacy:** destinations are addresses, ports, owners and bytes, never content, URLs, DNS queries or SNI. Destination collection is **off by default per node**; the hub turns it on and the node can refuse locally (deny wins). Reverse DNS by the hub is opt-in and labelled "reverse name".
- **Permissions:** `ports.read`, `traffic.read`, `destinations.read`, `traffic.configure`, all admin-only by default; viewing and switching collection are audited (coalesced per user and hour).
- **Expected ports** come only from declared sources: Regulus's own listeners, node type presets (#169), app manifests (#288), managers (#290), plus an administrator's audited acknowledgement. **Rejected:** a built-in list of "usual" ports.
- **Read-only.** Opening and closing ports belongs to the firewall manager (#290) and the firewall node (#284). The ports page makes the rule of #197 visible on every server; it does not replace the default.
- **Not decided:** whether `ports.read` should be `inventory.read`; whether destinations default on for some node types; replication of this data (#106); alerts on an unexpected port need an alerting design that does not exist.

## Open questions

- Should the other privileged actions use a `sudoers.d` rule or a root helper like the updates? (#60)
- Plugin features, dependencies, signatures and isolation: see the open questions in [plugins](plugins/README.md).
