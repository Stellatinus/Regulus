package console

// certs.go: when the hub's certificates end (#245), as the running hub reports
// them on its local socket. Kept apart from snapshot.go and text.go so that
// the other work on the console merges cleanly.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/CodexCircle/Regulus/internal/localstatus"
)

// Certificate is one of the hub's certificates and when it ends.
type Certificate = localstatus.Certificate

// describeCert says which certificate it is and when it ends, in the
// language of the table s.
func describeCert(s strs, c Certificate, now time.Time) string {
	var what string
	switch c.Kind {
	case "ca":
		what = s.CertCA
	case "listener":
		what = s.CertListener
	default:
		agent := c.AgentID
		if c.Name != "" {
			agent = c.Name + " (" + c.AgentID + ")"
		}
		what = fmt.Sprintf(s.CertAgent, agent)
	}
	date := c.NotAfter.UTC().Format(time.DateOnly)
	days := int(math.Floor(c.NotAfter.Sub(now).Hours() / 24))
	if days < 0 {
		return fmt.Sprintf(s.CertEnded, what, date)
	}
	return fmt.Sprintf(s.CertEnds, what, date, strconv.Itoa(days))
}

// certDetail is the detail of the certificates check in the language of s.
func certDetail(s strs, snap Snapshot, now time.Time) string {
	if len(snap.CertWarnings) == 0 {
		return s.CertNoneSoon + " " + describeCert(s, *snap.CertSoonest, now)
	}
	var parts []string
	ca, agent := false, false
	for _, w := range snap.CertWarnings {
		parts = append(parts, describeCert(s, w, now))
		ca = ca || w.Kind == "ca"
		agent = agent || w.Kind == "agent"
	}
	detail := strings.Join(parts, "; ") + "."
	if ca {
		detail += " " + s.CertRenewCA
	}
	if agent {
		detail += " " + s.CertRenewAgent
	}
	return detail
}

// certCheck is the health check of the certificates, or false when the hub
// did not report them (not running, or older than #245). Name and Detail are
// English (--json); Title and Text follow the console language.
func certCheck(snap Snapshot, now time.Time) (Check, bool) {
	if snap.CertSoonest == nil {
		return Check{}, false
	}
	status := OK
	if len(snap.CertWarnings) > 0 {
		status = Warn
	}
	return Check{
		Name: en.Certificates, Status: status, Detail: certDetail(en, snap, now),
		Key: "certificates", Title: T.Certificates, Text: certDetail(T, snap, now),
	}, true
}
