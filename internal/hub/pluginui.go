package hub

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/plugin"
)

// Codes of the plugin UI routes and the bridge. The page of a plugin gets them
// in error.code; the dashboard has a text for each (#354).
const (
	codePluginNotFound      = "plugin_not_found"
	codePluginNoUI          = "plugin_no_ui"
	codePluginUnsafeModule  = "plugin_unsafe_module"
	codePluginOpUnknown     = "plugin_op_unknown"
	codePluginNotRequested  = "plugin_permission_not_requested"
	codePluginForbidden     = "plugin_forbidden"
	codePluginBadParams     = "plugin_bad_params"
	codePluginOpUnavailable = "plugin_op_unavailable"
)

func pluginUICodes() []string {
	return []string{codePluginNotFound, codePluginNoUI, codePluginUnsafeModule, codePluginOpUnknown, codePluginNotRequested, codePluginForbidden, codePluginBadParams, codePluginOpUnavailable}
}

// pluginUI serves what the dashboard needs from the plugins that have a ui
// component: the list, the page of each, and the bridge.
type pluginUI struct {
	dir   string
	disks DiskSource
	// auth is nil in tests that exercise the handlers alone: there is then no
	// logged-in person to check.
	auth *auth.Service
}

type tabTitleJSON struct {
	EN string `json:"en"`
	IT string `json:"it"`
}

type nodeTabJSON struct {
	ID    string       `json:"id"`
	Title tabTitleJSON `json:"title"`
}

type uiPluginJSON struct {
	ID          string       `json:"id"`
	Version     string       `json:"version"`
	Module      string       `json:"module"`
	NodeTab     *nodeTabJSON `json:"node_tab,omitempty"`
	Permissions []string     `json:"permissions"`
}

// list serves GET /api/plugins/ui: the installed plugins that have a ui
// component, for any logged-in person (the bridge decides what works).
func (p pluginUI) list(w http.ResponseWriter, _ *http.Request) {
	out := []uiPluginJSON{}
	for _, u := range plugin.ListUI(p.dir) {
		m := u.Manifest
		e := uiPluginJSON{ID: m.ID, Version: m.Version, Module: m.Components.UI.Module, Permissions: []string{}}
		if t := m.Components.UI.NodeTab; t != nil {
			e.NodeTab = &nodeTabJSON{ID: t.ID, Title: tabTitleJSON{EN: t.Title.EN, IT: t.Title.TitleFor("it")}}
		}
		for _, perm := range m.Permissions {
			e.Permissions = append(e.Permissions, perm.ID)
		}
		out = append(out, e)
	}
	writeJSONBody(w, http.StatusOK, map[string][]uiPluginJSON{"plugins": out})
}

// loadModule finds the plugin and reads its module, or answers the error.
func (p pluginUI) loadModule(w http.ResponseWriter, r *http.Request) (module []byte, ok bool) {
	u, err := plugin.LoadUI(p.dir, r.PathValue("id"))
	switch {
	case errors.Is(err, plugin.ErrNoPlugin):
		writeJSONErrorCode(w, http.StatusNotFound, "no such plugin", codePluginNotFound)
		return nil, false
	case errors.Is(err, plugin.ErrNoUI):
		writeJSONErrorCode(w, http.StatusNotFound, "the plugin has no ui module", codePluginNoUI)
		return nil, false
	case err != nil:
		writeInternal(w)
		return nil, false
	}
	module, err = u.ReadModule()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONErrorCode(w, http.StatusNotFound, "the ui module is missing", codePluginNoUI)
			return nil, false
		}
		writeInternal(w)
		return nil, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return module, true
}

