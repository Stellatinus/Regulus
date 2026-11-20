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

Every process log is printed when the test fails, and a `DATA RACE` in any of them fails it. Breaking the pinning (`verifyPinned` accepting anything), the revocation of the live stream (`agentServer.disconnect`) or the revocation check at connection (`requireAgent` without `fleet.Authorize`) makes it fail; this was checked by hand when the test was written.

**Not covered:** an update sent over the stream (`ApplyUpdate` needs a hub that trusts a signed release, and a hub binary trusts only the project's Sigstore identity, so a test cannot make one; #341 stays open for it), a hub CA that has expired, PostgreSQL as the hub database, and anything that needs systemd (the restart policy on status 3 is only asserted as the exit status). Nothing here replaces a run on a real machine (#81).
