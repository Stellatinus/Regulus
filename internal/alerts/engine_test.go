package alerts

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

var t0 = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

// harness is an engine on a real database with a clock the test moves, and a
// notifier that keeps every message.
type harness struct {
	t    *testing.T
	e    *Engine
	now  time.Time
	msgs []Change
	snap Snapshot
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := storagetest.Open(t, t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	h := &harness{t: t, now: t0}
	e, err := New(context.Background(), db, slog.New(slog.NewTextHandler(io.Discard, nil)), NotifierFunc(func(_ context.Context, c Change) { h.msgs = append(h.msgs, c) }))
	if err != nil {
		t.Fatal(err)
	}
	h.e = e
	return h
}

// step moves the clock and evaluates the snapshot as it is now.
func (h *harness) step(d time.Duration) {
	h.t.Helper()
	h.now = h.now.Add(d)
	h.snap.Now = h.now
	if err := h.e.Evaluate(context.Background(), h.snap); err != nil {
		h.t.Fatal(err)
	}
}

// kinds returns the kinds of the messages since the last call.
func (h *harness) kinds() []Kind {
	var out []Kind
	for _, m := range h.msgs {
		out = append(out, m.Kind)
	}
	h.msgs = nil
	return out
}

func (h *harness) expect(want ...Kind) {
	h.t.Helper()
	got := h.kinds()
	if len(got) != len(want) {
		h.t.Fatalf("messages = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			h.t.Fatalf("messages = %v, want %v", got, want)
		}
	}
}

func (h *harness) alerts(states ...State) []Alert {
	h.t.Helper()
	l, err := h.e.List(context.Background(), states, 100)
	if err != nil {
		h.t.Fatal(err)
	}
	return l
}

func (h *harness) setRule(c Condition, mutate func(*Rule)) {
	h.t.Helper()
	rules, _ := h.e.Rules(context.Background())
	for _, r := range rules {
		if r.Condition == c {
			mutate(&r)
			if err := h.e.SetRule(context.Background(), r, "test"); err != nil {
				h.t.Fatal(err)
			}
			return
		}
	}
	h.t.Fatalf("no rule %s", c)
}

func f64(v float64) *float64 { return &v }

func web(connected bool) Agent {
	return Agent{ID: "web1", Name: "web1", Connected: connected, Reporting: connected, LastSeen: t0}
}

func (h *harness) diskAt(percent float64) {
	a := web(true)
	a.Disks = []Disk{{Mount: "/var", TotalBytes: 1000, UsedBytes: uint64(percent * 10)}}
	h.snap.Agents = []Agent{a}
}

func TestDefaultsAreQuiet(t *testing.T) {
	h := newHarness(t)
	rules, err := h.e.Rules(context.Background())
	if err != nil || len(rules) != len(Conditions()) {
		t.Fatalf("rules = %v, %v", rules, err)
	}
	for _, r := range rules {
		want := r.Condition != MemoryHigh && r.Condition != CPUHigh
		if r.Enabled != want || r.Reminder != 0 {
			t.Errorf("%s: enabled = %v, reminder = %v", r.Condition, r.Enabled, r.Reminder)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", r.Condition, err)
		}
	}
	// A busy server is not a broken one: CPU and memory at 99% say nothing.
	a := web(true)
	a.CPUPercent, a.MemoryPercent = f64(99), f64(99)
	h.snap.Agents = []Agent{a}
	for range 100 {
		h.step(time.Minute)
	}
	h.expect()
}

func TestAnOfflineServerOpensAfterFiveMinutesAndResolvesAfterItRecovered(t *testing.T) {
	h := newHarness(t)
	h.snap.Agents = []Agent{web(false)} // last seen at t0
	h.step(4 * time.Minute)
	h.expect()
	h.step(time.Minute) // five minutes since it was last seen
	h.expect(Opened)
	a := h.alerts(StateOpen)
	if len(a) != 1 || a[0].Condition != AgentOffline || a[0].Severity != Critical || a[0].Subject != "agent:web1" {
		t.Fatalf("alerts = %+v", a)
	}
	// One message per change of state, not per sample.
	for range 20 {
		h.step(30 * time.Second)
	}
	h.expect()
	// It comes back; the alert resolves after the recovery time (5 minutes).
	h.snap.Agents = []Agent{web(true)}
	h.step(time.Minute) // the first evaluation that finds it well
	h.step(3 * time.Minute)
	h.expect()
	h.step(2*time.Minute + time.Second) // five minutes since
	h.expect(Resolved)
	if got := h.alerts(StateResolved); len(got) != 1 || got[0].ResolveReason != ReasonRecovered || got[0].ResolvedAt == nil {
		t.Errorf("resolved = %+v", got)
	}
	if len(h.alerts(StateOpen, StateAcknowledged)) != 0 {
		t.Error("an alert is still active")
	}
}

func TestAFlappingConditionSendsNothing(t *testing.T) {
	h := newHarness(t)
	for range 30 {
		h.diskAt(95)
		h.step(5 * time.Minute) // shorter than the 10 minutes it must hold
		h.diskAt(50)
		h.step(time.Minute)
	}
	h.expect()
	if len(h.alerts()) != 0 {
		t.Errorf("alerts = %+v", h.alerts())
	}
}

func TestDiskFullOpensEscalatesAndResolvesWithOneMessageEach(t *testing.T) {
	h := newHarness(t)
	h.diskAt(91)
	h.step(0)
	h.step(9 * time.Minute)
	h.expect()
	h.step(time.Minute)
	h.expect(Opened)
	if a := h.alerts(StateOpen)[0]; a.Severity != Warning || a.Subject != "agent:web1:disk:/var" || a.Value == nil || *a.Value != 91 {
		t.Fatalf("alert = %+v", a)
	}
	// 97% is critical, once it has held for the duration as well.
	h.diskAt(98)
	h.step(time.Minute) // critical from here on
	h.step(9 * time.Minute)
	h.expect()
	h.step(time.Minute + time.Second)
	h.expect(Escalated)
	if m := h.alerts(StateOpen)[0]; m.Severity != Critical {
		t.Errorf("severity = %s", m.Severity)
	}
	// Back to 92: still open (a drop in severity sends nothing), then it clears.
	h.diskAt(92)
	for range 10 {
		h.step(time.Minute)
	}
	h.expect()
	h.diskAt(40)
	h.step(time.Minute)
	h.step(9 * time.Minute)
	h.expect()
	h.step(time.Minute + time.Second)
	h.expect(Resolved)
}

func TestAConditionThatComesBackDuringRecoveryDoesNotResolve(t *testing.T) {
	h := newHarness(t)
	h.diskAt(95)
	h.step(0)
	h.step(10 * time.Minute)
	h.expect(Opened)
	h.diskAt(10)
	h.step(5 * time.Minute)
	h.diskAt(95) // back before the 10 minutes of recovery
	h.step(time.Minute)
	h.diskAt(10)
	h.step(time.Minute) // a new recovery window starts
	h.step(8 * time.Minute)
	h.expect()
	h.step(2 * time.Minute)
	h.expect(Resolved)
}

func TestMemoryAndCPUFireOnlyWhenTurnedOn(t *testing.T) {
	h := newHarness(t)
	a := web(true)
	a.CPUPercent, a.MemoryPercent = f64(97), f64(96)
	h.snap.Agents = []Agent{a}
	h.step(0)
	h.step(time.Hour)
	h.expect()
	h.setRule(CPUHigh, func(r *Rule) { r.Enabled, r.Threshold, r.Duration = true, 95, 30*time.Minute })
	h.step(0)
	h.step(29 * time.Minute)
	h.expect()
	h.step(time.Minute)
	h.expect(Opened)
	if a := h.alerts(StateOpen); len(a) != 1 || a[0].Condition != CPUHigh {
		t.Errorf("alerts = %+v", a)
	}
	// A server whose CPU could not be read is not idle and not busy: nothing.
	b := web(true)
	b.CPUPercent = nil
	h.snap.Agents = []Agent{b}
	h.step(time.Minute)
	h.expect()
	// Turning the rule off resolves what it raised, with that reason.
	h.setRule(CPUHigh, func(r *Rule) { r.Enabled = false })
	h.step(time.Second)
	h.expect(Resolved)
	if got := h.alerts(StateResolved); got[0].ResolveReason != ReasonRuleOff {
		t.Errorf("reason = %s", got[0].ResolveReason)
	}
}

func TestCertificatesWarnAtOnceAndEscalateInTheLastWeek(t *testing.T) {
	h := newHarness(t)
	h.snap.Certificates = []Certificate{{Subject: "ca", Name: "CA", NotAfter: t0.Add(31 * 24 * time.Hour)}}
	h.step(0)
	h.expect()
	h.step(25 * time.Hour) // 29 days left
	h.expect(Opened)
	if a := h.alerts(StateOpen)[0]; a.Severity != Warning || a.Subject != "cert:ca" {
		t.Fatalf("alert = %+v", a)
	}
	h.step(22 * 24 * time.Hour) // 6 days left
	h.expect(Escalated)
	// Renewed: resolves after the recovery time (one minute at least).
	h.snap.Certificates = []Certificate{{Subject: "ca", Name: "CA", NotAfter: t0.Add(400 * 24 * time.Hour)}}
	h.step(30 * time.Second)
	h.expect()
	h.step(time.Minute)
	h.expect(Resolved)
}

func TestACertificateOfARevokedServerResolvesWithTheServer(t *testing.T) {
	h := newHarness(t)
	h.snap.Agents = []Agent{web(true)}
	h.snap.Certificates = []Certificate{{Subject: "agent:web1", AgentID: "web1", Name: "web1", NotAfter: t0.Add(10 * 24 * time.Hour)}}
	h.step(0)
	h.expect(Opened)
	h.snap.Agents, h.snap.Certificates = nil, nil // revoked
	h.step(time.Second)
	h.expect(Resolved)
	if got := h.alerts(StateResolved); len(got) != 1 || got[0].ResolveReason != ReasonSubjectGone {
		t.Errorf("resolved = %+v", got)
	}
}

func TestUpdateFailureAndHubLock(t *testing.T) {
	h := newHarness(t)
	h.snap.Agents = []Agent{web(true)}
	h.snap.UpdateFailures = []UpdateFailure{{AgentID: "web1", Name: "web1", Version: "0.4.0", Message: "the installer rolled the update back"}}
	h.step(0)
	h.expect(Opened)
	if a := h.alerts(StateOpen)[0]; a.Condition != UpdateFailed || a.Severity != Warning || a.Args["version"] != "0.4.0" {
		t.Fatalf("alert = %+v", a)
	}
	h.snap.UpdateFailures = nil // it was updated since
	h.step(time.Minute)
	h.step(time.Minute)
	h.expect(Resolved)

	// Losing the lock is said at once, by an evaluation of that condition only.
	only := Snapshot{Now: h.now, Only: []Condition{HubLockLost}, HubLockLost: true}
	if err := h.e.Evaluate(context.Background(), only); err != nil {
		t.Fatal(err)
	}
	h.expect(Opened)
	if a := h.alerts(StateOpen); len(a) != 1 || a[0].Condition != HubLockLost || a[0].Severity != Critical {
		t.Fatalf("alerts = %+v", a)
	}
	// The other conditions are left alone by that evaluation.
	only.Now = h.now.Add(10 * time.Minute)
	only.HubLockLost = false
	if err := h.e.Evaluate(context.Background(), only); err != nil {
		t.Fatal(err)
	}
	h.expect()
	only.Now = h.now.Add(12 * time.Minute)
	if err := h.e.Evaluate(context.Background(), only); err != nil {
		t.Fatal(err)
	}
	h.expect(Resolved)
}

func TestAServerThatIsNotReportingIsNotEvaluatedForItsDisks(t *testing.T) {
	h := newHarness(t)
	h.diskAt(95)
	h.step(0)
	h.step(10 * time.Minute)
	h.expect(Opened)
	// It goes offline: only agent_offline speaks, and the disk alert neither
	// resolves nor changes while nobody can read the disk.
	h.snap.Agents = []Agent{{ID: "web1", Name: "web1", LastSeen: h.now}}
	for range 12 {
		h.step(time.Minute)
	}
	h.expect(Opened) // agent_offline, once, after five minutes
	var conds []Condition
	for _, a := range h.alerts(StateOpen) {
		conds = append(conds, a.Condition)
	}
	if len(conds) != 2 {
		t.Fatalf("active alerts = %v", conds)
	}
	if len(h.alerts(StateResolved)) != 0 {
		t.Error("the disk alert resolved while the server did not report")
	}
}

func TestAcknowledgedAlertsSendNothingMoreExceptTheirEnd(t *testing.T) {
	h := newHarness(t)
	h.setRule(DiskFull, func(r *Rule) { r.Reminder = time.Hour })
	h.diskAt(95)
	h.step(0)
	h.step(10 * time.Minute)
	h.expect(Opened)
	// Not acknowledged: a reminder every hour.
	h.step(59 * time.Minute)
	h.expect()
	h.step(time.Minute)
	h.expect(Reminder)
	a := h.alerts(StateOpen)[0]
	got, err := h.e.Acknowledge(context.Background(), a.ID, "gina")
	if err != nil || got.State != StateAcknowledged || got.AcknowledgedBy != "gina" || got.AcknowledgedAt == nil {
		t.Fatalf("acknowledge = %+v, %v", got, err)
	}
	if _, err := h.e.Acknowledge(context.Background(), a.ID, "gina"); !errors.Is(err, ErrNotOpen) {
		t.Errorf("a second acknowledge: %v", err)
	}
	if _, err := h.e.Acknowledge(context.Background(), 9999, "gina"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown alert: %v", err)
	}
	h.diskAt(99) // worse, and nothing is said
	for range 5 {
		h.step(time.Hour)
	}
	h.expect()
	if s := h.alerts(StateAcknowledged); len(s) != 1 || s[0].Severity != Critical {
		t.Fatalf("acknowledged = %+v", s)
	}
	// It still resolves, and that is announced.
	h.diskAt(10)
	h.step(time.Minute)
	h.step(11 * time.Minute)
	h.expect(Resolved)
}

func TestARestartDoesNotAnnounceOpenAlertsAgain(t *testing.T) {
	h := newHarness(t)
	h.diskAt(95)
	h.step(0)
	h.step(10 * time.Minute)
	h.expect(Opened)

	// A new engine on the same database: the alert is in the table.
	h.e.mu.Lock()
	db := h.e.store.db
	h.e.mu.Unlock()
	e2, err := New(context.Background(), db, slog.New(slog.NewTextHandler(io.Discard, nil)), NotifierFunc(func(_ context.Context, c Change) { h.msgs = append(h.msgs, c) }))
	if err != nil {
		t.Fatal(err)
	}
	h.e = e2
	h.step(time.Minute)
	h.step(30 * time.Minute)
	h.expect() // still true: nothing new
	if len(h.alerts(StateOpen)) != 1 {
		t.Fatal("the alert is gone")
	}
	// After the restart a condition that is false needs its recovery time again.
	h.diskAt(10)
	h.step(time.Minute)
	h.step(9 * time.Minute)
	h.expect()
	h.step(2 * time.Minute)
	h.expect(Resolved)
	// The default rules are written once and not over what was set.
	h.setRule(DiskFull, func(r *Rule) { r.Threshold = 80 })
	e3, err := New(context.Background(), db, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := e3.Rules(context.Background())
	for _, r := range rules {
		if r.Condition == DiskFull && r.Threshold != 80 {
			t.Errorf("the rule was overwritten: %+v", r)
		}
	}
}

func TestResolvedAlertsAreKeptAsHistoryAndPrunedAfterNinetyDays(t *testing.T) {
	h := newHarness(t)
	h.snap.UpdateFailures = []UpdateFailure{{AgentID: "web1", Name: "web1", Version: "1"}}
	h.snap.Agents = []Agent{web(true)}
	h.step(0)
	h.snap.UpdateFailures = nil
	h.step(time.Minute)
	h.step(time.Minute)
	h.expect(Opened, Resolved)
	if len(h.alerts(StateResolved)) != 1 {
		t.Fatal("no history")
	}
	h.step(91 * 24 * time.Hour)
	if len(h.alerts()) != 0 {
		t.Errorf("history after 91 days: %+v", h.alerts())
	}
	// And the same condition may open a new alert for the same subject.
	h.snap.UpdateFailures = []UpdateFailure{{AgentID: "web1", Name: "web1", Version: "2"}}
	h.step(time.Second)
	h.expect(Opened)
}

func TestRuleValidation(t *testing.T) {
	for name, r := range map[string]Rule{
		"unknown":          {Condition: "nope", Severity: Warning},
		"severity":         {Condition: DiskFull, Threshold: 90, Severity: "loud"},
		"percent over 100": {Condition: DiskFull, Threshold: 101, Severity: Warning},
		"critical below":   {Condition: DiskFull, Threshold: 90, CriticalThreshold: 80, Severity: Warning},
		"negative":         {Condition: AgentOffline, Duration: -time.Second, Severity: Critical},
		"days":             {Condition: CertificateEnding, Threshold: 7, CriticalThreshold: 30, Severity: Warning},
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	h := newHarness(t)
	if err := h.e.SetRule(context.Background(), Rule{Condition: DiskFull, Threshold: 500, Severity: Warning}, "x"); err == nil {
		t.Error("SetRule saved an invalid rule")
	}
}

func TestRecoveryIsNeverShorterThanAMinute(t *testing.T) {
	if got := (Rule{}).EffectiveRecovery(); got != MinRecovery {
		t.Errorf("zero rule: %v", got)
	}
	if got := (Rule{Duration: 10 * time.Minute}).EffectiveRecovery(); got != 10*time.Minute {
		t.Errorf("same as the duration: %v", got)
	}
	if got := (Rule{Duration: 10 * time.Minute, Recovery: 2 * time.Minute}).EffectiveRecovery(); got != 2*time.Minute {
		t.Errorf("own recovery: %v", got)
	}
}
