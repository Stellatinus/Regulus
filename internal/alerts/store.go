package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// State of an alert.
type State string

// The states of an alert.
const (
	StateOpen         State = "open"
	StateAcknowledged State = "acknowledged"
	StateResolved     State = "resolved"
)

// Why an alert resolved.
const (
	ReasonRecovered   = "recovered"
	ReasonSubjectGone = "subject_gone"
	ReasonRuleOff     = "rule_disabled"
)

// Alert is the record of a rule being true for a subject.
type Alert struct {
	ID          int64             `json:"id"`
	Condition   Condition         `json:"condition"`
	Subject     string            `json:"subject"`
	SubjectName string            `json:"subject_name"`
	AgentID     string            `json:"agent_id,omitempty"`
	State       State             `json:"state"`
	Severity    Severity          `json:"severity"`
	Value       *float64          `json:"value"`
	Args        map[string]string `json:"args,omitempty"`
	OpenedAt    time.Time         `json:"opened_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	// LastNotifiedAt is when the last message about the alert was sent.
	LastNotifiedAt time.Time  `json:"last_notified_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
	AcknowledgedBy string     `json:"acknowledged_by,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	ResolveReason  string     `json:"resolve_reason,omitempty"`
}

// Errors of the engine.
var (
	ErrNotFound = errors.New("alert not found")
	// ErrNotOpen is the answer to acknowledging an alert that is not open.
	ErrNotOpen = errors.New("the alert is not open")
)

// timeLayout is a fixed-width UTC format, so that timestamps sort as text.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

func ts(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(timeLayout, s)
	return t
}

func parseNull(n sql.NullString) *time.Time {
	if !n.Valid {
		return nil
	}
	t := parseTime(n.String)
	return &t
}

type store struct{ db *sql.DB }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

const ruleColumns = `condition, enabled, threshold, critical_threshold, duration_seconds, recovery_seconds, reminder_seconds, severity`

// seedRules writes the default of every condition that has no row.
func (s store) seedRules(ctx context.Context, rules []Rule, now time.Time) error {
	for _, r := range rules {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO alert_rules (`+ruleColumns+`, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (condition) DO NOTHING`,
			string(r.Condition), b2i(r.Enabled), r.Threshold, r.CriticalThreshold, int64(r.Duration/time.Second), int64(r.Recovery/time.Second),
			int64(r.Reminder/time.Second), string(r.Severity), ts(now)); err != nil {
			return err
		}
	}
	return nil
}

func (s store) rules(ctx context.Context) (map[Condition]Rule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+ruleColumns+` FROM alert_rules`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[Condition]Rule{}
	for rows.Next() {
		var (
			r                        Rule
			c, sev                   string
			enabled, dur, rec, remnd int64
		)
		if err := rows.Scan(&c, &enabled, &r.Threshold, &r.CriticalThreshold, &dur, &rec, &remnd, &sev); err != nil {
			return nil, err
		}
		r.Condition, r.Enabled, r.Severity = Condition(c), enabled != 0, Severity(sev)
		r.Duration, r.Recovery, r.Reminder = time.Duration(dur)*time.Second, time.Duration(rec)*time.Second, time.Duration(remnd)*time.Second
		out[r.Condition] = r
	}
	return out, rows.Err()
}

