// Package e2e holds the end-to-end test of a hub and a remote agent, each
// the real `regulus` binary in its own process, talking over the real mTLS
// agent listener (#233). The test builds the binary, starts hubs on random
// ports with temporary data directories, drives them through the HTTP API
// and runs `regulus agent` against them: enrollment with a token, pinning of
// the hub's CA, metrics in the API, refusals (another protocol version, a
// certificate from another CA or expired), a hub restart, a cloned identity
// and revocation. A second test runs the agent of the newest published release
// against the hub of this source (#341).
//
// It has no code outside its test: it is not imported by anything. It runs
// only when REGULUS_E2E=1 (`make e2e`, and the end-to-end job of CI), so that
// a plain `go test ./...` stays fast; REGULUS_E2E_BIN can name an already
// built binary. What it covers is listed in docs/development.md. It does not
// replace scripts/smoke.sh, which checks the packaged binary (dashboard,
// console, configuration commands, admin reset).
package e2e
