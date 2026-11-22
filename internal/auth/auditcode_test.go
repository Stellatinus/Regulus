package auth

import (
	"context"
	"reflect"
	"testing"
)

func TestAuditEntriesKeepTheirCodeAndArguments(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	admin, _ := setup(t, s) // the first admin: code first_admin
	addUser(t, s, admin, "dana", RoleViewer)
	s.RecordCode(ctx, "hub", "update_agent_updated", "a1", "v1 to v2", CodeFromTo, "from", "v1", "to", "v2")
	s.audit(ctx, "hub", "something", "", "free text") // no code

	all, err := s.store.listAudit(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	byEvent := map[string]AuditEntry{}
	for _, e := range all {
		byEvent[e.Event] = e
	}
	if e := byEvent[EventSetup]; e.Code != CodeFirstAdmin || e.Detail != "first admin" || e.Args != nil {
		t.Errorf("setup entry = %+v", e)
	}
	if e := byEvent[EventUserCreated]; e.Code != CodeRole || !reflect.DeepEqual(e.Args, map[string]string{"role": string(RoleViewer)}) {
		t.Errorf("user created entry = %+v", e)
	}
	if e := byEvent["update_agent_updated"]; e.Code != CodeFromTo || e.Args["from"] != "v1" || e.Args["to"] != "v2" || e.Detail != "v1 to v2" {
		t.Errorf("coded entry = %+v", e)
	}
	if e := byEvent["something"]; e.Code != "" || e.Args != nil || e.Detail != "free text" {
		t.Errorf("uncoded entry = %+v", e)
	}
}

// A row written before the columns existed has empty code and arguments, and
// keeps its text.
func TestAuditRowWithoutCodeStillReads(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	if _, err := s.store.db.ExecContext(ctx, `INSERT INTO audit_log (at, actor, event, target, remote, detail) VALUES ('2020-01-01T00:00:00Z', 'a', 'login', '', '', 'old text')`); err != nil {
		t.Fatal(err)
	}
	all, err := s.store.listAudit(ctx, 10)
	if err != nil || len(all) == 0 {
		t.Fatalf("list = %v, %v", all, err)
	}
	if all[0].Code != "" || all[0].Detail != "old text" {
		t.Errorf("entry = %+v", all[0])
	}
}
