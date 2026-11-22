// Package agent is the client side of the agent-hub contract defined in
// proto/regulus/agent/v1. It reports the machine (system information,
// heartbeat, metrics, the identifiers of its physical disks, and the state of
// the plugins it is given) to a hub and
// executes the commands the hub sends, such as applying an update. It does not
// run plugins itself: `regulus agent` hands it the supervisor's status. It
// reads the identifiers of a disk (physdisks.go) but does not decide its
// identity: that is the hub's, through internal/diskid.
//
// The same code runs in two places:
//
//   - inside the hub, as the built-in agent, over the in-process transport of
//     package inproc (see Run in agent.go);
//   - on a remote server, as `regulus agent`, which connects outbound to the hub
//     over gRPC with TLS 1.3 and a client certificate (remote.go: Enroll
//     exchanges a single-use token for credentials, Credentials.Dial opens the
//     connection).
//
// Reading order: agent.go (Run and Options), collector.go and sysinfo.go (what is
// measured), update.go (applying an update), remote.go (enrollment, and the remote
// connection with its keepalive). Reports collected while no connection is up
// are not kept or replayed. The certificates are issued by package pki through the hub, and
// the hub side of the protocol lives in package hub.
package agent
