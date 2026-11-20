package hub

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

// blockAgentsTable makes every write to the agents table wait until the
// returned function is called: on SQLite by holding the one connection that
// writes, on PostgreSQL with a table lock.
func blockAgentsTable(t *testing.T, db *sql.DB) (release func()) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if storagetest.Postgres() {
		if _, err := tx.ExecContext(ctx, `LOCK TABLE agents IN ACCESS EXCLUSIVE MODE`); err != nil {
			t.Fatal(err)
		}
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			_ = tx.Rollback()
			_ = conn.Close()
		})
	}
	t.Cleanup(release)
	return release
}

// With the database blocked, an agent still gets its Welcome (#218); what the
// hub records about it is written once the database answers again.
func TestAgentConnectsWhileTheDatabaseIsBlocked(t *testing.T) {
	h := startHub(t)
	release := blockAgentsTable(t, h.db)

	start := time.Now()
	welcomed := make(chan struct{})
	go func() {
		defer close(welcomed)
		register(t, h, "blocked-db")
	}()
	select {
	case <-welcomed:
	case <-time.After(3 * time.Second):
		t.Fatal("the handshake waited for the database")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the handshake took %s with the database blocked", d)
	}
	release()
	waitFor(t, "the agent to be listed", func() bool { _, ok := h.agentInfo("blocked-db"); return ok })
}

// While an agent stays connected, last_seen_at moves forward on its own, so
// after a crash of the hub or of the machine it is at most one flush old
// (#218).
func TestLastSeenMovesWhileTheAgentIsConnected(t *testing.T) {
	cfg := config.Default()
	cfg.BuiltinAgent = false
	cfg.AgentListen = "127.0.0.1:0"
	h := startHubWithConfig(t, cfg, withSeenFlush(100*time.Millisecond))
	waitFor(t, "the agent listener", func() bool { return h.AgentAddr() != "" })
	creds := enrollNew(t, h, "steady")
	runAgent(t, h, creds)
	waitFor(t, "the agent to connect", func() bool { _, ok := h.agentInfo(creds.AgentID); return ok })

	lastSeen := func() time.Time {
		list, err := h.fleet.Agents(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range list {
			if a.ID == creds.AgentID {
				return a.LastSeen
			}
		}
		t.Fatal("the agent is not in the registry")
		return time.Time{}
	}
	connectedAt := time.Now()
	waitFor(t, "last_seen_at to move past the connection", func() bool {
		return lastSeen().After(connectedAt.Add(300 * time.Millisecond))
	})
	if age := time.Since(lastSeen()); age > time.Second {
		t.Errorf("last_seen_at is %s old with the agent connected", age)
	}
}

// The recorder keeps one pending write per agent (the latest), runs them in
// order with a timeout, and writes the connected agents once more on close;
// nothing is written after close.
func TestSeenRecorderCoalescesAndFlushesOnClose(t *testing.T) {
	var (
		mu     sync.Mutex
		writes []string
	)
	gate := make(chan struct{})
	connected := []AgentInfo{{ID: "c"}}
	r := newSeenRecorder(slog.New(slog.NewTextHandler(io.Discard, nil)),
		func() []AgentInfo { return connected },
		func(info AgentInfo, at time.Time) seenWrite {
			return func(ctx context.Context) error {
				if info.ID == "slow" {
					select {
					case <-gate:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				mu.Lock()
				writes = append(writes, info.ID+"@"+info.Version)
				mu.Unlock()
				return nil
			}
		})
	r.every = time.Hour
	r.timeout = 200 * time.Millisecond
	r.start()

	r.seen(AgentInfo{ID: "slow"}, time.Now()) // times out: the writer must not hang on it
	r.seen(AgentInfo{ID: "a", Version: "1"}, time.Now())
	r.seen(AgentInfo{ID: "a", Version: "2"}, time.Now()) // replaces the pending "1" (unless already written)
	r.put("cert/a", func(context.Context) error { return errors.New("refused") })
	r.close()
	r.seen(AgentInfo{ID: "late"}, time.Now())
	close(gate)

	mu.Lock()
	defer mu.Unlock()
	has := map[string]bool{}
	for _, w := range writes {
		has[w] = true
	}
	if !has["a@2"] || !has["c@"] || has["late@"] || has["slow@"] {
		t.Errorf("writes = %v; want a@2 and c@ (the final flush), not slow (timed out) or late (after close)", writes)
	}
}
