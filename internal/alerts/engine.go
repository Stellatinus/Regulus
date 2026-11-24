package alerts

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Kind is what changed in an alert, and so what a message is about.
type Kind string

// The changes a Notifier hears about, once each.
const (
	Opened    Kind = "opened"
	Escalated Kind = "escalated" // the severity went up
	Resolved  Kind = "resolved"
	Reminder  Kind = "reminder" // still open, not acknowledged, and the rule asks for a reminder
)

// Change is one message: the alert as it is after the change.
type Change struct {
	Kind  Kind
	Alert Alert
	// Previous is the severity before an Escalated change.
	Previous Severity
	// Reason is why a Resolved alert resolved (ReasonRecovered,
	// ReasonSubjectGone, ReasonRuleOff).
	Reason string
}

// Notifier receives every change of state, once. Notify must return quickly
// (hand the message to a queue): the evaluation waits for it.
type Notifier interface {
	Notify(ctx context.Context, c Change)
}

// NotifierFunc makes a Notifier of a function.
type NotifierFunc func(ctx context.Context, c Change)

// Notify calls f.
func (f NotifierFunc) Notify(ctx context.Context, c Change) { f(ctx, c) }

const (
	// refreshEvery is how often the numbers of an alert that stays open are
	// written again (they would otherwise be written at every evaluation).
	refreshEvery = 5 * time.Minute
	// keepResolved is how long resolved alerts are kept as history.
	keepResolved = 90 * 24 * time.Hour
	pruneEvery   = time.Hour
)

// tracker is what the engine remembers, in memory, about one condition and
// subject between two evaluations: since when it has been true at all, since
// when at the critical level, and since when it has been false. A restart of
// the hub clears it: a condition then needs its duration again to open, and
// an alert that was open needs its recovery time again to resolve; the alerts
// themselves are in the database and are not announced again.
type tracker struct {
	condition                            Condition
	subject                              string
	trueSince, criticalSince, falseSince time.Time
}

// Engine evaluates the rules and keeps the alerts.
type Engine struct {
	store  store
	log    *slog.Logger
	now    func() time.Time
	notify Notifier

	mu        sync.Mutex // one evaluation at a time
	trackers  map[string]*tracker
	lastPrune time.Time
}

// New makes the engine for the hub database and writes the default rules of
// the conditions that have none. notify may be nil.
func New(ctx context.Context, db *sql.DB, log *slog.Logger, notify Notifier) (*Engine, error) {
	e := &Engine{store: store{db: db}, log: log, now: time.Now, notify: notify, trackers: map[string]*tracker{}}
	if err := e.store.seedRules(ctx, DefaultRules(), e.now()); err != nil {
		return nil, fmt.Errorf("alert rules: %w", err)
	}
	return e, nil
}

// SetNotifier replaces the notifier; the hub uses it when delivery exists.
func (e *Engine) SetNotifier(n Notifier) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.notify = n
}

