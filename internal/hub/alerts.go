package hub

import (
	"cmp"
	"context"
	"time"

	"github.com/CodexCircle/Regulus/internal/alerts"
)

// The alerts of docs/alerts.md (#404): internal/alerts decides, this file
// gives it what the hub knows (the registry, the latest reports, the
// certificate dates, the rollout and the active-hub lock) and runs it on a
// timer in the hub that holds the lock, with the other timers.
//
// What is not here yet: delivery (email and webhooks, internal/notify), the API
// and the Alerts page. Until then a change of state is a line in the log.

// alertEvery is how often the rules are evaluated.
const alertEvery = 30 * time.Second

// alertLog is the notifier until delivery exists: one log line for each change
// of state, which is the message the design promises.
func (h *Hub) alertLog() alerts.Notifier {
	return alerts.NotifierFunc(func(_ context.Context, c alerts.Change) {
		a := c.Alert
		level := h.log.Warn
		if c.Kind == alerts.Resolved {
			level = h.log.Info
		}
		level("alert "+string(c.Kind), "condition", a.Condition, "subject", a.Subject, "name", a.SubjectName,
			"severity", a.Severity, "id", a.ID, "reason", c.Reason)
	})
}

// Alerts returns the alert engine, for the API step of the design.
func (h *Hub) Alerts() *alerts.Engine { return h.alerts }

// alertSnapshot collects what the rules read.
func (h *Hub) alertSnapshot(ctx context.Context, now time.Time) (alerts.Snapshot, error) {
	snap := alerts.Snapshot{Now: now, HubLockLost: h.demoted.Load()}

	connected := map[string]AgentInfo{}
	for _, a := range h.agents.agents() {
		connected[a.ID] = a
	}
	reg, err := h.fleet.Agents(ctx)
	if err != nil {
		return alerts.Snapshot{}, err
	}
	fresh := max(3*h.agents.metricsInterval, 30*time.Second)
	add := func(id, name string, last time.Time) {
		a := alerts.Agent{ID: id, Name: name, LastSeen: last}
		if info, ok := connected[id]; ok {
			a.Connected = true
			h.fillUsage(&a, info, now, fresh)
		}
		snap.Agents = append(snap.Agents, a)
	}
	for _, r := range reg {
		if r.Revoked {
			continue
		}
		add(r.ID, cmp.Or(r.Name, r.Hostname, r.ID), r.LastSeen)
		snap.Certificates = appendCert(snap.Certificates, r.ID, cmp.Or(r.Name, r.Hostname, r.ID), r.CertNotAfter)
	}
	// Servers that are connected and not enrolled: the built-in agent.
	for id, info := range connected {
		if id == BuiltinAgentID {
			name := info.System.GetHostname()
			add(id, cmp.Or(name, id), now)
		}
	}

	certs, err := h.Certificates(ctx)
	if err != nil {
		return alerts.Snapshot{}, err
	}
	snap.Certificates = append(snap.Certificates, alerts.Certificate{Subject: "ca", Name: "CA", NotAfter: certs.CA.NotAfter})
	if certs.Listener != nil {
		snap.Certificates = append(snap.Certificates, alerts.Certificate{Subject: "listener", Name: "agent listener", NotAfter: certs.Listener.NotAfter})
	}

	// A rollout that failed for a server that is still behind. A server that
	// runs the target now (updated by hand, or by a later rollout) is not.
	st, _ := h.RolloutStatus(ctx)
	for _, ra := range st.Agents {
		if ra.Status != AgentFailed {
			continue
		}
		if info, ok := connected[ra.ID]; ok && info.Version == st.Target {
			continue
		}
		name := cmp.Or(ra.Hostname, ra.ID)
		snap.UpdateFailures = append(snap.UpdateFailures, alerts.UpdateFailure{AgentID: ra.ID, Name: name, Version: st.Target, Message: ra.Message})
	}
	return snap, nil
}

func appendCert(certs []alerts.Certificate, id, name string, end time.Time) []alerts.Certificate {
	if end.IsZero() {
		return certs // the hub does not know it yet
	}
	return append(certs, alerts.Certificate{Subject: "agent:" + id, AgentID: id, Name: name, NotAfter: end})
}

// fillUsage takes the numbers of the latest reports of a connected server. A
// server whose last report is old, or that sends none (an update-only
// session), is not reporting and its disks, memory and CPU are not evaluated.
func (h *Hub) fillUsage(a *alerts.Agent, info AgentInfo, now time.Time, fresh time.Duration) {
	if info.UpdateOnly {
		return
	}
	samples, _ := h.agents.metricsHistory(info.ID)
	if len(samples) == 0 || now.Sub(samples[len(samples)-1].At) > fresh {
		return
	}
	from := max(0, len(samples)-2) // the CPU share needs the report before
	points := toPoints(samples[from:])
	p := points[len(points)-1]
	a.Reporting = true
	a.CPUPercent = p.CPUPercent
	if p.MemoryTotalBytes > 0 {
		used := 100 * float64(p.MemoryTotalBytes-min(p.MemoryAvailableBytes, p.MemoryTotalBytes)) / float64(p.MemoryTotalBytes)
		a.MemoryPercent = &used
	}
	for _, d := range p.Disks {
		a.Disks = append(a.Disks, alerts.Disk{Mount: d.MountPoint, TotalBytes: d.TotalBytes, UsedBytes: d.UsedBytes})
	}
}

// evaluateAlerts runs the rules once.
func (h *Hub) evaluateAlerts(ctx context.Context) {
	snap, err := h.alertSnapshot(ctx, time.Now())
	if err != nil {
		if ctx.Err() == nil {
			h.log.Warn("collecting what the alerts need", "err", err)
		}
		return
	}
	if err := h.alerts.Evaluate(ctx, snap); err != nil && ctx.Err() == nil {
		h.log.Warn("evaluating the alerts", "err", err)
	}
}

// runAlerts evaluates the rules every alertEvery until ctx ends.
func (h *Hub) runAlerts(ctx context.Context) {
	tick := time.NewTicker(h.alertEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			h.evaluateAlerts(ctx)
		}
	}
}

// alertLockLost says at once that another hub holds the lock: the other
// conditions are not evaluated here any more, and this one must not wait for a
// timer that was just stopped.
func (h *Hub) alertLockLost() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snap := alerts.Snapshot{Now: time.Now(), Only: []alerts.Condition{alerts.HubLockLost}, HubLockLost: true}
	if err := h.alerts.Evaluate(ctx, snap); err != nil {
		h.log.Warn("evaluating the alerts", "err", err)
	}
}
