package alerts

import "time"

// Snapshot is what the hub knows at one moment. The hub builds it, so the
// engine reads no registry, report or certificate itself and can be tested
// with a made-up one.
type Snapshot struct {
	Now time.Time
	// Only, when not empty, limits the evaluation to these conditions; the
	// alerts of the others are left as they are. The hub uses it to say at
	// once that it lost the active-hub lock.
	Only []Condition

	// Agents are the servers the hub knows: the enrolled ones that are not
	// revoked, and the connected ones. An alert about a server that is not in
	// the list is resolved with the reason subject_gone.
	Agents []Agent
	// Certificates are the end dates of the CA, the listener and the agents.
	Certificates []Certificate
	// UpdateFailures are the servers whose update failed or was rolled back
	// and that are still behind.
	UpdateFailures []UpdateFailure
	// HubLockLost is true while another hub holds the active-hub lock.
	HubLockLost bool
}

// Agent is one server.
type Agent struct {
	ID   string
	Name string
	// Connected says whether the server has a connection now; LastSeen is when
	// it last had one (zero if never).
	Connected bool
	LastSeen  time.Time
	// Reporting is true when Connected and a recent report is the source of
	// the numbers below. A server that is not reporting is not evaluated for
	// disks, memory or CPU: one outage is one alert.
	Reporting bool
	// CPUPercent is nil when the server could not read it.
	CPUPercent    *float64
	MemoryPercent *float64 // in use, without cache
	Disks         []Disk
}

// Disk is a mounted file system of a server.
type Disk struct {
	Mount      string
	TotalBytes uint64
	UsedBytes  uint64
}

// Certificate is a certificate with its end date. Subject is stable: "ca",
// "listener" or "agent:<id>"; AgentID is set for an agent's certificate.
type Certificate struct {
	Subject  string
	AgentID  string
	Name     string
	NotAfter time.Time
}

// UpdateFailure is a server whose update did not work.
type UpdateFailure struct {
	AgentID string
	Name    string
	Version string
	Message string
}