// Rules returns the rule of every condition, in the order of the design.
func (e *Engine) Rules(ctx context.Context) ([]Rule, error) {
	m, err := e.store.rules(ctx)
	if err != nil {
		return nil, err
	}
	var out []Rule
	for _, c := range Conditions() {
		if r, ok := m[c]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// SetRule saves the settings of a condition (the API step of the design calls
// it). The change applies at the next evaluation.
func (e *Engine) SetRule(ctx context.Context, r Rule, by string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	return e.store.updateRule(ctx, r, by, e.now())
}

// List returns the newest alerts in the given states (all when none is given).
func (e *Engine) List(ctx context.Context, states []State, limit int) ([]Alert, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	return e.store.list(ctx, states, limit)
}

// Acknowledge marks an open alert as seen by a person. An acknowledged alert
// stays on the list and sends nothing more until it resolves.
func (e *Engine) Acknowledge(ctx context.Context, id int64, by string) (Alert, error) {
	ok, err := e.store.acknowledge(ctx, id, by, e.now())
	if err != nil {
		return Alert{}, err
	}
	a, err := e.store.get(ctx, id)
	if err != nil {
		return Alert{}, err
	}
	if !ok {
		return a, ErrNotOpen
	}
	return a, nil
}

func (e *Engine) send(ctx context.Context, c Change) {
	if e.notify != nil {
		e.notify.Notify(ctx, c)
	}
}

// Evaluate looks at the snapshot and changes the alerts: it opens the ones
// whose condition has held for its duration, raises a severity, resolves the
// ones that have been false for their recovery time, and tells the notifier
// once for each change.
func (e *Engine) Evaluate(ctx context.Context, snap Snapshot) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := snap.Now
	if now.IsZero() {
		now = e.now()
		snap.Now = now
	}
	rules, err := e.store.rules(ctx)
	if err != nil {
		return err
	}
	active, err := e.store.active(ctx)
	if err != nil {
		return err
	}
	byKey := map[string]Alert{}
	for _, a := range active {
		byKey[key(a.Condition, a.Subject)] = a
	}
	found, skipped := findings(rules, snap)
	only := map[Condition]bool{}
	for _, c := range snap.Only {
		only[c] = true
	}
	inScope := func(c Condition) bool { return len(only) == 0 || only[c] }

	// Servers that are gone take their alerts with them.
	known := map[string]bool{}
	for _, a := range snap.Agents {
		known[a.ID] = true
	}
	if len(snap.Only) == 0 {
		for k, a := range byKey {
			if a.AgentID != "" && !known[a.AgentID] {
				if err := e.resolve(ctx, a, ReasonSubjectGone, now); err != nil {
					return err
				}
				delete(byKey, k)
				delete(e.trackers, k)
			}
		}
	}

	seen := map[string]bool{}
	for _, f := range found {
		k := f.key()
		seen[k] = true
		r := rules[f.condition]
		t := e.trackers[k]
		if t == nil {
			t = &tracker{condition: f.condition, subject: f.subject}
			e.trackers[k] = t
		}
		t.falseSince = time.Time{}
		if t.trueSince.IsZero() {
			t.trueSince = now
			if !f.since.IsZero() && f.since.Before(now) {
				t.trueSince = f.since
			}
		}
		if f.level.rank() > r.Severity.rank() {
			if t.criticalSince.IsZero() {
				t.criticalSince = now
			}
		} else {
			t.criticalSince = time.Time{}
		}
		if now.Sub(t.trueSince) < r.Duration {
			continue // not for long enough yet
		}
		sev := r.Severity
		if f.level.rank() > sev.rank() && now.Sub(t.criticalSince) >= r.Duration {
			sev = f.level
		}
		a, exists := byKey[k]
		if !exists {
			na := Alert{
				Condition: f.condition, Subject: f.subject, SubjectName: f.name, AgentID: f.agentID, Severity: sev,
				Value: f.value, Args: f.args, OpenedAt: now, UpdatedAt: now, LastNotifiedAt: now,
			}
			id, err := e.store.insert(ctx, na)
			if err != nil {
				return err
			}
			na.ID, na.State = id, StateOpen
			e.send(ctx, Change{Kind: Opened, Alert: na})
			continue
		}
		raised := sev.rank() > a.Severity.rank()
		prev := a.Severity
		refresh := sev != a.Severity || now.Sub(a.UpdatedAt) >= refreshEvery
		if refresh {
			a.Severity, a.Value, a.Args, a.UpdatedAt = sev, f.value, f.args, now
		}
		// An acknowledged alert sends nothing more until it resolves, a raise
		// of the severity included.
		var msg *Change
		if a.State == StateOpen {
			switch {
			case raised:
				a.LastNotifiedAt = now
				msg = &Change{Kind: Escalated, Alert: a, Previous: prev}
			case r.Reminder > 0 && now.Sub(a.LastNotifiedAt) >= r.Reminder:
				a.LastNotifiedAt = now
				msg = &Change{Kind: Reminder, Alert: a}
			}
		}
		if refresh || msg != nil {
			if err := e.store.touch(ctx, a); err != nil {
				return err
			}
		}
		if msg != nil {
			e.send(ctx, *msg)
		}
	}

	// What is not true now: false conditions run towards their recovery time.
	for k, a := range byKey {
		if seen[k] || !inScope(a.Condition) {
			continue
		}
		if skipped(a.Condition, a.Subject) {
			continue
		}
		if r := rules[a.Condition]; !r.Enabled {
			if err := e.resolve(ctx, a, ReasonRuleOff, now); err != nil {
				return err
			}
			delete(e.trackers, k)
			continue
		}
		t := e.trackers[k]
		if t == nil {
			t = &tracker{condition: a.Condition, subject: a.Subject}
			e.trackers[k] = t
		}
		t.trueSince, t.criticalSince = time.Time{}, time.Time{}
		if t.falseSince.IsZero() {
			t.falseSince = now
		}
		if now.Sub(t.falseSince) >= rules[a.Condition].EffectiveRecovery() {
			if err := e.resolve(ctx, a, ReasonRecovered, now); err != nil {
				return err
			}
			delete(e.trackers, k)
		}
	}
	// A condition that was true for a while and is false again before it
	// opened an alert starts over.
	for k, t := range e.trackers {
		if _, hasAlert := byKey[k]; seen[k] || hasAlert || !inScope(t.condition) || skipped(t.condition, t.subject) {
			continue
		}
		delete(e.trackers, k)
	}

	if now.Sub(e.lastPrune) >= pruneEvery {
		e.lastPrune = now
		if err := e.store.pruneResolved(ctx, now.Add(-keepResolved)); err != nil {
			e.log.Warn("pruning the resolved alerts", "err", err)
		}
	}
	return nil
}

func (e *Engine) resolve(ctx context.Context, a Alert, reason string, now time.Time) error {
	if err := e.store.resolve(ctx, a.ID, reason, now); err != nil {
		return err
	}
	a.State, a.ResolveReason, a.UpdatedAt, a.LastNotifiedAt = StateResolved, reason, now, now
	a.ResolvedAt = &now
	e.send(ctx, Change{Kind: Resolved, Alert: a, Reason: reason})
	return nil
}
