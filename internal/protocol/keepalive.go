package protocol

import "time"

// The keepalive of the agent connection (#222, #244). Both ends are set from
// these numbers, so that the hub's policy always allows what the agent does.
//
// They are a contract between versions, like the messages: an agent of one
// release meets hubs of other releases. The rule is that the hub's
// KeepaliveMinTime never rises above the KeepaliveTime of any agent release it
// serves, and an agent never pings more often than the KeepaliveMinTime of the
// hubs it may meet. See docs/versioning.md.
const (
	// KeepaliveTime: an agent that has read nothing from the hub for this long
	// sends an HTTP/2 PING. The hub answers it in its transport, whatever it
	// is doing.
	KeepaliveTime = 30 * time.Second
	// KeepaliveTimeout: a PING not answered within this time means the hub is
	// gone (the host lost power, a NAT dropped the flow): the agent closes the
	// connection and reconnects. A dead hub is therefore noticed within
	// KeepaliveTime+KeepaliveTimeout instead of the many minutes TCP takes.
	KeepaliveTimeout = 10 * time.Second
	// KeepaliveMinTime is the hub's enforcement policy: a client that pings
	// more often than this is warned twice, then disconnected (gRPC's
	// "too_many_pings"). It is half of KeepaliveTime, so that a future agent
	// may ping somewhat more often without being cut off by today's hubs.
	KeepaliveMinTime = 15 * time.Second
	// HubKeepaliveTime and HubKeepaliveTimeout: the hub pings a connection it
	// has read nothing from for this long, and closes it when the PING is not
	// answered, so a dead agent does not hold its stream for the minutes TCP
	// takes. A live agent sends a heartbeat every 15 s by default, so the hub
	// rarely needs to ping. Every agent release answers, since PING is part of
	// HTTP/2.
	HubKeepaliveTime    = 30 * time.Second
	HubKeepaliveTimeout = 10 * time.Second
)
