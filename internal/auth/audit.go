package auth

import (
	"context"
	"strings"
	"time"
)

// What the audit log keeps. Time decides what is old; the two counts only
// bound the table against a flood, and they are separate on purpose: events
// that anybody can cause without a login (a wrong password, a wrong enrollment
// token) must not push out the history an administrator needs (a user created,
// a role changed, a second factor reset).
const (
	// AuditRetention is how long an event is kept, whatever it is.
	AuditRetention = 90 * 24 * time.Hour
	// maxUnauthenticatedAuditRows is the budget of the events anyone can cause.
	maxUnauthenticatedAuditRows = 5000
	// maxAuditRows is a backstop for all the other events, which need a login
	// or the machine; reaching it is not expected in 90 days.
	maxAuditRows = 100000
	// auditPruneEvery is how often Run prunes. An event is written without any
	// deletion, so a burst between two runs is bounded only by what the limits
	// of the login and of the enrollment let through.
	auditPruneEvery = time.Hour
)

// enrollFailedEvent is the audit event of an invalid enrollment token. It is
// recorded by the fleet package (fleet.EventEnrollFailed), which this package
// cannot import; a test in the hub package keeps the two equal.
const enrollFailedEvent = "agent_enroll_failed"

// unauthenticatedEvents are the events a stranger can cause: the login and the
// enrollment are reachable without credentials.
var unauthenticatedEvents = []string{EventLoginFailed, enrollFailedEvent}

// IsUnauthenticatedEvent reports whether event is one that anybody can cause
// without a login, and so shares a small budget in the audit log.
func IsUnauthenticatedEvent(event string) bool {
	for _, e := range unauthenticatedEvents {
		if e == event {
			return true
		}
	}
	return false
}

// pruneAudit deletes the events older than before, then the oldest events of
// each class beyond its budget. The queries read ids, which only grow, so
// "the newest N" is "the ids above the id of the N+1th newest".
func (s store) pruneAudit(ctx context.Context, before time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM audit_log WHERE at < ?`, before.UTC().Format(timeLayout)); err != nil {
		return err
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(unauthenticatedEvents)), ", ")
	args := func(limit int) []any {
		a := make([]any, 0, 2*len(unauthenticatedEvents)+1)
		for range 2 {
			for _, e := range unauthenticatedEvents {
				a = append(a, e)
			}
		}
		return append(a, limit)
	}
	for _, class := range []struct {
		in    string
		limit int
	}{
		{"IN", maxUnauthenticatedAuditRows},
		{"NOT IN", maxAuditRows},
	} {
		// The first list of marks is the outer DELETE's, the second the subquery's.
		q := `DELETE FROM audit_log WHERE event ` + class.in + ` (` + marks + `) AND id <= COALESCE(` +
			`(SELECT id FROM audit_log WHERE event ` + class.in + ` (` + marks + `) ORDER BY id DESC LIMIT 1 OFFSET ?), 0)`
		if _, err := s.db.ExecContext(ctx, q, args(class.limit)...); err != nil {
			return err
		}
	}
	return nil
}

// PruneAudit applies the retention of the audit log. Run calls it on a timer.
func (s *Service) PruneAudit(ctx context.Context) error {
	return s.store.pruneAudit(ctx, s.now().Add(-AuditRetention))
}

// Run prunes the audit log when it starts and then every hour, until ctx is
// cancelled. The hub starts it once.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(auditPruneEvery)
	defer tick.Stop()
	for {
		if err := s.PruneAudit(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("pruning the audit log", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
