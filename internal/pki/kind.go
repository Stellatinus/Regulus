package pki

import (
	"crypto/x509"
	"net/url"
	"slices"
	"strings"
)

// Kind is what a certificate issued by the hub CA is for. Every certificate
// the CA issues carries exactly one kind, as a URI in its subject alternative
// names: "regulus:<kind>:<id>" for a client certificate (id is also the common
// name) and "regulus:hub" for the certificate of the agent listener. Each
// service accepts only its own kind, so a certificate made for one purpose
// cannot be used for another even though the same CA signed it (#316).
type Kind string

const (
	// KindAgent is the client certificate of an enrolled agent.
	KindAgent Kind = "agent"
	// KindHub is the certificate the agent listener shows to agents.
	KindHub Kind = "hub"
	// KindDevice is reserved for the device certificate of Regulus Access
	// (#154, not designed). Nothing issues it today; it exists so that the
	// agent listener's refusal of any other kind can be tested.
	KindDevice Kind = "device"
)

// kindScheme is the URI scheme of the kind marker.
const kindScheme = "regulus"

// legacyAgentOrganization is the subject organization of every agent
// certificate, including those issued before kinds existed.
const legacyAgentOrganization = "Regulus agent"

func (k Kind) isClient() bool { return k == KindAgent || k == KindDevice }

func (k Kind) known() bool { return k.isClient() || k == KindHub }

func kindURI(k Kind, id string) *url.URL {
	if id == "" {
		return &url.URL{Scheme: kindScheme, Opaque: string(k)}
	}
	return &url.URL{Scheme: kindScheme, Opaque: string(k) + ":" + id}
}

// validID reports whether id can be the common name and the end of the kind
// URI: 1 to 64 letters, digits, '.', '_' or '-'. Agent ids are "agent-" and
// 16 hex digits.
func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func kindMarks(c *x509.Certificate) []*url.URL {
	var marks []*url.URL
	for _, u := range c.URIs {
		if strings.EqualFold(u.Scheme, kindScheme) {
			marks = append(marks, u)
		}
	}
	return marks
}

// hasKindMark reports whether c carries at least one "regulus:" URI.
func hasKindMark(c *x509.Certificate) bool { return len(kindMarks(c)) > 0 }

// kindOf tells the kind of a certificate verified against the hub CA. ok is
// false when it cannot be told, and the certificate must then be refused by
// every service:
//
//   - more than one "regulus:" URI, an unknown kind, or a client kind whose id
//     is not the common name;
//   - no "regulus:" URI and not an agent certificate of an older hub.
//
// A certificate with no "regulus:" URI at all and the organization "Regulus
// agent" is an agent certificate issued before kinds existed (legacy is true).
// Every older hub issued exactly that, and nothing else, as a client
// certificate, and every certificate this version issues carries a kind, so a
// certificate without one cannot be of a newer kind. This exception lasts as
// long as such certificates do (at most ClientValidity after the last
// enrollment on an older hub) and is to be removed once renewal (#128) has
// re-issued them; see docs/decisions.md.
func kindOf(c *x509.Certificate) (kind Kind, legacy, ok bool) {
	marks := kindMarks(c)
	switch len(marks) {
	case 0:
		if slices.Contains(c.Subject.Organization, legacyAgentOrganization) {
			return KindAgent, true, true
		}
		return "", false, false
	case 1:
	default:
		return "", false, false
	}
	k, id, hasID := strings.Cut(marks[0].Opaque, ":")
	kind = Kind(k)
	if !kind.known() {
		return "", false, false
	}
	if kind.isClient() {
		if !hasID || id != c.Subject.CommonName {
			return "", false, false
		}
	} else if hasID {
		return "", false, false
	}
	return kind, false, true
}
