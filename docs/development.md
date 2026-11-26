# Development

Requirements: Go 1.26+, Node.js 22+.

```sh
make build            # builds the dashboard, then bin/regulus
./bin/regulus hub     # serves the dashboard on 127.0.0.1:8080
make test             # go vet, go test -race, dashboard typecheck and tests
make test-postgres    # the database tests on PostgreSQL too (needs REGULUS_TEST_DATABASE_URL, see the Makefile)
make lint             # golangci-lint and ESLint
make smoke            # start bin/regulus and check it end to end (after make build)
make e2e              # hub and remote agent binaries end to end over mTLS (about 30 s; no dashboard build needed)
make browser-test     # the dashboard in Chromium against the real hub and agents (builds first; needs Playwright, see below)
make fuzz             # run each fuzz target for 30s (FUZZTIME=2m for longer)
scripts/test-install.sh   # tests of the install script (also part of make test)
```

For dashboard work, run `npm run dev` in `dashboard/` (proxies `/api` to the hub on `127.0.0.1:8080`).

## Fuzz tests

The /proc parsers of the agent and the network rates of the hub have fuzz targets (`func Fuzz…` in `internal/agent/collector_fuzz_test.go` and `internal/hub/rate_fuzz_test.go`). `go test` runs their seed inputs every time; `make fuzz` and the nightly **Fuzz** workflow look for new inputs. When a fuzzer fails it saves the input under `testdata/fuzz/<Target>` of the package: fix the code and commit that file (or turn it into a named test) with the fix.

## End-to-end test (hub and remote agent)

`make e2e` (the **End-to-end** job of CI, on every pull request) runs `internal/e2e`: it builds `regulus` with the race detector, starts the hub as its own process on random ports with a temporary data directory and the remote-agent listener on, and runs `regulus agent` processes against it. A plain `go test ./...` skips it (it needs `REGULUS_E2E=1`); `REGULUS_E2E_BIN=path` uses a binary you built. It covers, in order:

