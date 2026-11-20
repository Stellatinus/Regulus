package hub

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/storage"
	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

// skipUnlessReadPool skips the tests about the SQLite read pool where there
// is none.
func skipUnlessReadPool(t testing.TB) {
	if storagetest.Postgres() {
		t.Skip("about the SQLite read pool; PostgreSQL has a pool of several connections")
	}
	if os.Getenv(EnvOneConnection) != "" {
		t.Skip("the read pool is off in this run")
	}
}

// While a write transaction holds the SQLite writer (a slow audit write, a
// migration-sized update), the dashboard's reads and a connecting agent do
// not wait for it (#219).
func TestReadsAndAgentsDoNotWaitForTheWriter(t *testing.T) {
	skipUnlessReadPool(t)
	h := startRemoteHub(t)
	admin := newAPI(t, h, auth.RoleAdmin)
	creds := enrollNew(t, h, "reader")
	admin.do("GET", "/api/agents", nil) // records the session's use now, so the reads below need no write

	tx, err := h.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO audit_log (at, actor, event, target, remote, detail) VALUES ('2026-01-01T00:00:00.000000000Z', 'x', 'slow_write', '', '', '')`); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/auth/me", "/api/agents?all=true", "/api/audit", "/api/users", "/api/roles", "/api/layout", "/api/account/sessions"} {
		done := make(chan int, 1)
		go func() { done <- admin.do("GET", path, nil).Code }()
		select {
		case code := <-done:
			if code != http.StatusOK && code != http.StatusNotFound {
				t.Errorf("GET %s = %d", path, code)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("GET %s waited for the writer", path)
		}
	}

	runAgent(t, h, creds)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := h.agentInfo(creds.AgentID); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the remote agent could not connect while the writer was busy")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

// BenchmarkReadLatencyDuringAuditWrites measures what #219 is about: 20
// dashboards checking their session and listing 200 servers while audit
// events are written in transactions that each hold the writer for 20 ms.
// It reports the median and the 99th percentile of a read, with the read pool
// and with one shared connection:
//
//	go test -run '^$' -bench ReadLatency ./internal/hub/
func BenchmarkReadLatencyDuringAuditWrites(b *testing.B) {
	if storagetest.Postgres() {
		b.Skip("SQLite only")
	}
	for _, mode := range []struct {
		name  string
		conns int
	}{{"read-pool", 0}, {"one-connection", -1}} {
		b.Run(mode.name, func(b *testing.B) {
			benchReads(b, mode.conns)
		})
	}
}

func benchReads(b *testing.B, readConns int) {
	old := tuneStorage
	tuneStorage = func(c *storage.Config) { c.ReadConns = readConns }
	defer func() { tuneStorage = old }()
	h := benchHub(b)
	ctx := context.Background()

	_, session, _, err := h.auth.Setup(ctx, h.auth.SetupToken(), "admin", "correct horse battery")
	if err != nil {
		b.Fatal(err)
	}
	for i := range 200 {
		if _, err := h.db.Exec(`INSERT INTO agents (id, name, hostname, created_at, enrolled_from, cert_serial) VALUES (?, '', ?, '2026-01-01T00:00:00Z', '', ?)`,
			"agent-"+strconv.Itoa(i), "host-"+strconv.Itoa(i), "serial-"+strconv.Itoa(i)); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := h.auth.Authenticate(ctx, session); err != nil { // the once-a-minute touch, out of the way
		b.Fatal(err)
	}

	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() { // the slow audit writes
		defer writer.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			tx, err := h.db.BeginTx(ctx, nil)
			if err != nil {
				return
			}
			_, _ = tx.Exec(`INSERT INTO audit_log (at, actor, event, target, remote, detail) VALUES ('2026-01-01T00:00:00.000000000Z', 'bench', 'slow_write', '', '', '')`)
			time.Sleep(20 * time.Millisecond)
			_ = tx.Commit()
		}
	}()

	var (
		mu        sync.Mutex
		latencies []time.Duration
	)
	b.ResetTimer()
	for range b.N { // one round: each of the 20 dashboards reads once
		var round sync.WaitGroup
		for range 20 {
			round.Add(1)
			go func() {
				defer round.Done()
				start := time.Now()
				if _, err := h.auth.Authenticate(ctx, session); err != nil {
					b.Error(err)
					return
				}
				if _, err := h.fleet.Agents(ctx); err != nil {
					b.Error(err)
					return
				}
				d := time.Since(start)
				mu.Lock()
				latencies = append(latencies, d)
				mu.Unlock()
			}()
		}
		round.Wait()
		time.Sleep(5 * time.Millisecond) // dashboards poll, they do not spin
	}
	b.StopTimer()
	close(stop)
	writer.Wait()

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	if n := len(latencies); n > 0 {
		b.ReportMetric(float64(latencies[n/2].Microseconds()), "p50-µs")
		b.ReportMetric(float64(latencies[n*99/100].Microseconds()), "p99-µs")
	}
}

// benchHub makes a hub that is not serving, closed when the benchmark ends.
func benchHub(b *testing.B) *Hub {
	b.Helper()
	cfg := config.Default()
	cfg.DataDir = b.TempDir()
	cfg.Updates.Check = false
	cfg.BuiltinAgent = false
	h, err := New(context.Background(), cfg, quiet(), fstest.MapFS{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = h.Close() })
	return h
}

// TestRemainingPlainReadsDoNotWaitForTheWriter is #345: the update settings,
// the two-factor state, the security policy and the saved listener are plain
// reads of committed rows, so they use the read pool and do not queue behind
// a write transaction.
func TestRemainingPlainReadsDoNotWaitForTheWriter(t *testing.T) {
	skipUnlessReadPool(t)
	h := startRemoteHub(t)
	admin := newAPI(t, h, auth.RoleAdmin)
	admin.do("GET", "/api/agents", nil) // records the session's use now, so the reads below need no write

	tx, err := h.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO audit_log (at, actor, event, target, remote, detail) VALUES ('2026-01-01T00:00:00.000000000Z', 'x', 'slow_write', '', '', '')`); err != nil {
		t.Fatal(err)
	}

	within := func(name string, f func()) {
		t.Helper()
		done := make(chan struct{})
		go func() { f(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s waited for the writer", name)
		}
	}
	within("the update settings", func() {
		if _, err := h.updates.Settings(context.Background()); err != nil {
			t.Error(err)
		}
	})
	within("the saved listener", func() {
		if _, err := h.savedAgentListen(context.Background()); err != nil {
			t.Error(err)
		}
	})
	for _, path := range []string{"/api/account/two-factor", "/api/settings/security", "/api/updates"} {
		within("GET "+path, func() {
			if code := admin.do("GET", path, nil).Code; code != http.StatusOK {
				t.Errorf("GET %s = %d", path, code)
			}
		})
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
