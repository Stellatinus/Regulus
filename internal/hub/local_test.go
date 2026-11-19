package hub

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/localstatus"
)

func TestLocalStatusSocket(t *testing.T) {
	h := startHubWithConfig(t, config.Default())
	waitFor(t, "built-in agent", func() bool {
		a := h.Agents()
		return len(a) == 1 && !a[0].LastHeartbeat.IsZero()
	})

	info, err := os.Stat(localstatus.SocketPath(h.cfg.DataDir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket mode = %o, want 600", perm)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := localstatus.Fetch(ctx, h.cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != "9.9.9" || st.Addr == "" || !st.BuiltinAgent || st.StartedAt.IsZero() {
		t.Errorf("status = %+v", st)
	}
	if len(st.Agents) != 1 || st.Agents[0].ID != BuiltinAgentID || st.Agents[0].Status != statusConnected {
		t.Errorf("agents = %+v", st.Agents)
	}
	// Remote agents are off: the soonest certificate is the CA, 20 years away.
	if st.CertSoonest == nil || st.CertSoonest.Kind != CertKindCA || !st.CertSoonest.NotAfter.Equal(h.fleet.CA().NotAfter()) || len(st.CertWarnings) != 0 {
		t.Errorf("certificates = %+v, %+v", st.CertSoonest, st.CertWarnings)
	}
	// No admin yet: the console shows the one-time token.
	if st.SetupToken == "" || st.SetupToken != h.auth.SetupToken() {
		t.Errorf("setup token = %q", st.SetupToken)
	}
}

func TestLocalStatusGoneWithTheHub(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := localstatus.Fetch(ctx, t.TempDir()); err == nil {
		t.Error("a hub answered where none runs")
	}
}

// A database that does not answer (#313): the status must still come back
// well within the client's timeout, and name the problem once the watcher
// recorded it. Holding the only connection of the SQLite pool makes every
// ping and query block, like PostgreSQL behind a firewall that drops packets.
func TestLocalStatusAnswersWhileTheDatabaseBlocks(t *testing.T) {
	h := startHubWithConfig(t, config.Default())
	if h.cfg.Storage().IsPostgres() {
		t.Skip("blocks the single connection of the SQLite pool")
	}
	waitFor(t, "status socket", func() bool {
		_, err := localstatus.Fetch(t.Context(), h.cfg.DataDir)
		return err == nil
	})
	conn, err := h.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	fetch := func() localstatus.Status {
		t.Helper()
		start := time.Now()
		st, err := localstatus.Fetch(t.Context(), h.cfg.DataDir)
		if err != nil {
			t.Fatalf("the status did not answer: %v", err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("the status took %v, want under 1s", d)
		}
		return st
	}
	if st := fetch(); st.Version != "9.9.9" || st.CertSoonest != nil {
		t.Errorf("status with a blocked database = %+v", st)
	}

	// The watcher's ping blocks for its own timeout and records the problem;
	// the status reports it without asking the database again.
	h.checkDatabase(t.Context())
	st := fetch()
	if st.DatabaseProblem == "" || !strings.Contains(st.DatabaseProblem, "timed out") {
		t.Errorf("database problem = %q", st.DatabaseProblem)
	}

	_ = conn.Close()
	h.checkDatabase(t.Context())
	if st := fetch(); st.DatabaseProblem != "" || st.CertSoonest == nil {
		t.Errorf("after the database answers again: problem %q, certificates %+v", st.DatabaseProblem, st.CertSoonest)
	}
}
