package hub

// certs.go: when the hub's certificates end (#245). The hub keeps the end of
// the CA, of the current agent listener certificate and of each enrolled
// agent's certificate; it serves them at GET /api/certificates, reports the
// soonest one to `regulus status`, and logs one warning a day while the CA or
// an agent certificate ends within CertWarnBefore. Renewing them is #128.

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/CodexCircle/Regulus/internal/localstatus"
)

// CertWarnBefore is how long before it ends a certificate is warned about.
const CertWarnBefore = 90 * 24 * time.Hour

// certLogEvery is how often, at most, the warning is logged.
const certLogEvery = 24 * time.Hour

// certCheckEvery is how often the hub looks whether it is time to log it.
const certCheckEvery = time.Hour

// Kinds of certificate.
const (
	CertKindCA       = "ca"
	CertKindListener = "listener"
	CertKindAgent    = "agent"
)

// What the log says about renewing, until #128 makes it automatic.
const (
	renewAgentHint = "agent certificates are not renewed automatically yet (#128): revoke the server and enroll it again with a new token"
	renewCAHint    = "the CA is not renewed automatically yet (#128): a new CA means every agent must enroll again"
)

// CertEnd is one certificate and when it ends.
type CertEnd struct {
	Kind string
	// AgentID and Name (the display name, else the host name) are set for an
	// agent certificate.
	AgentID  string
	Name     string
	NotAfter time.Time
}

// Certificates is when the hub's certificates end.
type Certificates struct {
	CA CertEnd
	// Listener is nil while the agent listener is off. It is renewed by
	// itself 30 days before it ends, so it is never warned about.
	Listener *CertEnd
	// Agents are the enrolled agents that are not revoked and whose end date
	// is known, soonest first.
	Agents []CertEnd
	// AgentsUnknown counts the enrolled agents, not revoked, whose end date
	// the hub does not know yet: enrolled before it kept it and not connected
	// since.
	AgentsUnknown int
	// LegacyAgents counts the connected agents that present a certificate
	// with no kind, issued before #316 (#322). When it is 0 and every agent
	// has connected since, the legacy case can be removed.
	LegacyAgents int
}

// Soonest is the certificate that ends first, of all of them.
func (c Certificates) Soonest() CertEnd {
	s := c.CA
	if c.Listener != nil && c.Listener.NotAfter.Before(s.NotAfter) {
		s = *c.Listener
	}
	for _, a := range c.Agents {
		if a.NotAfter.Before(s.NotAfter) {
			s = a
		}
	}
	return s
}

// Warnings lists the CA and the agent certificates that end within
// CertWarnBefore of now, or have ended, soonest first.
func (c Certificates) Warnings(now time.Time) []CertEnd {
	limit := now.Add(CertWarnBefore)
	var out []CertEnd
	if c.CA.NotAfter.Before(limit) {
		out = append(out, c.CA)
	}
	for _, a := range c.Agents {
		if a.NotAfter.Before(limit) {
			out = append(out, a)
		}
	}
	slices.SortStableFunc(out, func(a, b CertEnd) int { return a.NotAfter.Compare(b.NotAfter) })
	return out
}

// CertificateSource reports when the certificates end. *Hub implements it.
type CertificateSource interface {
	Certificates(ctx context.Context) (Certificates, error)
}

// Certificates reads when the hub's certificates end.
func (h *Hub) Certificates(ctx context.Context) (Certificates, error) {
	c := Certificates{CA: CertEnd{Kind: CertKindCA, NotAfter: h.fleet.CA().NotAfter().UTC()}}
	if end, _ := h.listenerCertEnd.Load().(time.Time); !end.IsZero() {
		c.Listener = &CertEnd{Kind: CertKindListener, NotAfter: end.UTC()}
	}
	agents, err := h.fleet.Agents(ctx)
	if err != nil {
		return Certificates{}, err
	}
	for _, a := range agents {
		switch {
		case a.Revoked:
		case a.CertNotAfter.IsZero():
			c.AgentsUnknown++
		default:
			c.Agents = append(c.Agents, CertEnd{Kind: CertKindAgent, AgentID: a.ID, Name: cmp.Or(a.Name, a.Hostname), NotAfter: a.CertNotAfter.UTC()})
		}
	}
	slices.SortStableFunc(c.Agents, func(a, b CertEnd) int { return a.NotAfter.Compare(b.NotAfter) })
	for _, a := range h.Agents() {
		if a.LegacyCert {
			c.LegacyAgents++
		}
	}
	return c, nil
}

// daysLeft is the number of whole days from now to t, negative once t passed.
func daysLeft(now, t time.Time) int {
	return int(math.Floor(t.Sub(now).Hours() / 24))
}

