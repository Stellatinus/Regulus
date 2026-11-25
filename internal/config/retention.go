package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Retention is how long a tier of the stored metrics history is kept (#405).
// It is written as days ("3d", "90d") or as a Go duration ("36h"); 0 turns the
// tier off.
type Retention time.Duration

const (
	// DefaultRetention1m and DefaultRetention15m are the defaults of
	// metrics_retention_1m and metrics_retention_15m.
	DefaultRetention1m  Retention = Retention(3 * 24 * time.Hour)
	DefaultRetention15m Retention = Retention(90 * 24 * time.Hour)
	// MaxRetention is the longest accepted: ten years, far beyond any use,
	// and small enough that no arithmetic on it overflows.
	MaxRetention Retention = Retention(3650 * 24 * time.Hour)
)

// ParseRetention reads "3d", "36h", "90m" or "0".
func ParseRetention(s string) (Retention, error) {
	v := strings.TrimSpace(s)
	var d time.Duration
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.ParseInt(strings.TrimSpace(days), 10, 64)
		if err != nil || n < 0 || n > int64(MaxRetention)/int64(24*time.Hour) {
			return 0, fmt.Errorf("%q is not a retention", s)
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		p, err := time.ParseDuration(v)
		if err != nil || p < 0 || p > time.Duration(MaxRetention) {
			return 0, fmt.Errorf("%q is not a retention", s)
		}
		d = p
	}
	return Retention(d), nil
}

// Duration returns the retention as a time.Duration.
func (r Retention) Duration() time.Duration { return time.Duration(r) }

// String writes whole days as days ("3d"), anything else as a duration, and
// "0" for off.
func (r Retention) String() string {
	switch {
	case r == 0:
		return "0"
	case time.Duration(r)%(24*time.Hour) == 0:
		return strconv.FormatInt(int64(time.Duration(r)/(24*time.Hour)), 10) + "d"
	}
	return time.Duration(r).String()
}

// UnmarshalYAML accepts the forms ParseRetention does.
func (r *Retention) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := ParseRetention(s)
	if err != nil {
		return fmt.Errorf("line %d: %v (use days such as 3d, or 0 to turn it off)", n.Line, err)
	}
	*r = v
	return nil
}

// MarshalYAML writes the retention the way it is read.
func (r Retention) MarshalYAML() (any, error) { return r.String(), nil }
