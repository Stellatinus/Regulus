# Development

Requirements: Go 1.26+, Node.js 22+.

```sh
make build            # builds the dashboard, then bin/regulus
./bin/regulus hub     # serves the dashboard on :8080
make test             # go vet, go test -race, dashboard typecheck and tests
make test-postgres    # the database tests on PostgreSQL too (needs REGULUS_TEST_DATABASE_URL, see the Makefile)
make lint             # golangci-lint and ESLint
make smoke            # start bin/regulus and check it end to end (after make build)
make fuzz             # run each fuzz target for 30s (FUZZTIME=2m for longer)
scripts/test-install.sh   # tests of the install script (also part of make test)
```

For dashboard work, run `npm run dev` in `dashboard/` (proxies `/api` to the hub on `:8080`).

## Fuzz tests

The /proc parsers of the agent and the network rates of the hub have fuzz targets (`func Fuzz…` in `internal/agent/collector_fuzz_test.go` and `internal/hub/rate_fuzz_test.go`). `go test` runs their seed inputs every time; `make fuzz` and the nightly **Fuzz** workflow look for new inputs. When a fuzzer fails it saves the input under `testdata/fuzz/<Target>` of the package: fix the code and commit that file (or turn it into a named test) with the fix.
