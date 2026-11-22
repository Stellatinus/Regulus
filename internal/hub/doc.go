// Package hub implements the Regulus hub, the process that everything else talks
// to: it serves the dashboard and the HTTP API, accepts the connections of the
// agents, keeps the live state of the fleet and drives the updates.
//
// What it does, and where to start reading:
//
//   - hub.go: New builds a Hub from the configuration, Serve runs it. Serve
//     starts the HTTP server (dashboard, API, the /api/stream WebSocket) and,
//     when agent_listen is set, the gRPC server for remote agents.
//   - https.go: how the dashboard port is exposed: HTTPS from tls_cert and
//     tls_key (reloaded when the files change), or plain HTTP with a warning
//     at start when that port reaches other machines.
//   - headers.go: the browser security headers (CSP, framing, sniffing, referrer,
//     HSTS) set on every response.
//   - disks.go: the physical disks the agents report, matched to an identity
//     (internal/diskid), and the moves of a disk between servers.
//   - api.go, openapi.go, layout.go, updates_api.go: the HTTP API and its
//     OpenAPI description.
//   - agents.go, agent_net.go, local.go: the server side of the agent-hub
//     contract (proto/regulus/agent/v1): heartbeats, metrics, commands,
//     the in-process connection of the built-in agent, refusal of agents
//     whose version is not accepted, and the agent listener's checks (a
//     certificate on every call but Enroll, the keepalive policy).
//   - seen.go: the presence writer, which records when agents were seen off
//     the handshake path, every minute for connected agents and at Close.
//   - fleet_api.go: enrollment tokens and the list of enrolled agents, on top of
//     package fleet.
//   - metrics.go, events.go, stream.go: samples kept in memory (bounded per
//     agent and for all agents together) and the live stream sent to the
//     dashboard (bounded per user).
//   - rollout.go: the update of the agents in waves, driven by an administrator.
//   - plugins.go: the hub's own plugins (plugins_dir, role hub), run beside its
//     servers, and GET /api/plugins with the plugins the agents report.
//   - certs.go: when the CA, the agent listener and each agent certificate end
//     (GET /api/certificates, the local status, a daily warning in the log).
//     It only reports: renewing them (#128) is not done here.
//
// The hub does not own the details of accounts and sessions (package auth),
// certificates (pki), the registry of agents (fleet), release download and
// verification (update), how plugins are found, started and restarted
// (plugin) or the agent logic itself (agent). Redundancy and
// failover (#103) do not exist yet.
package hub
