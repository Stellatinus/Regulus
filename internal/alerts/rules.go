package alerts

import (
	"errors"
	"fmt"
	"time"
)

// Condition is a kind of problem the hub can see.
type Condition string

// The conditions of the design (docs/alerts.md).
const (
	AgentOffline      Condition = "agent_offline"
	DiskFull          Condition = "disk_full"
	MemoryHigh        Condition = "memory_high"
	CPUHigh           Condition = "cpu_high"
	CertificateEnding Condition = "certificate_ending"
	UpdateFailed      Condition = "update_failed"
	HubLockLost       Condition = "hub_lock_lost"
)

// Conditions lists them in the order of the design.
func Conditions() []Condition {
	return []Condition{AgentOffline, DiskFull, MemoryHigh, CPUHigh, CertificateEnding, UpdateFailed, HubLockLost}
}

// Severity says how urgent an alert is.
type Severity string

// The two severities. Critical is worse than warning.
const (
	Warning  Severity = "warning"
	Critical Severity = "critical"
)

func (s Severity) rank() int {
	if s == Critical {
		return 2
	}
	return 1
}

// MinRecovery is the least time a condition must be false before its alert
// resolves, whatever the rule says: a flapping condition cannot send a message
// per sample.
const MinRecovery = time.Minute

// Rule is a condition with its settings. There is one per condition.
type Rule struct {
	Condition Condition
	Enabled   bool
	// Threshold and CriticalThreshold are a percentage (disk, memory, CPU) or a
	// number of days (certificate), and are not used by the other conditions.
	// For the percentages the alert is on at or above them, for days at or
	// below. A CriticalThreshold of 0 means the condition has one severity.
	Threshold         float64
	CriticalThreshold float64
	// Duration is how long the condition must hold before the alert opens.
	Duration time.Duration
	// Recovery is how long it must be false before the alert resolves; zero
	// means the same as Duration. EffectiveRecovery is what is used.
	Recovery time.Duration
	// Severity is the severity of the alert; CriticalThreshold raises it.
	Severity Severity
	// Reminder repeats the message for an alert that is open and not
	// acknowledged; zero (the default) turns it off.
	Reminder time.Duration
}

// EffectiveRecovery is the recovery time the engine uses.
func (r Rule) EffectiveRecovery() time.Duration {
	d := r.Recovery
	if d == 0 {
		d = r.Duration
	}
	return max(d, MinRecovery)
}

// Validate checks the numbers of a rule.
func (r Rule) Validate() error {
	switch {
	case !knownCondition(r.Condition):
		return fmt.Errorf("unknown condition %q", r.Condition)
	case r.Severity != Warning && r.Severity != Critical:
		return errors.New("severity must be warning or critical")
	case r.Duration < 0 || r.Recovery < 0 || r.Reminder < 0:
		return errors.New("durations must not be negative")
	case r.Threshold < 0 || r.CriticalThreshold < 0:
		return errors.New("thresholds must not be negative")
	}
	switch r.Condition {
	case DiskFull, MemoryHigh, CPUHigh:
		if r.Threshold <= 0 || r.Threshold > 100 || r.CriticalThreshold > 100 {
			return errors.New("a percentage must be between 0 and 100")
		}
		if r.CriticalThreshold != 0 && r.CriticalThreshold < r.Threshold {
			return errors.New("the critical threshold must not be below the threshold")
		}
	case CertificateEnding:
		if r.Threshold <= 0 {
			return errors.New("the period must be at least one day")
		}
		if r.CriticalThreshold > r.Threshold {
			return errors.New("the critical period must not be longer than the period")
		}
	}
	return nil
}

func knownCondition(c Condition) bool {
	for _, k := range Conditions() {
		if k == c {
			return true
		}
	}
	return false
}

// DefaultRules are the quiet defaults of the design: a condition fires only
// after it has held for a while, and a threshold is where a person should act.
// CPU and memory are off because a busy server is not a broken one.
func DefaultRules() []Rule {
	return []Rule{
		{Condition: AgentOffline, Enabled: true, Duration: 5 * time.Minute, Severity: Critical},
		{Condition: DiskFull, Enabled: true, Threshold: 90, CriticalThreshold: 97, Duration: 10 * time.Minute, Severity: Warning},
		{Condition: MemoryHigh, Enabled: false, Threshold: 95, Duration: 30 * time.Minute, Severity: Warning},
		{Condition: CPUHigh, Enabled: false, Threshold: 95, Duration: 30 * time.Minute, Severity: Warning},
		{Condition: CertificateEnding, Enabled: true, Threshold: 30, CriticalThreshold: 7, Severity: Warning},
		{Condition: UpdateFailed, Enabled: true, Severity: Warning},
		{Condition: HubLockLost, Enabled: true, Severity: Critical},
	}
}
