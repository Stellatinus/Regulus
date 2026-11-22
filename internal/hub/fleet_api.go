package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/fleet"
	"github.com/CodexCircle/Regulus/internal/update"
)

// ErrAgentsOff is returned when remote agents are not enabled.
var ErrAgentsOff = errors.New("remote agents are off: turn them on in Servers, or set agent_listen in the configuration")

// Enrollment is what an admin needs to add a server: the token, valid once,
// and what the agent must trust.
type Enrollment struct {
	Token         string
	Name          string
	ExpiresAt     time.Time
	CAFingerprint string
	AgentPort     int
}

// FleetControl is what the HTTP API needs to enroll and revoke remote agents.
// *Hub implements it.
type FleetControl interface {
	CreateEnrollment(ctx context.Context, by, name string, ttl time.Duration) (Enrollment, error)
	PendingEnrollments(ctx context.Context) ([]fleet.PendingToken, error)
	RevokeEnrollment(ctx context.Context, by, id string) error
	RevokeAgent(ctx context.Context, by, id string) error
	RenameAgent(ctx context.Context, by, id, name string) error
	AgentListener(ctx context.Context) (AgentListener, error)
	EnableAgents(ctx context.Context, by string, port int) (AgentListener, error)
}

// CreateEnrollment makes a one-time enrollment token, valid for ttl (the
// default of fleet.TokenTTL when zero).
func (h *Hub) CreateEnrollment(ctx context.Context, by, name string, ttl time.Duration) (Enrollment, error) {
	addr := h.AgentAddr()
	if addr == "" {
		return Enrollment{}, ErrAgentsOff
	}
	token, expires, err := h.fleet.CreateToken(ctx, by, name, ttl)
	if err != nil {
		return Enrollment{}, err
	}
	_, port, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(port)
	return Enrollment{
		Token:         token,
		Name:          name,
		ExpiresAt:     expires,
		CAFingerprint: h.fleet.CA().Fingerprint(),
		AgentPort:     n,
	}, nil
}

// PendingEnrollments lists the enrollment tokens that can still be used.
func (h *Hub) PendingEnrollments(ctx context.Context) ([]fleet.PendingToken, error) {
	return h.fleet.PendingTokens(ctx)
}

// RevokeEnrollment cancels a pending enrollment token.
func (h *Hub) RevokeEnrollment(ctx context.Context, by, id string) error {
	return h.fleet.RevokeToken(ctx, by, id)
}

// RevokeAgent stops an agent from connecting again and drops its connection.
func (h *Hub) RevokeAgent(ctx context.Context, by, id string) error {
	if err := h.fleet.Revoke(ctx, by, id); err != nil {
		return err
	}
	h.agents.disconnect(id)
	return nil
}

// RenameAgent sets the name shown for an enrolled agent.
func (h *Hub) RenameAgent(ctx context.Context, by, id, name string) error {
	return h.fleet.Rename(ctx, by, id, name)
}

// Registry lists the enrolled agents, connected or not.
func (h *Hub) Registry(ctx context.Context) ([]fleet.Agent, error) {
	return h.fleet.Agents(ctx)
}

type fleetHandlers struct {
	fleet   FleetControl
	version string
}

