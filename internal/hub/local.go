package hub

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/CodexCircle/Regulus/internal/localstatus"
	"github.com/CodexCircle/Regulus/internal/storage"
	"github.com/CodexCircle/Regulus/internal/update"
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
	dbAnswered := false
	if st.DatabaseProblem == "" {
		cctx, cancel := context.WithTimeout(ctx, localDBTimeout)
		certs, err := h.Certificates(cctx)
		cancel()
		switch {
		case err == nil:
			dbAnswered = true
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
	// Only when the database just answered: a second wait on one that does not
	// would double the time of the answer (#313).
	if dbAnswered {
		uctx, cancel := context.WithTimeout(ctx, localDBTimeout)
		if us, err := h.updates.Status(uctx); err == nil && us.UpdateAvailable && us.Latest != nil {
			st.UpdateAvailable = us.Latest.Version
		}
		cancel()
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
// user only; when it cannot be made the hub runs without it, and problem says
// why. Without it `regulus status` sees no live data, and the update helper
// confirms an update through running.json instead (#312).
func (h *Hub) serveLocal(addr string) (stop func(), problem string) {
	path := localstatus.SocketPath(h.cfg.DataDir)
	without := func(problem string, args ...any) (func(), string) {
		h.log.Warn("no local status socket: `regulus status` and the console will not see live data; updates are confirmed through "+update.RunningName+" instead", append([]any{"problem", problem, "path", path}, args...)...)
		return func() {}, problem
	}
	if len(path) > maxSocketPath {
		return without(fmt.Sprintf("the path of the socket is %d bytes, longer than the %d a Unix socket allows (move data_dir to a shorter path)", len(path), maxSocketPath))
	}
	// A socket here was left behind by a hub that was killed: a live hub
	// holds the lock of the data directory, so this hub would not have
	// started (see lockDataDir).
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return without("cannot open the socket: " + err.Error())
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return without("cannot restrict the socket to the hub's user: " + err.Error())
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
	}, ""
}

// writeRunning records that this hub serves, for the update helper (#312).
func (h *Hub) writeRunning(socketProblem string) {
	if err := update.WriteRunning(h.cfg.DataDir, update.Running{Version: h.version, StartedAt: h.startedAt.UTC(), StatusSocket: socketProblem}); err != nil {
		h.log.Warn("cannot write "+update.RunningName+"; an update cannot be confirmed without the local status socket", "err", err)
	}
}
