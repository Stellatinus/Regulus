package alerts

import (
	"math"
	"strconv"
	"time"
)

// finding is a condition that is true now for one subject.
type finding struct {
	condition Condition
	subject   string
	name      string
	agentID   string
	// level is the severity the numbers call for. The engine opens the alert
	// at the rule's severity and raises it to critical only when the critical
	// level has held for the rule's duration too.
	level Severity
	value *float64
	args  map[string]string
	// since is when the condition became true, when the snapshot knows it (a
	// server that went offline); zero means the engine starts counting now.
	since time.Time
}

func key(c Condition, subject string) string { return string(c) + "\x00" + subject }

func (f finding) key() string { return key(f.condition, f.subject) }

func pct(v float64) *float64 { v = math.Round(v*10) / 10; return &v }

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// findings evaluates the rules that are on against the snapshot. skipped says
// that a subject is not evaluated this time (its server is not reporting):
// its alert stays as it is.
func findings(rules map[Condition]Rule, s Snapshot) (out []finding, skipped func(Condition, string) bool) {
	on := func(c Condition) (Rule, bool) {
		r, ok := rules[c]
		if !ok || !r.Enabled {
			return Rule{}, false
		}
		if len(s.Only) > 0 {
			found := false
			for _, o := range s.Only {
				found = found || o == c
			}
			if !found {
				return Rule{}, false
			}
		}
		return r, true
	}
	notReporting := map[string]bool{} // servers whose usage is not evaluated

	for _, a := range s.Agents {
		if !a.Connected {
			notReporting[a.ID] = true
		}
		if r, ok := on(AgentOffline); ok && !a.Connected {
			since := a.LastSeen
			if since.IsZero() {
				// Never connected (enrolled, nothing yet): it counts from now.
				since = time.Time{}
			}
			out = append(out, finding{
				condition: AgentOffline, subject: "agent:" + a.ID, name: a.Name, agentID: a.ID, level: r.Severity,
				since: since, args: map[string]string{"server": a.Name},
			})
		}
		if !a.Reporting {
			if a.Connected {
				notReporting[a.ID] = true
			}
			continue
		}
		if r, ok := on(DiskFull); ok {
			for _, d := range a.Disks {
				if d.TotalBytes == 0 {
					continue
				}
				p := 100 * float64(d.UsedBytes) / float64(d.TotalBytes)
				if p < r.Threshold {
					continue
				}
				lvl := r.Severity
				if r.CriticalThreshold > 0 && p >= r.CriticalThreshold {
					lvl = Critical
				}
				out = append(out, finding{
					condition: DiskFull, subject: "agent:" + a.ID + ":disk:" + d.Mount, name: a.Name + ": " + d.Mount, agentID: a.ID,
					level: lvl, value: pct(p),
					args: map[string]string{"server": a.Name, "mount": d.Mount, "percent": fmtNum(*pct(p)), "threshold": fmtNum(r.Threshold)},
				})
			}
		}
		if r, ok := on(MemoryHigh); ok && a.MemoryPercent != nil && *a.MemoryPercent >= r.Threshold {
			out = append(out, usageFinding(MemoryHigh, r, a, *a.MemoryPercent))
		}
		if r, ok := on(CPUHigh); ok && a.CPUPercent != nil && *a.CPUPercent >= r.Threshold {
			out = append(out, usageFinding(CPUHigh, r, a, *a.CPUPercent))
		}
	}

	if r, ok := on(CertificateEnding); ok {
		for _, c := range s.Certificates {
			days := int(math.Floor(c.NotAfter.Sub(s.Now).Hours() / 24))
			if float64(days) > r.Threshold {
				continue
			}
			lvl := r.Severity
			if r.CriticalThreshold > 0 && float64(days) <= r.CriticalThreshold {
				lvl = Critical
			}
			v := float64(days)
			out = append(out, finding{
				condition: CertificateEnding, subject: "cert:" + c.Subject, name: c.Name, agentID: c.AgentID, level: lvl, value: &v,
				args: map[string]string{"certificate": c.Subject, "name": c.Name, "days": strconv.Itoa(days), "not_after": c.NotAfter.UTC().Format(time.RFC3339)},
			})
		}
	}
	if r, ok := on(UpdateFailed); ok {
		for _, f := range s.UpdateFailures {
			out = append(out, finding{
				condition: UpdateFailed, subject: "agent:" + f.AgentID + ":update", name: f.Name, agentID: f.AgentID, level: r.Severity,
				args: map[string]string{"server": f.Name, "version": f.Version, "message": f.Message},
			})
		}
	}
	if r, ok := on(HubLockLost); ok && s.HubLockLost {
		out = append(out, finding{condition: HubLockLost, subject: "hub", name: "hub", level: r.Severity, args: map[string]string{}})
	}

	skipped = func(c Condition, subject string) bool {
		// Only the conditions that read a report wait for the server to report.
		switch c {
		case DiskFull, MemoryHigh, CPUHigh:
			return notReporting[agentOfSubject(subject)]
		}
		return false
	}
	return out, skipped
}

func usageFinding(c Condition, r Rule, a Agent, p float64) finding {
	lvl := r.Severity
	if r.CriticalThreshold > 0 && p >= r.CriticalThreshold {
		lvl = Critical
	}
	return finding{
		condition: c, subject: "agent:" + a.ID, name: a.Name, agentID: a.ID, level: lvl, value: pct(p),
		args: map[string]string{"server": a.Name, "percent": fmtNum(*pct(p)), "threshold": fmtNum(r.Threshold)},
	}
}

// agentOfSubject is the server id of a subject "agent:<id>[:...]", or "".
func agentOfSubject(subject string) string {
	const p = "agent:"
	if len(subject) <= len(p) || subject[:len(p)] != p {
		return ""
	}
	rest := subject[len(p):]
	for i := 0; i < len(rest); i++ {
		if rest[i] == ':' {
			return rest[:i]
		}
	}
	return rest
}
