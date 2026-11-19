package hub

import (
	"testing"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/fleet"
)

// The audit log gives the events that anybody can cause a small budget of
// their own. The auth package cannot import fleet, so it spells the enrollment
// event by hand: this keeps the two the same.
func TestTheInvalidEnrollmentEventSharesTheSmallAuditBudget(t *testing.T) {
	if !auth.IsUnauthenticatedEvent(fleet.EventEnrollFailed) {
		t.Fatalf("%q is recorded by the fleet for any invalid token and must be an unauthenticated audit event", fleet.EventEnrollFailed)
	}
	if !auth.IsUnauthenticatedEvent(auth.EventLoginFailed) {
		t.Fatal("a failed login must be an unauthenticated audit event")
	}
}
