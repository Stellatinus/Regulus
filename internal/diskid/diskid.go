// Package diskid decides the identity of a physical disk from what the drive
// reports about itself (#148), so that one history can follow a disk when it
// moves between servers. The identity key is the best identifier the drive
// has, in this order: a WWN, EUI-64 or NGUID, then model plus serial number.
// A disk without a reliable identifier is not identifiable, and two disks that
// report the same one are a conflict; neither is ever given a guessed key.
// It never uses a device name, a filesystem or partition UUID or the hostname:
// they change when a disk is moved, renamed or reformatted.
//
// The package is pure: it reads no system file and uses no database. The
// agent reads the identifiers (internal/agent) and the hub stores the result
// (internal/hub). It does not decide what is shown to whom (the inventory.read
// permission does) and it does not match a disk across servers: the hub does,
// by comparing keys.
package diskid

import (
	"strings"
	"unicode"
)

// Kind says which identifier a key was made from.
type Kind string

const (
	KindWWN         Kind = "wwn"          // a SATA or SAS world-wide name
	KindEUI64       Kind = "eui64"        // an NVMe EUI-64
	KindNGUID       Kind = "nguid"        // an NVMe namespace GUID
	KindModelSerial Kind = "model-serial" // the model and the serial number
	KindNone        Kind = "none"         // not identifiable
)

// Disk is what one drive reports. A field it does not report is empty.
type Disk struct {
	// Device is the kernel name ("sda"). It is never part of a key.
	Device    string
	Vendor    string
	Model     string
	Serial    string
	WWN       string
	WWNKind   string // "wwn", "eui64" or "nguid"
	Transport string // "sata", "sas", "nvme", "usb", "virtio", ... or empty
	Virtual   bool
}

// Identity is the result for one disk.
type Identity struct {
	Kind Kind
	// Key is the identity, empty when the disk is not identifiable. It starts
	// with the kind, so a WWN and a serial number can never collide.
	Key string
	// Conflict: another disk of the same report has the same identifier. Both
	// are then not identifiable, and the conflict is flagged.
	Conflict bool
	// Reason says, for a disk that is not identifiable, why (a short English
	// code: "no-identifier", "virtual", "usb-bridge", "placeholder-serial",
	// "duplicate"). Empty when the disk is identifiable.
	Reason string
}

// Identifiable reports whether the disk has a key.
func (i Identity) Identifiable() bool { return i.Key != "" }

// Identify decides the identity of every disk of one report; the result has
// the same order as disks.
func Identify(disks []Disk) []Identity {
	out := make([]Identity, len(disks))
	count := map[string]int{}
	for i, d := range disks {
		out[i] = identifyOne(d)
		if out[i].Key != "" {
			count[out[i].Key]++
		}
	}
	for i := range out {
		if out[i].Key != "" && count[out[i].Key] > 1 {
			out[i] = Identity{Kind: KindNone, Conflict: true, Reason: "duplicate"}
		}
	}
	return out
}

func identifyOne(d Disk) Identity {
	if hex, kind, ok := worldWideName(d); ok {
		return Identity{Kind: kind, Key: string(kind) + ":" + hex}
	}
	switch {
	case d.Virtual:
		// A hypervisor makes up the serial number of its disks, or repeats
		// it from a template: it does not belong to a drive.
		return Identity{Kind: KindNone, Reason: "virtual"}
	case strings.EqualFold(d.Transport, "usb"):
		// The serial number of an enclosure is the bridge's, not the disk's.
		return Identity{Kind: KindNone, Reason: "usb-bridge"}
	}
	serial := strings.TrimSpace(d.Serial)
	model := normalize(d.Model)
	switch {
	case serial == "" || model == "":
		return Identity{Kind: KindNone, Reason: "no-identifier"}
	case placeholder(serial):
		return Identity{Kind: KindNone, Reason: "placeholder-serial"}
	}
	return Identity{Kind: KindModelSerial, Key: "model-serial:" + model + "|" + strings.ToLower(serial)}
}

// worldWideName returns the normalized WWN, EUI-64 or NGUID of d. A value
// that is not hexadecimal of a valid length, or is all zeros, is not one.
func worldWideName(d Disk) (string, Kind, bool) {
	hex := normalizeWWN(d.WWN)
	if hex == "" {
		return "", "", false
	}
	switch Kind(strings.ToLower(d.WWNKind)) {
	case KindEUI64:
		if len(hex) == 16 {
			return hex, KindEUI64, true
		}
	case KindNGUID:
		if len(hex) == 32 {
			return hex, KindNGUID, true
		}
	default:
		if len(hex) == 16 || len(hex) == 32 {
			return hex, KindWWN, true
		}
	}
	return "", "", false
}

// normalizeWWN lowercases a world-wide identifier and removes the prefixes
// and separators the system puts around it ("0x5000c500a1b2c3d4",
// "naa.5000c500a1b2c3d4", "eui.0025385b71b0c7e3", "00 25 38 5b"). It returns
// "" for anything that is not hexadecimal or is all zeros.
func normalizeWWN(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"0x", "naa.", "eui.", "nguid."} {
		s = strings.TrimPrefix(s, p)
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ':' || r == '-' || r == ' ' || r == '.':
		case unicode.IsDigit(r) || (r >= 'a' && r <= 'f'):
			b.WriteRune(r)
		default:
			return ""
		}
	}
	hex := b.String()
	if strings.Trim(hex, "0") == "" {
		return ""
	}
	return hex
}

// normalize lowercases a model name and collapses runs of spaces; drives pad
// the field with blanks, and the padding differs between sources.
func normalize(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// placeholder reports whether a serial number is one a manufacturer or a
// firmware fills in when it has none: empty-looking, one repeated character,
// or one of the well-known filler texts.
func placeholder(serial string) bool {
	s := strings.ToLower(strings.TrimSpace(serial))
	switch s {
	case "", "none", "null", "unknown", "n/a", "na", "default string", "to be filled by o.e.m.",
		"not specified", "not available", "0123456789", "1234567890", "serial", "serialnumber":
		return true
	}
	first := []rune(s)[0]
	for _, r := range s {
		if r != first {
			return false
		}
	}
	return len(s) > 1 || first == '0'
}