// page serves GET /api/plugins/{id}/ui: the page the dashboard puts in a
// sandboxed frame, with the module inlined and its own
// Content-Security-Policy (see plugin.UIShell for why it is inlined).
func (p pluginUI) page(w http.ResponseWriter, r *http.Request) {
	module, ok := p.loadModule(w, r)
	if !ok {
		return
	}
	page, csp, err := plugin.UIShell(module)
	if err != nil {
		writeJSONErrorCode(w, http.StatusUnprocessableEntity, "the ui module cannot be shown: it contains </script or <!--", codePluginUnsafeModule)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", csp)
	// Only the hub's own pages may frame this one (the headers set for every
	// response say DENY); the sandbox is also set by the frame's element.
	h.Set("X-Frame-Options", "SAMEORIGIN")
	_, _ = w.Write(page)
}

// module serves GET /api/plugins/{id}/module: the module file itself, with the
// policy of an empty origin. Nothing else of the plugin is served.
func (p pluginUI) module(w http.ResponseWriter, r *http.Request) {
	module, ok := p.loadModule(w, r)
	if !ok {
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("Content-Security-Policy", plugin.ModuleCSP)
	_, _ = w.Write(module)
}

type callRequest struct {
	Op     string          `json:"op"`
	Params json.RawMessage `json:"params"`
}

// call serves POST /api/plugins/{id}/call, the read door of a plugin's page.
// The checks, in order: the plugin is installed and has a ui; the operation is
// in the core's allowlist; the plugin's manifest requests the permission the
// operation needs; the logged-in person has the core permission behind it.
// All must hold (the narrower wins). Nothing here writes.
func (p pluginUI) call(w http.ResponseWriter, r *http.Request) {
	u, err := plugin.LoadUI(p.dir, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, plugin.ErrNoPlugin) || errors.Is(err, plugin.ErrNoUI) {
			writeJSONErrorCode(w, http.StatusNotFound, "no such plugin", codePluginNotFound)
			return
		}
		writeInternal(w)
		return
	}
	var req callRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSONErrorCode(w, http.StatusBadRequest, "the request is not valid JSON", codeInvalidJSON)
		return
	}
	op, ok := plugin.LookupOp(req.Op)
	if !ok {
		writeJSONErrorCode(w, http.StatusNotFound, "unknown operation", codePluginOpUnknown)
		return
	}
	if perm := op.PluginPermission(u.Manifest.ID); !u.Manifest.Requests(perm) {
		writeJSONErrorCode(w, http.StatusForbidden, "the plugin does not request the permission "+perm, codePluginNotRequested)
		return
	}
	if p.auth != nil {
		if user, ok := auth.UserFrom(r.Context()); !ok || !user.Can(op.Core) {
			writeJSONErrorCode(w, http.StatusForbidden, "you do not have the permission "+op.Core, codePluginForbidden)
			return
		}
	}
	if p.disks == nil {
		writeJSONErrorCode(w, http.StatusNotFound, "this data is not available", codePluginOpUnavailable)
		return
	}
	switch op.Name {
	case "inventory.disks":
		var params struct {
			Node string `json:"node"`
		}
		if !decodeParams(w, req.Params, &params) {
			return
		}
		if params.Node == "" {
			badParams(w)
			return
		}
		body, err := agentDisksBody(r.Context(), p.disks, params.Node)
		if err != nil {
			writeInternal(w)
			return
		}
		writeJSONBody(w, http.StatusOK, body)
	case "inventory.disk_moves":
		var params struct {
			Limit int `json:"limit"`
		}
		if !decodeParams(w, req.Params, &params) {
			return
		}
		limit := params.Limit
		if limit == 0 {
			limit = 100
		}
		if limit < 1 || limit > 1000 {
			badParams(w)
			return
		}
		body, err := diskMovesBody(r.Context(), p.disks, limit)
		if err != nil {
			writeInternal(w)
			return
		}
		writeJSONBody(w, http.StatusOK, body)
	default:
		// An operation in plugin.Ops that this switch does not run is a bug:
		// a test lists them together.
		writeJSONErrorCode(w, http.StatusNotFound, "unknown operation", codePluginOpUnknown)
	}
}

// decodeParams reads params into v (absent or null is the zero value, unknown
// fields are refused). It answers 400 and returns false when it cannot.
func decodeParams(w http.ResponseWriter, raw json.RawMessage, v any) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		badParams(w)
		return false
	}
	return true
}

func badParams(w http.ResponseWriter) {
	writeJSONErrorCode(w, http.StatusBadRequest, "the parameters of the operation are not valid", codePluginBadParams)
}
