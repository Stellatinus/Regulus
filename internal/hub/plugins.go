// plugins.go runs the hub's own plugins (plugins_dir, role hub) beside its
// servers and serves GET /api/plugins: the state of the hub's plugins and of
// the plugins each connected agent reports with its metrics, and announces
// their changes on the live stream ("plugins.changed"). See
// docs/plugins/runtime.md.

package hub

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"github.com/CodexCircle/Regulus/internal/plugin"
)

// HubPlugins is the state of the plugins the hub runs itself.
type HubPlugins struct {
	// Enabled is true when plugins_dir is set.
	Enabled bool
	// Problem is why no plugin runs although Enabled (the directory cannot be
	// read); empty otherwise.
	Problem string
	Plugins []plugin.Status
}

// PluginSource reports the hub's plugins. *Hub implements it.
type PluginSource interface {
	HubPlugins() HubPlugins
}

// pluginRunner is the supervisor of the hub's plugins, nil while plugins are
// off.
type pluginRunner struct {
	sup *plugin.Supervisor

	mu      sync.Mutex
	problem string
}

// WithPluginSupervisor lets a test change the supervisor of the hub's plugins
// (shorter pauses and checks, a short socket directory) before it starts.
func WithPluginSupervisor(fn func(*plugin.Supervisor)) Option {
	return func(h *Hub) { h.pluginTune = fn }
}

// startPlugins runs the plugins of plugins_dir with the role hub until ctx
// ends. Each plugin is its own process with a clean environment (the
// database password and every other variable of the hub stay out), and
// nothing a plugin does reaches the hub: the supervisor only logs and counts.
// The returned function stops them and waits until they are gone.
func (h *Hub) startPlugins(ctx context.Context) (stop func()) {
	if h.cfg.PluginsDir == "" {
		return func() {}
	}
	sup := &plugin.Supervisor{
		Dir:         h.cfg.PluginsDir,
		RunDir:      filepath.Join(h.cfg.DataDir, "plugins-run"),
		DataDir:     filepath.Join(h.cfg.DataDir, "plugins-data"),
		Role:        plugin.RoleHub,
		CoreVersion: h.version,
		Log:         h.log.With("component", "plugins"),
		OnChange:    func() { h.agents.events.publish(hubPluginsChanged) },
	}
	if h.pluginTune != nil {
		h.pluginTune(sup)
	}
	r := &pluginRunner{sup: sup}
	h.plugins.Store(r)
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := sup.Run(ctx); err != nil {
			h.log.Error("plugins are not running", "dir", h.cfg.PluginsDir, "err", err)
			r.mu.Lock()
			r.problem = err.Error()
			r.mu.Unlock()
			h.agents.events.publish(hubPluginsChanged)
		}
	}()
	h.log.Info("plugins enabled", "dir", h.cfg.PluginsDir)
	return func() { cancel(); <-done }
}

// HubPlugins returns the state of the hub's own plugins.
func (h *Hub) HubPlugins() HubPlugins {
	out := HubPlugins{Enabled: h.cfg.PluginsDir != "", Plugins: []plugin.Status{}}
	r, _ := h.plugins.Load().(*pluginRunner)
	if r == nil {
		return out
	}
	out.Plugins = r.sup.Status()
	r.mu.Lock()
	out.Problem = r.problem
	r.mu.Unlock()
	return out
}

// pluginsEvent is "plugins.changed" on GET /api/stream: the plugins of the hub
// (source "hub") or of one agent (source "agent", with its id) changed state,
// restart count or last error, or appeared. It carries no state: the client
// reads GET /api/plugins again, which applies the same permission and names
// the agents. A client also reads it again after a snapshot (a new or
// resumed stream may have missed events) and when an agent connects or
// disconnects (its plugins come and go with it).
type pluginsEvent struct {
	Type   string `json:"type"` // "plugins.changed"
	Source string `json:"source"`
	ID     string `json:"id,omitempty"`
}

var hubPluginsChanged = pluginsEvent{Type: "plugins.changed", Source: "hub"}

// samePlugins reports whether two reports of an agent's plugins show the same
// thing, so that only a change is pushed. Since is left out: it moves only
// with the state.
func samePlugins(aOn bool, a []*agentv1.PluginStatus, bOn bool, b []*agentv1.PluginStatus) bool {
	if aOn != bOn || len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.GetId() != y.GetId() || x.GetVersion() != y.GetVersion() || x.GetState() != y.GetState() ||
			x.GetRestarts() != y.GetRestarts() || x.GetLastError() != y.GetLastError() {
			return false
		}
	}
	return true
}

// pluginJSON is one plugin in GET /api/plugins, the same shape for the hub and
// for agents.
type pluginJSON struct {
	ID        string     `json:"id"`
	Version   string     `json:"version"`
	State     string     `json:"state"`
	Restarts  int        `json:"restarts"`
	LastError string     `json:"last_error,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
}

type hubPluginsJSON struct {
	Enabled bool         `json:"enabled"`
	Problem string       `json:"problem,omitempty"`
	Plugins []pluginJSON `json:"plugins"`
}

type agentPluginsJSON struct {
	ID      string       `json:"id"`
	Name    string       `json:"name,omitempty"`
	Plugins []pluginJSON `json:"plugins"`
}

type pluginsJSON struct {
	Hub    hubPluginsJSON     `json:"hub"`
	Agents []agentPluginsJSON `json:"agents"`
}

func sinceJSON(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

// pluginsHandler serves GET /api/plugins. Agents are listed when they are
// connected and report a plugins directory; an agent without one, the
// built-in agent and agents older than the report are left out.
func pluginsHandler(src PluginSource, agents AgentSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := pluginsJSON{Hub: hubPluginsJSON{Plugins: []pluginJSON{}}, Agents: []agentPluginsJSON{}}
		if src != nil {
			hp := src.HubPlugins()
			out.Hub.Enabled, out.Hub.Problem = hp.Enabled, hp.Problem
			for _, st := range hp.Plugins {
				out.Hub.Plugins = append(out.Hub.Plugins, pluginJSON{
					ID: st.ID, Version: st.Version, State: st.State, Restarts: st.Restarts,
					LastError: st.LastError, Since: sinceJSON(st.Since),
				})
			}
		}
		if agents != nil {
			names := map[string]string{}
			if reg, ok := agents.(registrySource); ok {
				enrolled, err := reg.Registry(r.Context())
				if err != nil {
					writeInternal(w)
					return
				}
				for _, e := range enrolled {
					names[e.ID] = e.Name
				}
			}
			for _, a := range agents.Agents() {
				if !a.PluginsEnabled || a.UpdateOnly {
					continue
				}
				ap := agentPluginsJSON{ID: a.ID, Name: names[a.ID], Plugins: []pluginJSON{}}
				for _, p := range a.Plugins {
					j := pluginJSON{ID: p.GetId(), Version: p.GetVersion(), State: p.GetState(), Restarts: int(p.GetRestarts()), LastError: p.GetLastError()}
					if p.GetSince() != nil {
						j.Since = sinceJSON(p.GetSince().AsTime())
					}
					ap.Plugins = append(ap.Plugins, j)
				}
				out.Agents = append(out.Agents, ap)
			}
		}
		writeJSONBody(w, http.StatusOK, out)
	}
}