func (f fleetHandlers) createToken(w http.ResponseWriter, r *http.Request) {
	if f.fleet == nil {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Name string `json:"name"`
		// TTL is how long the token can be used, a duration such as "15m" or
		// "24h"; empty is the default of one hour.
		TTL string `json:"ttl"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	var ttl time.Duration
	if req.TTL != "" {
		d, err := time.ParseDuration(req.TTL)
		if err != nil {
			writeJSONErrorCode(w, http.StatusBadRequest, `ttl must be a duration such as "15m" or "24h"`, codeInvalidTTLFormat)
			return
		}
		ttl = d
	}
	by, _ := auth.UserFrom(r.Context())
	grant, err := f.fleet.CreateEnrollment(r.Context(), by.Username, req.Name, ttl)
	var nameErr *fleet.NameError
	var ttlErr *fleet.TTLError
	switch {
	case errors.Is(err, ErrAgentsOff):
		writeJSONErr(w, http.StatusConflict, err)
	case errors.As(err, &nameErr), errors.As(err, &ttlErr):
		writeJSONErr(w, http.StatusBadRequest, err)
	case err != nil:
		writeInternal(w)
	default:
		body := map[string]any{
			"token":          grant.Token,
			"name":           grant.Name,
			"expires_at":     grant.ExpiresAt.UTC(),
			"ca_fingerprint": grant.CAFingerprint,
			"agent_port":     grant.AgentPort,
		}
		// A release hub names its version, so the new agent installs the same
		// one; a development build has no release to point to.
		if v, ok := update.ParseBuildVersion(f.version); ok {
			body["hub_version"] = v.String()
		}
		writeJSONBody(w, http.StatusCreated, body)
	}
}

// listTokens answers with the enrollment tokens that can still be used. The
// secret of a token was shown once, when it was made, and is not kept.
func (f fleetHandlers) listTokens(w http.ResponseWriter, r *http.Request) {
	if f.fleet == nil {
		http.NotFound(w, r)
		return
	}
	pending, err := f.fleet.PendingEnrollments(r.Context())
	if err != nil {
		writeInternal(w)
		return
	}
	type item struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		CreatedBy string    `json:"created_by"`
		CreatedAt time.Time `json:"created_at"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	out := make([]item, 0, len(pending))
	for _, p := range pending {
		out = append(out, item{p.ID, p.Name, p.CreatedBy, p.CreatedAt.UTC(), p.ExpiresAt.UTC()})
	}
	writeJSONBody(w, http.StatusOK, out)
}

func (f fleetHandlers) revokeToken(w http.ResponseWriter, r *http.Request) {
	if f.fleet == nil {
		http.NotFound(w, r)
		return
	}
	var req struct{}
	if !decodeBody(w, r, &req) {
		return
	}
	by, _ := auth.UserFrom(r.Context())
	err := f.fleet.RevokeEnrollment(r.Context(), by.Username, r.PathValue("id"))
	switch {
	case errors.Is(err, fleet.ErrTokenNotFound):
		writeJSONErrorCode(w, http.StatusNotFound, "no pending enrollment token with this id", codeTokenNotFound)
	case err != nil:
		writeInternal(w)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (f fleetHandlers) revoke(w http.ResponseWriter, r *http.Request) {
	if f.fleet == nil {
		http.NotFound(w, r)
		return
	}
	var req struct{}
	if !decodeBody(w, r, &req) {
		return
	}
	by, _ := auth.UserFrom(r.Context())
	err := f.fleet.RevokeAgent(r.Context(), by.Username, r.PathValue("id"))
	switch {
	case errors.Is(err, fleet.ErrNotFound):
		writeJSONErrorCode(w, http.StatusNotFound, "no enrolled agent with this id", codeAgentNotEnrolled)
	case err != nil:
		writeInternal(w)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// decodeBody reads a small JSON request body; it answers 400 and returns false
// when the body is not valid.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSONErrorCode(w, http.StatusBadRequest, "invalid JSON body", codeInvalidJSON)
		return false
	}
	return true
}

func (f fleetHandlers) rename(w http.ResponseWriter, r *http.Request) {
	if f.fleet == nil {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	by, _ := auth.UserFrom(r.Context())
	err := f.fleet.RenameAgent(r.Context(), by.Username, r.PathValue("id"), req.Name)
	var nameErr *fleet.NameError
	switch {
	case errors.Is(err, fleet.ErrNotFound):
		writeJSONErrorCode(w, http.StatusNotFound, "no enrolled agent with this id", codeAgentNotEnrolled)
	case errors.As(err, &nameErr):
		writeJSONErr(w, http.StatusBadRequest, err)
	case err != nil:
		writeInternal(w)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
