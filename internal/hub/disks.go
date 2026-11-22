package hub

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/CodexCircle/Regulus/internal/diskid"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
)

// The physical disks an agent reports are matched to a known identity here
// (#148): the identity key comes from internal/diskid, the rows are in the
// disks table, and a disk reported by a different server than last time is a
// move, recorded in disk_moves. The records are written off the path of the
// agent's stream, by the same writer as the agents' presence (seen.go).
//
// Two connected servers that report the same disk at the same time (a cloned
// disk, or an agent that lies) are a conflict, not moves back and forth: the
// disk stays with the server that had it, the second one is named in
// contested_by and no move is written (#382). The conflict ends when either
// server stops reporting the disk or goes away; the hub then asks the other
// to report again, so that a real move is recorded as one.
//
// What this does not do: keep a history of a disk's other changes (the event
// log is #150).

const (
	// maxMoves is how many moves are kept; an agent that reports made-up
	// serial numbers cannot grow the table without limit.
	maxMoves = 5000
	// maxDisksPerAgent is how many disks of one report are recorded.
	maxDisksPerAgent = 256
)

// DiskRecord is one disk as the hub knows it.
type DiskRecord struct {
	// Key is the identity of the disk; for a disk that is not identifiable it
	// is a per-server slot key, and says nothing about the drive.
	Key          string `json:"key"`
	Kind         string `json:"kind"`
	Identifiable bool   `json:"identifiable"`
	Conflict     bool   `json:"conflict"`
	Reason       string `json:"reason,omitempty"`
	// Agent is the server that last reported the disk. Contested is true
	// while another connected server reports the same disk too (ContestedBy
	// names it): a cloned disk, an agent that lies, or a move that has not
	// settled yet. It is not a move, and none is recorded for it (#382).
	Contested   bool   `json:"contested"`
	ContestedBy string `json:"contested_by,omitempty"`
	Agent       string `json:"agent"`
	Device      string `json:"device"`
	Vendor      string `json:"vendor"`
	Model       string `json:"model"`
	Serial      string `json:"serial"`
	WWN         string `json:"wwn"`
	Transport   string `json:"transport"`
	SizeBytes   uint64 `json:"size_bytes"`
	Virtual     bool   `json:"virtual"`
	// Present is false for a disk the server stopped reporting (removed, or
	// moved: then it is present on another server).
	Present   bool      `json:"present"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// DiskMove is a disk that was reported by another server than the one before.
type DiskMove struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Key    string    `json:"key"`
	Model  string    `json:"model"`
	Serial string    `json:"serial"`
	From   string    `json:"from"`
	To     string    `json:"to"`
}

// DiskSource provides the disk data served by the HTTP API. *Hub implements it.
type DiskSource interface {
	AgentDisks(ctx context.Context, agentID string) ([]DiskRecord, error)
	DiskMoves(ctx context.Context, limit int) ([]DiskMove, error)
}

// disksSignature changes when the report of the disks changes, so that an
// unchanged report (every ten seconds) writes nothing.
func disksSignature(disks []*agentv1.PhysicalDisk) string {
	b, _ := json.Marshal(disks)
	return string(b)
}

// recordDisks matches the disks one agent reported and stores them. A move is
// recorded when a disk with an identity was last reported by another server.
func (h *Hub) recordDisks(ctx context.Context, agentID string, reported []*agentv1.PhysicalDisk, at time.Time) error {
	if len(reported) > maxDisksPerAgent {
		reported = reported[:maxDisksPerAgent]
	}
	disks := make([]diskid.Disk, len(reported))
	for i, d := range reported {
		disks[i] = diskid.Disk{
			Device: d.GetDevice(), Vendor: d.GetVendor(), Model: d.GetModel(), Serial: d.GetSerial(),
			WWN: d.GetWwn(), WWNKind: d.GetWwnKind(), Transport: d.GetTransport(), Virtual: d.GetVirtual(),
		}
	}
	ids := diskid.Identify(disks)
	stamp := at.UTC().Format(time.RFC3339)

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	// This server no longer contests what it does not report now; the loop
	// below contests again what it still reports.
	if _, err := tx.ExecContext(ctx, `UPDATE disks SET contested_by = '' WHERE contested_by = ?`, agentID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE disks SET present = 0 WHERE agent_id = ?`, agentID); err != nil {
		return err
	}
	for i, d := range reported {
		id := ids[i]
		key := id.Key
		if key == "" {
			key = "slot:" + agentID + ":" + d.GetDevice()
		}
		var prevAgent string
		var prevPresent int
		err := tx.QueryRowContext(ctx, `SELECT agent_id, present FROM disks WHERE identity_key = ?`, key).Scan(&prevAgent, &prevPresent)
		known := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if known && id.Identifiable() && prevAgent != agentID && prevPresent == 1 && h.agents.connected(prevAgent) {
			// Both servers are connected and both report the disk: it cannot
			// be in two places. Keep it where it was and flag it.
			if _, err := tx.ExecContext(ctx, `UPDATE disks SET contested_by = ? WHERE identity_key = ?`, agentID, key); err != nil {
				return err
			}
			continue
		}
		if known && id.Identifiable() && prevAgent != agentID {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO disk_moves (at, identity_key, from_agent, to_agent) VALUES (?, ?, ?, ?)`,
				stamp, key, prevAgent, agentID); err != nil {
				return err
			}
		}
		size := d.GetSizeBytes()
		if size > 1<<62 {
			size = 1 << 62
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO disks (identity_key, kind, identifiable, conflict, reason, agent_id, device, vendor, model, serial, wwn, transport, size_bytes, is_virtual, present, first_seen, last_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
			ON CONFLICT (identity_key) DO UPDATE SET
				kind = excluded.kind, identifiable = excluded.identifiable, conflict = excluded.conflict, reason = excluded.reason,
				agent_id = excluded.agent_id, device = excluded.device, vendor = excluded.vendor, model = excluded.model,
				serial = excluded.serial, wwn = excluded.wwn, transport = excluded.transport, size_bytes = excluded.size_bytes,
				is_virtual = excluded.is_virtual, present = 1, last_seen = excluded.last_seen,
				contested_by = CASE WHEN disks.agent_id = excluded.agent_id THEN disks.contested_by ELSE '' END`,
			key, string(id.Kind), b2i(id.Identifiable()), b2i(id.Conflict), id.Reason, agentID,
			d.GetDevice(), d.GetVendor(), d.GetModel(), d.GetSerial(), d.GetWwn(), d.GetTransport(), int64(size), b2i(d.GetVirtual()), stamp, stamp)
		if err != nil {
			return err
		}
	}
	// A disk this server held, no longer reports, and another server was
	// contesting: that server has it now. Let it report again so that the move
	// is recorded (its report is otherwise written only when it changes).
	rows, err := tx.QueryContext(ctx, `SELECT identity_key, contested_by FROM disks WHERE agent_id = ? AND present = 0 AND contested_by <> ''`, agentID)
	if err != nil {
		return err
	}
	var released []string
	var recheck []string
	for rows.Next() {
		var k, other string
		if err := rows.Scan(&k, &other); err != nil {
			_ = rows.Close()
			return err
		}
		released = append(released, k)
		recheck = append(recheck, other)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, k := range released {
		if _, err := tx.ExecContext(ctx, `UPDATE disks SET contested_by = '' WHERE identity_key = ?`, k); err != nil {
			return err
		}
	}
	// A disk that cannot be identified is only tracked where it is: when its
	// slot is gone, so is the record.
	if _, err := tx.ExecContext(ctx, `DELETE FROM disks WHERE agent_id = ? AND identifiable = 0 AND present = 0`, agentID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM disk_moves WHERE id <= (SELECT MAX(id) FROM disk_moves) - ?`, maxMoves); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, other := range recheck {
		h.agents.recheckDisks(other)
	}
	return nil
}

// releaseContested is called when a server goes away: the disks it held that
// another server was contesting are that server's now, and it is asked to
// report again so that the move is recorded.
func (h *Hub) releaseContested(ctx context.Context, agentID string) error {
	rows, err := h.db.QueryContext(ctx, `SELECT DISTINCT contested_by FROM disks WHERE agent_id = ? AND contested_by <> ''`, agentID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var others []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			return err
		}
		others = append(others, o)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, o := range others {
		h.agents.recheckDisks(o)
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// AgentDisks lists the disks last reported by one server, then the ones it no
// longer reports, each in the order of its device name.
func (h *Hub) AgentDisks(ctx context.Context, agentID string) ([]DiskRecord, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT identity_key, kind, identifiable, conflict, reason, agent_id, device, vendor, model, serial, wwn, transport, size_bytes, is_virtual, present, first_seen, last_seen, contested_by
		FROM disks WHERE agent_id = ? OR contested_by = ? ORDER BY present DESC, device, identity_key`, agentID, agentID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []DiskRecord{}
	for rows.Next() {
		var r DiskRecord
		var ident, conflict, virtual, present, size int64
		var first, last string
		if err := rows.Scan(&r.Key, &r.Kind, &ident, &conflict, &r.Reason, &r.Agent, &r.Device, &r.Vendor, &r.Model, &r.Serial, &r.WWN, &r.Transport, &size, &virtual, &present, &first, &last, &r.ContestedBy); err != nil {
			return nil, err
		}
		// Contested only while both servers are still connected: when one of
		// them has gone, the flag is a leftover that the next report settles.
		r.Contested = r.ContestedBy != "" && h.agents.connected(r.Agent) && h.agents.connected(r.ContestedBy)
		if !r.Contested {
			r.ContestedBy = ""
		}
		r.Identifiable, r.Conflict, r.Virtual, r.Present = ident == 1, conflict == 1, virtual == 1, present == 1
		r.SizeBytes = uint64(size)
		r.FirstSeen, _ = time.Parse(time.RFC3339, first)
		r.LastSeen, _ = time.Parse(time.RFC3339, last)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DiskMoves lists the newest moves first, at most limit of them.
func (h *Hub) DiskMoves(ctx context.Context, limit int) ([]DiskMove, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT m.id, m.at, m.identity_key, COALESCE(d.model, ''), COALESCE(d.serial, ''), m.from_agent, m.to_agent
		FROM disk_moves m LEFT JOIN disks d ON d.identity_key = m.identity_key
		ORDER BY m.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []DiskMove{}
	for rows.Next() {
		var m DiskMove
		var at string
		if err := rows.Scan(&m.ID, &at, &m.Key, &m.Model, &m.Serial, &m.From, &m.To); err != nil {
			return nil, err
		}
		m.At, _ = time.Parse(time.RFC3339, at)
		out = append(out, m)
	}
	return out, rows.Err()
}

type disksJSON struct {
	Agent string       `json:"agent"`
	Disks []DiskRecord `json:"disks"`
}

// agentDisksHandler serves GET /api/agents/{id}/disks. The serial numbers
// identify hardware, so the route needs inventory.read, not servers.read.
func agentDisksHandler(src DiskSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if src == nil {
			http.NotFound(w, r)
			return
		}
		id := r.PathValue("id")
		disks, err := src.AgentDisks(r.Context(), id)
		if err != nil {
			http.Error(w, "could not read the disks", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(disksJSON{Agent: id, Disks: disks})
	}
}

// diskMovesHandler serves GET /api/disks/moves?limit=N (default 100, at most 1000).
func diskMovesHandler(src DiskSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if src == nil {
			http.NotFound(w, r)
			return
		}
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 1000 {
				http.Error(w, "limit must be an integer from 1 to 1000", http.StatusBadRequest)
				return
			}
			limit = n
		}
		moves, err := src.DiskMoves(r.Context(), limit)
		if err != nil {
			http.Error(w, "could not read the moves", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(struct {
			Moves []DiskMove `json:"moves"`
		}{moves})
	}
}