// certJSON is a certificate in GET /api/certificates.
type certJSON struct {
	Kind     string    `json:"kind"`
	AgentID  string    `json:"agent_id,omitempty"`
	Name     string    `json:"name,omitempty"`
	NotAfter time.Time `json:"not_after"`
	DaysLeft int       `json:"days_left"`
}

func toCertJSON(c CertEnd, now time.Time) certJSON {
	return certJSON{Kind: c.Kind, AgentID: c.AgentID, Name: c.Name, NotAfter: c.NotAfter.UTC(), DaysLeft: daysLeft(now, c.NotAfter)}
}

// certificatesJSON is the answer of GET /api/certificates.
type certificatesJSON struct {
	CA            certJSON   `json:"ca"`
	Listener      *certJSON  `json:"listener"`
	Soonest       certJSON   `json:"soonest"`
	AgentsUnknown int        `json:"agents_unknown"`
	LegacyAgents  int        `json:"legacy_agents"`
	WarnDays      int        `json:"warn_days"`
	Warnings      []certJSON `json:"warnings"`
}

func toCertificatesJSON(c Certificates, now time.Time) certificatesJSON {
	out := certificatesJSON{
		CA:            toCertJSON(c.CA, now),
		Soonest:       toCertJSON(c.Soonest(), now),
		AgentsUnknown: c.AgentsUnknown,
		LegacyAgents:  c.LegacyAgents,
		WarnDays:      int(CertWarnBefore / (24 * time.Hour)),
		Warnings:      []certJSON{},
	}
	if c.Listener != nil {
		l := toCertJSON(*c.Listener, now)
		out.Listener = &l
	}
	for _, w := range c.Warnings(now) {
		out.Warnings = append(out.Warnings, toCertJSON(w, now))
	}
	return out
}

// certificatesHandler serves GET /api/certificates. The end of each agent's
// certificate is also in GET /api/agents (cert_not_after).
func certificatesHandler(src CertificateSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if src == nil {
			http.NotFound(w, r)
			return
		}
		c, err := src.Certificates(r.Context())
		if err != nil {
			writeInternal(w)
			return
		}
		writeJSONBody(w, http.StatusOK, toCertificatesJSON(c, time.Now()))
	}
}

// toLocalCert is a certificate for `regulus status`.
func toLocalCert(c CertEnd) localstatus.Certificate {
	return localstatus.Certificate{Kind: c.Kind, AgentID: c.AgentID, Name: c.Name, NotAfter: c.NotAfter}
}

// certLogState remembers when the warning was last logged.
type certLogState struct {
	mu   sync.Mutex
	last time.Time
}

// watchCertificates logs, at most once a day until ctx ends, which
// certificates end soon. The first check is at start.
func (h *Hub) watchCertificates(ctx context.Context) {
	go func() {
		h.logCertificates(ctx, time.Now())
		t := time.NewTicker(certCheckEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				h.logCertificates(ctx, now)
			}
		}
	}()
}

// logCertificates logs one warning line when the CA or an agent certificate
// ends within CertWarnBefore, unless it did so less than a day ago. It returns
// true when it logged.
func (h *Hub) logCertificates(ctx context.Context, now time.Time) bool {
	h.certLog.mu.Lock()
	defer h.certLog.mu.Unlock()
	if !h.certLog.last.IsZero() && now.Sub(h.certLog.last) < certLogEvery {
		return false
	}
	c, err := h.Certificates(ctx)
	if err != nil {
		if ctx.Err() == nil {
			h.log.Warn("cannot read when the certificates end", "err", err)
		}
		return false
	}
	warnings := c.Warnings(now)
	if len(warnings) == 0 {
		return false
	}
	h.certLog.last = now
	var names, renew []string
	ca, agent := false, false
	for _, w := range warnings {
		names = append(names, describeCert(w, now))
		ca = ca || w.Kind == CertKindCA
		agent = agent || w.Kind == CertKindAgent
	}
	if ca {
		renew = append(renew, renewCAHint)
	}
	if agent {
		renew = append(renew, renewAgentHint)
	}
	h.log.Warn("certificates end within 90 days",
		"count", len(warnings),
		"soonest", warnings[0].NotAfter.UTC().Format(time.RFC3339),
		"certificates", strings.Join(names, "; "),
		"renew", strings.Join(renew, "; "))
	return true
}

// describeCert names a certificate and its end for the log.
func describeCert(c CertEnd, now time.Time) string {
	what := "CA"
	if c.Kind == CertKindAgent {
		what = "agent " + c.AgentID
		if c.Name != "" {
			what += " (" + c.Name + ")"
		}
	}
	d := daysLeft(now, c.NotAfter)
	if d < 0 {
		return fmt.Sprintf("%s ended %s", what, c.NotAfter.UTC().Format(time.DateOnly))
	}
	return fmt.Sprintf("%s ends %s (in %d days)", what, c.NotAfter.UTC().Format(time.DateOnly), d)
}
