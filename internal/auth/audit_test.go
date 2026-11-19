package auth

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func auditCount(t *testing.T, s *Service, where string) int {
	t.Helper()
	var n int
	if err := s.store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_log `+where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFloodOfUnauthenticatedFailuresKeepsThePrivilegedHistory(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	admin, _ := setup(t, s)
	addUser(t, s, admin, "dana", RoleViewer)
	s.audit(ctx, admin.Username, EventTwoFactorReset, "dana", "sessions ended")

	// 20,000 events that anybody can cause, from many addresses.
	for i := range 10000 {
		fctx := WithClient(ctx, Client{Remote: fmt.Sprintf("2001:db8:%x::1", i)})
		s.audit(fctx, "someone", EventLoginFailed, "", "")
		s.Record(fctx, "", enrollFailedEvent, "", "invalid token")
	}
	if got := auditCount(t, s, ""); got < 20000 {
		t.Fatalf("writing must not delete anything: %d rows", got)
	}
	if err := s.PruneAudit(ctx); err != nil {
		t.Fatal(err)
	}

	all, err := s.store.listAudit(ctx, 100000)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ event, actor, target string }{
		{EventSetup, "admin", "admin"},
		{EventUserCreated, "admin", "dana"},
		{EventTwoFactorReset, "admin", "dana"},
	} {
		if !hasEvent(all, want.event, want.actor, want.target) {
			t.Errorf("the %s event was evicted by the flood", want.event)
		}
	}
	if got := auditCount(t, s, `WHERE event IN ('login_failed', 'agent_enroll_failed')`); got != maxUnauthenticatedAuditRows {
		t.Errorf("unauthenticated events kept = %d, want %d", got, maxUnauthenticatedAuditRows)
	}
	// The newest are the ones kept.
	if newest := all[0]; newest.Event != enrollFailedEvent {
		t.Errorf("the newest event is %q", newest.Event)
	}
}

func TestAuditIsPrunedByAgeNotByCount(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return start }
	s.audit(ctx, "admin", EventUserCreated, "old", "")
	s.audit(ctx, "someone", EventLoginFailed, "", "")
	s.now = func() time.Time { return start.Add(AuditRetention - time.Hour) }
	s.audit(ctx, "admin", EventUserCreated, "recent", "")

	if err := s.PruneAudit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := auditCount(t, s, ""); got != 3 {
		t.Fatalf("nothing is older than the retention yet, %d rows", got)
	}
	s.now = func() time.Time { return start.Add(AuditRetention + time.Hour) }
	if err := s.PruneAudit(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := s.store.listAudit(ctx, 10)
	if len(all) != 1 || all[0].Target != "recent" {
		t.Fatalf("after the retention only the recent event stays: %+v", all)
	}
}

func TestRunPrunesWhenItStartsAndStopsWithItsContext(t *testing.T) {
	s := newService(t)
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return start }
	s.audit(ctx, "admin", EventUserCreated, "old", "")
	s.now = func() time.Time { return start.Add(AuditRetention + time.Hour) }

	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	deadline := time.After(5 * time.Second)
	for auditCount(t, s, "") != 0 {
		select {
		case <-deadline:
			t.Fatal("Run did not prune when it started")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop with its context")
	}
}

func TestUnauthenticatedEvents(t *testing.T) {
	for _, e := range []string{EventLoginFailed, "agent_enroll_failed"} {
		if !IsUnauthenticatedEvent(e) {
			t.Errorf("%s must share the small budget", e)
		}
	}
	for _, e := range []string{EventLogin, EventUserCreated, EventTwoFactorReset, EventPasswordFailed} {
		if IsUnauthenticatedEvent(e) {
			t.Errorf("%s needs a login and must not share the small budget", e)
		}
	}
}