1. **Pinning at enrollment:** a wrong CA fingerprint fails, saves nothing, and the token never reaches the hub (it still works afterwards).
2. **Enrollment over the network** with a token from `POST /api/enrollment-tokens`; the agent connects over mTLS, shows as `connected` in `GET /api/agents` and its metrics appear in `GET /api/agents/{id}/metrics`. The token is single use.
3. **Protocol version:** a Hello with a newer, and with an older, protocol than the hub speaks (sent by the test with the agent's real certificate) gets `Reject` with `REASON_UNSUPPORTED_PROTOCOL_VERSION`.
4. **Client certificates:** one for the same agent id signed by another CA, and an expired one signed by the hub's own CA, are refused.
5. **Hub restart:** the running agent reconnects by itself and reports again.
6. **Pinning on every connection:** the agent's credentials pointed at another hub (another CA) do not connect.
7. **Cloned identity:** a copy of the agent's data directory takes the connection over; the original logs that the identity is in use and backs off (at most 3 connections in about 5 seconds).
8. **Revocation:** `DELETE /api/agents/{id}` closes the live stream and the agent exits with status 3; after a hub restart the revoked agent is refused at once and listed as `revoked`.

A second test, `TestAgentOfAnOlderRelease` (#341), downloads the agent of the newest published release (any channel) from the project's releases, checks it against that release's `checksums.txt` (not its Sigstore signature: it only decides what the test runs), and connects it to a hub built from this source with a release version made up for the purpose:

9. **One minor ahead of the old agent:** the agent connects as a normal agent, its metrics appear, and the API says its release is `behind`.
10. **Two minors ahead:** the real old binary is admitted to an update-only session (#212): the API says `too_old` and `update_only`, and no metrics are kept.

It skips, saying why, when there is no release to download (no network, GitHub's rate limit, a repository without releases); CI passes `GITHUB_TOKEN` to avoid the limit and accepts this one skip as a warning. `REGULUS_E2E_OLD_BIN` and `REGULUS_E2E_OLD_VERSION` (for example `0.2.0-alpha.1`) run it with a binary you have, offline.

A third test, `TestPreviousReleaseServesOnTheCurrentSchema` (#383), runs the **program** of that same release on a database that this source migrated, the way a rollback does (the migration tests in `internal/storage` run only the old release's migrations):

11. **The old release starts** on an empty database, then **this source** starts on it, migrates it and creates the first account, then the old release starts **again** on the migrated database. If every migration since is expand-only for it (`reader_min` not above the old release's schema version) it must serve: `/healthz` answers and the account signs in. If one is strict, the old release must refuse with "created by a newer version", and the test logs which migrations are strict (a rollback past them needs the backup). It runs on SQLite, and on PostgreSQL when `REGULUS_TEST_DATABASE_URL` is set (CI sets it with a PostgreSQL 16 service; each run uses a schema of its own). It skips with the same reasons and the same override as the one above, and the PostgreSQL part skips when the variable is not set. Both outcomes were run by hand against real binaries built from older commits; the PostgreSQL part has not run on a real machine (#239).

A fourth test, `TestUpdateOfAnOlderAgentOverMTLS` (#384), needs two published releases: the real agent of the older one connects to a hub built from this source with the newer one's version, a rollout makes the hub download the newer release and check its Sigstore signature, and the old agent fetches it over mTLS, verifies it and leaves its request for the helper (`update.ApplyDir` of its data directory). Nothing installs it (the helper is a systemd unit), so the agent stays old and the rollout stays `running`. It skips, saying why, with fewer than two releases and **when the old agent does not trust the identity that signed the newer release**, which is the case for the two published today (v0.1.0-alpha.1 was signed under the previous organization and v0.2.0-alpha.1 under `Stellatinus/Regulus`, see the move in [decisions](decisions.md): #392). **So the hand-over itself has not run against real releases**; the same flow is covered in process by `TestRolloutUpdatesAnAgentBehind`.

Every process log is printed when the test fails, and a `DATA RACE` in any of them fails it. Breaking the pinning (`verifyPinned` accepting anything), the revocation of the live stream (`agentServer.disconnect`) or the revocation check at connection (`requireAgent` without `fleet.Authorize`) makes it fail; this was checked by hand when the test was written.

**Not covered:** an update **installed** over the stream (the fourth test stops at the request for the helper, and skips until two releases signed by one identity exist, #392; #341 stays open for it), a hub CA that has expired, PostgreSQL as the hub database (except the schema test above), and anything that needs systemd (the restart policy on status 3 is only asserted as the exit status). Nothing here replaces a run on a real machine (#81).

## Browser tests (the dashboard in a real browser, #390)

`make browser-test` (the **Browser tests** job of CI, on every pull request) builds the binary with the dashboard in it and runs `scripts/browser-test/dashboard.test.mjs` with Node's test runner and Playwright's Chromium. The hub is the real binary on loopback with a temporary data directory, and the agents are real `regulus agent` processes enrolled through the API, as in `internal/e2e`. The scenarios share one hub and run in order; each has a browser context of its own with a Playwright trace:

1. **First admin:** the setup page refuses a wrong setup token with a sentence, then creates the admin with the right one.
2. **Live stream:** the Servers page is open on the live stream (**Live** in the top bar) before any server exists; an agent that is started and enrolled afterwards appears and turns `online` without a reload.
3. **Language:** the language select switches to Italian (navigation and `<html lang>`) and back.
4. **Themes:** Light and Dark set `data-theme` on `<html>` and give a light and a dark page background.
5. **Phone:** at 390 by 844 with touch, no page scrolls sideways (Dashboard, Servers, a server, Plugins, Users, Updates, Account).
6. **Refused sign-ins:** with the page in Italian, a `429` shows "Troppi tentativi non riusciti. Riprova tra 2 minuti.", a wrong password through the real hub shows "Nome utente o password errati.", and a `503` with `Retry-After` shows the busy note, waits, asks once more by itself and signs in. (The `429` and the first `503` are made by the test's network interception, not by the hub: the hub's own limits are tested in Go.)
7. **Two-factor:** turn it on in Account with a code computed in the test (RFC 6238), sign out, and see that sign-in needs the code: a wrong one is refused and the right one signs in.
8. **No Content-Security-Policy violation** in the console of any page the scenarios visited.

A failed scenario leaves `browser-test-artifacts/<scenario>.png` (full-page screenshot), `.trace.zip` (open it with `npx playwright show-trace`) and `.txt` (the error and the page's console); CI uploads that directory as an artifact only when the job fails. A passing scenario leaves nothing.

**Playwright is not a dependency of the dashboard.** The test uses the `playwright` package that is installed on its own (`npm install --global playwright@1.63.0 && playwright install --with-deps chromium`, as the CI job does; the cloud session of the agents has both), found through `NODE_PATH` as `scripts/browser-load.mjs` does, so `package.json` and its lockfile carry no browser downloader and `make test` is as light as before. The cost is a second place that names the version (the Makefile comment and `ci.yml`). `REGULUS_BROWSER` points at another Chromium, and `REGULUS_BIN` at another binary.

**What stays manual** (the `[Test]` issues name only these now): Firefox and Safari (the CSP and the live stream there, #281; the automated run on Firefox and WebKit is #393); a real phone, with its keyboard, touch and the browser's own chrome (#309); a real reverse proxy and a real network, including the limits behind `trusted_proxies` (#281, #248); a screen reader (#309); the Italian wording, which only a person who reads Italian can judge (#274); plain HTTP on a LAN address (#309); charts over an hour of real data, and the long-`data_dir` note (#353); the hub busy under real load (#329). The scenarios above do not look at layout beyond horizontal scroll, and do not compare screenshots.

## Load test (hub under 200 agents and 20 dashboards)

`internal/hub/load_test.go` (`TestHubUnderLoad`, #234) is skipped unless `REGULUS_LOAD=1`:

```sh
REGULUS_LOAD=1 go test -run TestHubUnderLoad -v -count=1 ./internal/hub/
# knobs: REGULUS_LOAD_AGENTS=200 REGULUS_LOAD_DASHBOARDS=20 REGULUS_LOAD_INTERVAL=1s REGULUS_LOAD_SECONDS=20
```

It starts a hub (SQLite, built-in agent off, the real 10 s agent intervals), connects N simulated agents that send a heartbeat and a metrics report (2 disks, 1 interface) each interval, and runs M dashboards that poll `GET /api/agents` and one agent's metrics once a second. It prints the hub's CPU time, the metrics history the hub holds, goroutines and p50, p95 and maximum latency of the two calls, and fails if a call is not answered with 200 or an agent is dropped.

**One run** (a 4-vCPU Intel Xeon @ 2.10 GHz virtual machine with 16 GB, Go 1.27, 22 November 2026), 200 agents and 20 dashboards:

| Reports per agent | Hub CPU (of one CPU) | `GET /api/agents` p50 / p95 / max | `GET …/metrics` p50 / p95 / max | Metrics history |
|---|---|---|---|---|
| every 10 s (the real default, 20 reports/s) for 60 s | 1.4% | 2.3 / 5.8 / 69 ms | 0.19 / 0.64 / 30 ms | 4.2 KiB per agent after 60 s |
| every 1 s (ten times the real load, 200 reports/s) for 30 s | 4.4% | 2.4 / 5.1 / 7.3 ms | 0.22 / 0.55 / 2.1 ms | 4.2 KiB per agent after 30 s |

The CPU figure includes the simulated agents and dashboards, so the hub's own share is lower. Nothing was dropped and every call answered 200.

**What this does not measure**, and so what must not be claimed from it: a real network and TLS between agents and hub (the agents use the in-process transport); the WebSocket stream the dashboard really uses (its fan-out to 20 browsers); the database (the metrics history is in memory, and only the Servers and settings data touch it); the browser, where the cost of redrawing 200 servers is: the React profile with 50 or more agents is not done; and memory per connected agent: the process heap in the run is dominated by the 2 MiB of buffers the in-process transport allocates per connection, so it says nothing about a real connection. What the run does support is that the hub's own CPU and the latency of the two API calls are small at 200 agents, ten times the real report rate included. "Tested with" in the documents means this run only. A load run over real TLS connections (several machines, or a loopback listener with the agent binary) and the browser measurements stay open in #234.

## Load test over real connections (#370)

`internal/e2e/load_test.go` (`TestHubUnderRealLoad`) measures what the in-process run above cannot. It runs the real `regulus` binary as the hub in a process of its own (so the CPU and memory are the hub's own), connects N simulated agents over **real mTLS** on a loopback listener (one enrollment and one certificate each), gives each of M users a **real WebSocket** to `/api/stream` and one **database write** a second (a layout save, alternating with `GET /api/agents`), and prints the hub's CPU and memory from `/proc`, the WebSocket events per second and how long a metrics event takes to reach a dashboard after its report, and the latency of the calls.

```sh
REGULUS_LOAD=1 go test -run TestHubUnderRealLoad -v -count=1 -timeout 20m ./internal/e2e/
# knobs: REGULUS_LOAD_AGENTS=200 REGULUS_LOAD_DASHBOARDS=20 REGULUS_LOAD_INTERVAL=10s REGULUS_LOAD_SECONDS=60
# PostgreSQL: REGULUS_LOAD_DATABASE_URL='postgres://user:pass@host:5432/emptydb?sslmode=disable'
# REGULUS_E2E_BIN=bin/regulus uses a binary you built (otherwise one is built without -race)
```

**One run** (a 4-vCPU virtual machine with 16 GB, Go 1.27, PostgreSQL 16 on the same machine over loopback, 22 November 2026; the simulated agents and dashboards share the machine with the hub), 200 agents and 20 dashboards:

| | SQLite, a report every 10 s (real default) | SQLite, every 1 s (10× real) | PostgreSQL, every 10 s |
|---|---|---|---|
| Hub CPU (its own process) | 5.1% of one CPU | 18.2% | 5.0% |
| Hub memory (RSS), with 200 agents connected | 60 MiB (77 at the end, highest 92) | 63 MiB (88 at the end, highest 92) | 57 MiB (76 at the end, highest 91) |
| WebSocket events to 20 dashboards | 800/s | 8,003/s (87 MiB in 30 s) | 801/s |
| Report to dashboard (p50 / p95 / max) | 0.7 / 1.4 / 12.8 ms | 0.6 / 1.0 / 23.3 ms | 0.8 / 1.1 / 10.7 ms |
| `PUT /api/layout` (p50 / p95 / max) | 6.8 / 14.1 / 29.9 ms | 5.9 / 30.5 / 36.0 ms | 8.9 / 18.6 / 31.7 ms |
| `GET /api/agents` (p50 / p95 / max) | 7.9 / 15.7 / 21.1 ms | 11.9 / 23.6 / 45.2 ms | 15.0 / 28.8 / 64.2 ms |

Nothing was dropped: every call answered and no WebSocket closed. The 200 agents enrolled and connected in 2 s. The hub's memory figure has no per-connection value: the hub's start-up peaks near 90 MiB and the runtime returns it over minutes, so two readings do not differ by what a connection costs; the claim that holds is that the hub stayed under 92 MiB with 200 connected agents and 20 dashboards. A write path under a handful of users is not a contention test: 20 users write one small row each per second.

**The dashboard in a browser**, 50 agents connected: `scripts/browser-load.mjs` (needs Playwright and a binary built with the dashboard, see the comment at its top) drives Chromium and reads its own metrics. Run for 30 s, a report every 10 s: the Servers page showed all 50 servers 1.0 s after sign-in; the JavaScript heap was 3.9 MiB at the start and 4.1 MiB after 30 s with a forced garbage collection, the DOM stayed at 1,214 nodes, the page spent 3.9 ms of every second on tasks (1.1 ms running scripts), there was no task longer than 50 ms, and a heap snapshot is 11 MiB. This is **not** the React Profiler: it says what the tab costs, not which component does, and a report every 10 s gives the page little to redraw; a run at 1 s is still to do.

**Still not measured**: several machines and a real network between agents and hub (all of this is loopback, so network latency and the cost of many real TLS handshakes at once are not in it), the write paths under many users writing to the same rows or under load from the agents' own writes (agent presence), PostgreSQL on another host, and the browser with the React Profiler or on a slow device. The limits written in the documents ("tested with") mean these runs only. Findings become `[Audit]` issues: none from this run.

## How CI is organised, and why (#488)

`.github/workflows/ci.yml` runs on every pull request, on every push to `main` and by hand. Measured on the runs of 2026-11-25: 4m50 to 6m53 per run, and 25 minutes of runner time over 15 jobs, even for a pull request that only changed a document.

- **A documents-only pull request skips what a document cannot break.** The `changes` job (`scripts/ci-changes.sh`, tested by `scripts/test-ci-changes.sh`) compares the pull request's merge commit with its base. Only `docs/`, any `*.md`, `LICENSE` and the issue templates count as documents; anything else, a push to `main` and a manual run run everything, and anything the script cannot decide counts as "not documents only". The one test that reads documents (`TestNoStaleRepositoryName`, a stale repository name in any text file) still runs, in the `docs` job. Workflows, scripts, `go.mod`, the dashboard and packaging are code for this purpose.
- **Matrix jobs are never skipped at job level.** The PostgreSQL and package jobs skip their *steps* instead. GitHub reports a matrix job that is skipped before it is expanded under its unexpanded name (`Package lifecycle (${{ matrix.image }})`), so a required check on the real name would wait for ever.
- **The Go job is split** into `go-lint`, `go-test` and `go-one-connection`, which run side by side (the last used to be the final step of the one job, 1m16 of its 4m52). A job named **`Go (lint, vet, test)`** remains and passes when those jobs passed or were skipped, so a required check on that name keeps working. **`All checks`** fails if any job failed or was cancelled and passes when jobs were skipped on purpose: it is the one check the maintainer can require instead of a list (a repository setting, not a file).
- **A run on `main` is never cancelled.** The release workflow requires CI to have passed on the tagged commit (#207); a cancelled run is not a pass. Superseded pull request runs are still cancelled.
- **Every job has `timeout-minutes`**, so a hung test does not hold a runner for the default six hours.
- **Not done, on purpose:** skipping jobs for dashboard-only changes (the Go jobs may depend on the built dashboard in ways nobody has listed), and shortening the slowest tests, which is where most of the remaining time is (`TestOnlyTheMostRecentMovesAreKept` 35 s and `TestFloodOfUnauthenticatedFailuresKeepsThePrivilegedHistory` 23 s on SQLite, each in packages that four jobs run). The timings of the new layout are not known until it has run on GitHub.
