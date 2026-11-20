package config

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ByteSize is an amount of memory or disk in bytes. In the configuration it is
// written with a binary unit: 256MiB, 1GiB, 512KiB, or a plain number of bytes.
type ByteSize int64

const (
	// DefaultMetricsBudget is the default of metrics_budget: it keeps a full
	// hour at the 10 s default for about 500 typical agents (#244).
	DefaultMetricsBudget ByteSize = 256 << 20
	// MinMetricsBudget is the smallest accepted metrics_budget; below it the
	// history of a few agents would not fit.
	MinMetricsBudget ByteSize = 4 << 20
)

var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"gib", 1 << 30}, {"mib", 1 << 20}, {"kib", 1 << 10},
	{"gb", 1 << 30}, {"mb", 1 << 20}, {"kb", 1 << 10},
	{"g", 1 << 30}, {"m", 1 << 20}, {"k", 1 << 10}, {"b", 1},
}

// ParseSize reads a size such as 256MiB, 1GiB, 512k or 1048576. All units are
// binary (MB means MiB), so that what a person types is what is reserved.
func ParseSize(s string) (ByteSize, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range sizeUnits {
		if strings.HasSuffix(v, u.suffix) {
			v = strings.TrimSpace(strings.TrimSuffix(v, u.suffix))
			mult = u.mult
			break
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 || n > (1<<62)/mult {
		return 0, fmt.Errorf("%q is not a size", s)
	}
	return ByteSize(n * mult), nil
}

// String writes the size with the largest unit that divides it exactly.
func (b ByteSize) String() string {
	switch {
	case b >= 1<<30 && b%(1<<30) == 0:
		return strconv.FormatInt(int64(b)>>30, 10) + "GiB"
	case b >= 1<<20 && b%(1<<20) == 0:
		return strconv.FormatInt(int64(b)>>20, 10) + "MiB"
	case b >= 1<<10 && b%(1<<10) == 0:
		return strconv.FormatInt(int64(b)>>10, 10) + "KiB"
	}
	return strconv.FormatInt(int64(b), 10) + "B"
}

// UnmarshalYAML accepts a size as a string ("256MiB") or a number of bytes.
func (b *ByteSize) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := ParseSize(s)
	if err != nil {
		return fmt.Errorf("line %d: %v (use a size such as 256MiB)", n.Line, err)
	}
	*b = v
	return nil
}

// MarshalYAML writes the size the way it is read.
func (b ByteSize) MarshalYAML() (any, error) { return b.String(), nil }
