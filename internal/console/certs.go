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

// describeCert says which certificate it is and when it ends, in the console
// language.
func describeCert(c Certificate, now time.Time) string {
	var what string
	switch c.Kind {
	case "ca":
		what = T.CertCA
	case "listener":
		what = T.CertListener
	default:
		agent := c.AgentID
		if c.Name != "" {
			agent = c.Name + " (" + c.AgentID + ")"
		}
		what = fmt.Sprintf(T.CertAgent, agent)
	}
	date := c.NotAfter.UTC().Format(time.DateOnly)
	days := int(math.Floor(c.NotAfter.Sub(now).Hours() / 24))
	if days < 0 {
		return fmt.Sprintf(T.CertEnded, what, date)
	}
	return fmt.Sprintf(T.CertEnds, what, date, strconv.Itoa(days))
}

// certCheck is the health check of the certificates, or false when the hub
// did not report them (not running, or older than #245).
func certCheck(snap Snapshot, now time.Time) (Check, bool) {
	if snap.CertSoonest == nil {
		return Check{}, false
	}
	if len(snap.CertWarnings) == 0 {
		return Check{"Certificates", OK, T.CertNoneSoon + " " + describeCert(*snap.CertSoonest, now)}, true
	}
	var parts []string
	ca, agent := false, false
	for _, w := range snap.CertWarnings {
		parts = append(parts, describeCert(w, now))
		ca = ca || w.Kind == "ca"
		agent = agent || w.Kind == "agent"
	}
	detail := strings.Join(parts, "; ") + "."
	if ca {
		detail += " " + T.CertRenewCA
	}
	if agent {
		detail += " " + T.CertRenewAgent
	}
	return Check{"Certificates", Warn, detail}, true
}
