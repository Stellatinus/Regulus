// Package localstatus is the small channel between a running hub and the
// tools on the same machine (the console and `regulus status`): a Unix socket
// in the data directory, readable only by the user that runs the hub. It
// carries no login; the file permissions are the proof of who may ask.
package localstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

// SocketName is the file name of the socket inside the data directory.
const SocketName = "regulus.sock"

// SocketPath is where the hub of dataDir listens for local tools.
func SocketPath(dataDir string) string {
	return filepath.Join(dataDir, SocketName)
}

// Status is what a running hub reports about itself.
type Status struct {
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
	// Addr is where the dashboard is bound; AgentAddr where remote agents
	// connect, or "" while that is off.
	Addr      string `json:"addr"`
	AgentAddr string `json:"agent_addr,omitempty"`
	// CAFingerprint is what a new agent pins to trust this hub.
	CAFingerprint string `json:"ca_fingerprint,omitempty"`
	BuiltinAgent  bool   `json:"builtin_agent"`
	// SetupToken is the one-time token to create the first admin; it is
	// empty once the admin exists.
	SetupToken string  `json:"setup_token,omitempty"`
	Agents     []Agent `json:"agents"`
	// Database names the backend (never with a password); DatabaseProblem is
	// empty while the database answers.
	Database        string `json:"database,omitempty"`
	DatabaseProblem string `json:"database_problem,omitempty"`
	// PluginsEnabled is true when the hub has a plugins directory; Plugins
	// is the state of each plugin it runs, and PluginsProblem why none runs
	// (the directory cannot be read).
	PluginsEnabled bool     `json:"plugins_enabled,omitempty"`
	PluginsProblem string   `json:"plugins_problem,omitempty"`
	Plugins        []Plugin `json:"plugins,omitempty"`
	// CertSoonest is the certificate that ends first (the CA, the agent
	// listener's or an agent's); CertWarnings are the CA and agent
	// certificates that end within 90 days, soonest first. Both are empty
	// from a hub older than #245.
	CertSoonest  *Certificate  `json:"cert_soonest,omitempty"`
	CertWarnings []Certificate `json:"cert_warnings,omitempty"`
}

// Plugin is one plugin of the hub (docs/plugins/runtime.md).
type Plugin struct {
	ID        string    `json:"id"`
	Version   string    `json:"version"`
	State     string    `json:"state"`
	Restarts  int       `json:"restarts"`
	LastError string    `json:"last_error,omitempty"`
	Since     time.Time `json:"since,omitzero"`
}

// Certificate is one of the hub's certificates and when it ends.
type Certificate struct {
	// Kind is "ca", "listener" or "agent".
	Kind string `json:"kind"`
	// AgentID and Name are set for an agent's certificate.
	AgentID  string    `json:"agent_id,omitempty"`
	Name     string    `json:"name,omitempty"`
	NotAfter time.Time `json:"not_after"`
}

// Agent is a connected agent.
type Agent struct {
	ID            string    `json:"id"`
	Hostname      string    `json:"hostname,omitempty"`
	Version       string    `json:"version,omitempty"`
	Status        string    `json:"status"`
	LastHeartbeat time.Time `json:"last_heartbeat,omitzero"`
}

// Handler serves the status as JSON at /status.
func Handler(get func() Status) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(get())
	})
	return mux
}

// Fetch asks the hub of dataDir for its status. The error is not nil when no
// hub is listening there.
func Fetch(ctx context.Context, dataDir string) (Status, error) {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", SocketPath(dataDir))
			},
		},
		Timeout: 3 * time.Second,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://hub/status", nil)
	if err != nil {
		return Status{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Status{}, fmt.Errorf("the hub answered %s", resp.Status)
	}
	var st Status
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return Status{}, err
	}
	return st, nil
}
