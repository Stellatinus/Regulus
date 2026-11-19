package hub

import (
	"context"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/CodexCircle/Regulus/internal/localstatus"
	"github.com/CodexCircle/Regulus/internal/storage"
)

// maxSocketPath is the longest path a Unix socket can have on Linux.
const maxSocketPath = 107

// localDBTimeout bounds the one database query of a status answer (the agent
// certificates). The client gives the whole request 3 s, so a database that
// does not answer must cost much less than that (#313).
const localDBTimeout = 500 * time.Millisecond

// localStatus describes the running hub for the tools on the same machine. It
// never waits for the database longer than localDBTimeout: the database's state
// is the one watchDatabase last recorded, not a new ping, so `regulus status`,
// the console and the update helper get an answer even when PostgreSQL drops
// packets, and that answer names the database problem.
func (h *Hub) localStatus(ctx context.Context, addr string) localstatus.Status {
	st := localstatus.Status{
		Version:       h.version,
		StartedAt:     h.startedAt,
		Addr:          addr,
		AgentAddr:     h.AgentAddr(),
		CAFingerprint: h.fleet.CA().Fingerprint(),
		BuiltinAgent:  h.cfg.BuiltinAgent,
		SetupToken:    h.auth.SetupToken(),
		Agents:        []localstatus.Agent{},
		Database:      h.cfg.Storage().Describe(),
	}
	st.DatabaseProblem = h.dbState.get()
	hp := h.HubPlugins()
	st.PluginsEnabled, st.PluginsProblem = hp.Enabled, hp.Problem
	for _, p := range hp.Plugins {
		st.Plugins = append(st.Plugins, localstatus.Plugin{ID: p.ID, Version: p.Version, State: p.State, Restarts: p.Restarts, LastError: p.LastError, Since: p.Since})
	}
	now := time.Now()
	// While the database is known not to answer, the certificates are not
	// asked for: each request would wait localDBTimeout for nothing.
	if st.DatabaseProblem == "" {
		cctx, cancel := context.WithTimeout(ctx, localDBTimeout)
		certs, err := h.Certificates(cctx)
		cancel()
		switch {
		case err == nil:
			soonest := toLocalCert(certs.Soonest())
			st.CertSoonest = &soonest
			for _, w := range certs.Warnings(now) {
				st.CertWarnings = append(st.CertWarnings, toLocalCert(w))
			}
		case ctx.Err() == nil && h.cfg.Storage().IsPostgres():
			// The watcher has not noticed yet (it asks every
			// dbWatchInterval); this answer already says it. SQLite has one
			// connection, which a long write can hold for a moment: that is
			// not a problem to report.
			st.DatabaseProblem = storage.Explain(err)
		}
	}
	for _, a := range h.Agents() {
		j := toAgentJSON(a, now, h.StaleAfter())
		agent := localstatus.Agent{ID: j.ID, Hostname: j.Hostname, Version: j.Version, Status: j.Status}
		if j.LastHeartbeat != nil {
			agent.LastHeartbeat = *j.LastHeartbeat
		}
		st.Agents = append(st.Agents, agent)
	}
	return st
}

// serveLocal starts the Unix socket for local tools in the data directory and
// returns the function that stops it. The socket is readable by the hub's
// user only; when it cannot be made the hub runs without it.
func (h *Hub) serveLocal(addr string) (stop func()) {
	path := localstatus.SocketPath(h.cfg.DataDir)
	if len(path) > maxSocketPath {
		h.log.Warn("the data directory path is too long for the local status socket; `regulus status` will not see live data", "path", path)
		return func() {}
	}
	_ = os.Remove(path) // left behind by a hub that was killed
	ln, err := net.Listen("unix", path)
	if err != nil {
		h.log.Warn("cannot open the local status socket", "path", path, "err", err)
		return func() {}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		h.log.Warn("cannot restrict the local status socket", "path", path, "err", err)
		_ = ln.Close()
		_ = os.Remove(path)
		return func() {}
	}
	srv := &http.Server{
		Handler:           localstatus.Handler(func(ctx context.Context) localstatus.Status { return h.localStatus(ctx, addr) }),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = os.Remove(path)
	}
}
