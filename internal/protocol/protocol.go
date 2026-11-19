// Package protocol holds the version of the agent-hub contract defined in
// proto/regulus/agent/v1 and the rules to negotiate it: the full protocol
// (Negotiate), the update contract that every released agent still gets
// (UpdateContract, NegotiateUpdateOnly), and the keepalive both ends of the
// connection agree on (keepalive.go). It does not decide which agent
// releases a hub serves (that is update.CheckCompat) and holds no generated
// code: that lives in internal/gen and must not be edited by hand (`make
// proto`). The rules for changing any of it are in docs/versioning.md.
package protocol

// Version is the protocol version spoken by this build. It is sent in Hello
// and Welcome. Bump it only for changes that an older peer cannot understand;
// adding fields or messages is compatible and does not need a new version.
const Version uint32 = 1

// MinSupported is the oldest protocol version this build still accepts.
const MinSupported uint32 = 1

// UpdateContract is the oldest protocol version whose update contract this
// build still speaks. The update contract (Hello, Welcome, Reject, Heartbeat,
// Command with Ping and ApplyUpdate, CommandResult, FetchUpdate) never changes
// meaning, so it stays at 1 for as long as protocol 1 agents exist: a hub that
// has dropped an old protocol for everything else still updates an agent that
// speaks only that one (docs/versioning.md). Lowering MinSupported never moves
// this.
const UpdateContract uint32 = 1

// Supported reports whether a peer speaking version v can be served.
func Supported(v uint32) bool {
	return v >= MinSupported && v <= Version
}

// Negotiate picks the protocol version for a connection from the range the
// agent speaks. min 0 means an agent that predates the field and speaks only
// max. ok is false when the ranges do not meet.
func Negotiate(min, max uint32) (version uint32, ok bool) {
	if min == 0 || min > max {
		min = max
	}
	version = max
	if version > Version {
		version = Version
	}
	return version, version >= min && Supported(version)
}

// NegotiateUpdateOnly picks the protocol version for an agent that is admitted
// only to be updated: the highest version both sides speak, ignoring
// MinSupported, as long as the agent speaks at least the update contract and
// is not newer than this build (then the hub, not the agent, needs updating).
func NegotiateUpdateOnly(min, max uint32) (version uint32, ok bool) {
	if min == 0 || min > max {
		min = max
	}
	version = max
	if version > Version {
		version = Version
	}
	return version, version >= min && version >= UpdateContract
}