func (s store) updateRule(ctx context.Context, r Rule, by string, now time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE alert_rules SET enabled = ?, threshold = ?, critical_threshold = ?, duration_seconds = ?, recovery_seconds = ?,
		 reminder_seconds = ?, severity = ?, updated_at = ?, updated_by = ? WHERE condition = ?`,
		b2i(r.Enabled), r.Threshold, r.CriticalThreshold, int64(r.Duration/time.Second), int64(r.Recovery/time.Second),
		int64(r.Reminder/time.Second), string(r.Severity), ts(now), by, string(r.Condition))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const alertColumns = `id, condition, subject, subject_name, agent_id, state, severity, value, args, opened_at, updated_at,
	last_notified_at, acknowledged_at, acknowledged_by, resolved_at, resolve_reason`

func scanAlert(sc interface{ Scan(...any) error }) (Alert, error) {
	var (
		a                         Alert
		cond, state, sev, args    string
		value                     sql.NullFloat64
		opened, updated, notified string
		acked, resolved           sql.NullString
	)
	if err := sc.Scan(&a.ID, &cond, &a.Subject, &a.SubjectName, &a.AgentID, &state, &sev, &value, &args, &opened, &updated,
		&notified, &acked, &a.AcknowledgedBy, &resolved, &a.ResolveReason); err != nil {
		return Alert{}, err
	}
	a.Condition, a.State, a.Severity = Condition(cond), State(state), Severity(sev)
	if value.Valid {
		v := value.Float64
		a.Value = &v
	}
	if args != "" {
		_ = json.Unmarshal([]byte(args), &a.Args)
	}
	a.OpenedAt, a.UpdatedAt, a.LastNotifiedAt = parseTime(opened), parseTime(updated), parseTime(notified)
	a.AcknowledgedAt, a.ResolvedAt = parseNull(acked), parseNull(resolved)
	return a, nil
}

// active are the alerts that are not resolved.
func (s store) active(ctx context.Context) ([]Alert, error) {
	return s.query(ctx, `SELECT `+alertColumns+` FROM alerts WHERE state <> 'resolved' ORDER BY id`)
}

func (s store) query(ctx context.Context, q string, args ...any) ([]Alert, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Alert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s store) list(ctx context.Context, states []State, limit int) ([]Alert, error) {
	q := `SELECT ` + alertColumns + ` FROM alerts`
	var args []any
	if len(states) > 0 {
		q += ` WHERE state IN (` + strings.TrimSuffix(strings.Repeat("?, ", len(states)), ", ") + `)`
		for _, st := range states {
			args = append(args, string(st))
		}
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	return s.query(ctx, q, args...)
}

func (s store) get(ctx context.Context, id int64) (Alert, error) {
	a, err := scanAlert(s.db.QueryRowContext(ctx, `SELECT `+alertColumns+` FROM alerts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Alert{}, ErrNotFound
	}
	return a, err
}

func argsJSON(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func (s store) insert(ctx context.Context, a Alert) (int64, error) {
	var value any
	if a.Value != nil {
		value = *a.Value
	}
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO alerts (condition, subject, subject_name, agent_id, state, severity, value, args, opened_at, updated_at, last_notified_at)
		 VALUES (?, ?, ?, ?, 'open', ?, ?, ?, ?, ?, ?) RETURNING id`,
		string(a.Condition), a.Subject, a.SubjectName, a.AgentID, string(a.Severity), value, argsJSON(a.Args),
		ts(a.OpenedAt), ts(a.UpdatedAt), ts(a.LastNotifiedAt)).Scan(&id)
	return id, err
}

// touch rewrites the values of an alert that stays open, and its severity.
func (s store) touch(ctx context.Context, a Alert) error {
	var value any
	if a.Value != nil {
		value = *a.Value
	}
	_, err := s.db.ExecContext(ctx, `UPDATE alerts SET severity = ?, value = ?, args = ?, updated_at = ?, last_notified_at = ? WHERE id = ?`,
		string(a.Severity), value, argsJSON(a.Args), ts(a.UpdatedAt), ts(a.LastNotifiedAt), a.ID)
	return err
}

func (s store) acknowledge(ctx context.Context, id int64, by string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE alerts SET state = 'acknowledged', acknowledged_at = ?, acknowledged_by = ?, updated_at = ? WHERE id = ? AND state = 'open'`,
		ts(now), by, ts(now), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s store) resolve(ctx context.Context, id int64, reason string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE alerts SET state = 'resolved', resolved_at = ?, resolve_reason = ?, updated_at = ?, last_notified_at = ? WHERE id = ? AND state <> 'resolved'`,
		ts(now), reason, ts(now), ts(now), id)
	return err
}

func (s store) pruneResolved(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM alerts WHERE state = 'resolved' AND resolved_at < ?`, ts(before))
	return err
}
