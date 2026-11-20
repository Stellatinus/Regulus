package agent

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// The fuzz targets below run as ordinary tests on their seed corpus (the
// f.Add calls and testdata/fuzz/<Target>), so `go test` checks every seed and
// every input a fuzzer ever found. CI also runs each one with -fuzz for a
// short time every night (.github/workflows/fuzz.yml). To run one locally:
//
//	go test ./internal/agent -run '^$' -fuzz '^FuzzParseMounts$' -fuzztime 30s
//
// A failing input is saved under testdata/fuzz/<Target>; commit it with the
// fix, as a regression test.

// FuzzParseCPUTimes checks that any /proc/stat content either fails or gives
// counters where the busy time is part of the total, and that the share
// computed from two samples is always a number between 0 and 100.
func FuzzParseCPUTimes(f *testing.F) {
	f.Add(statSample, "cpu  101 10 50 801 40 5 5 0 0 0\n")
	f.Add("cpu  1 2 3 4\n", "cpu  1 2 3 4 5 6 7 8 9 10 11 12\n")
	f.Add("cpu 18446744073709551615 1 1 1 1\n", "cpu 0 0 0 0 0\n")
	f.Add("cpu  0 0 0 18446744073709551615 18446744073709551615\n", "")
	f.Add("cpu0 1 1 1 1 1\ncpu 2 2 2 2 2\n", "cpu x 1 1 1 1\n")
	f.Fuzz(func(t *testing.T, a, b string) {
		prev, errA := parseCPUTimes(a)
		now, errB := parseCPUTimes(b)
		for _, c := range []struct {
			times cpuTimes
			err   error
		}{{prev, errA}, {now, errB}} {
			if c.err != nil {
				if c.times != (cpuTimes{}) {
					t.Fatalf("an error came with counters %+v", c.times)
				}
				continue
			}
			if c.times.busy > c.times.total {
				t.Fatalf("busy %d is more than total %d", c.times.busy, c.times.total)
			}
		}
		for _, pair := range [][2]cpuTimes{{prev, now}, {now, prev}, {{}, now}, {now, now}} {
			pct := cpuUsagePercent(pair[0], pair[1])
			if math.IsNaN(pct) || pct < 0 || pct > 100 {
				t.Fatalf("cpuUsagePercent(%+v, %+v) = %v, want 0..100", pair[0], pair[1], pct)
			}
		}
	})
}

// FuzzParseMemInfo checks that any /proc/meminfo content either fails or
// gives a report where nothing used or available is more than the total, and
// that a value given in kB that does not fit in bytes is never wrapped into a
// small number.
func FuzzParseMemInfo(f *testing.F) {
	f.Add(meminfoSample)
	f.Add("MemTotal: 1000 kB\nMemFree: 2000 kB\n")
	f.Add("MemTotal: 1000\nMemAvailable: 5000\nSwapTotal: 10\nSwapFree: 20\n")
	f.Add("MemTotal: 18014398509481984 kB\nMemAvailable: 1 kB\n")
	f.Add("MemTotal:\nMemTotal: x\nMemTotal: -1 kB\n")
	f.Add("MemTotal: 4 kB\nMemFree: 18446744073709551615\nBuffers: 18446744073709551615\nCached: 2\n")
	f.Fuzz(func(t *testing.T, content string) {
		m, err := parseMemInfo(content)
		if err != nil {
			if m != nil {
				t.Fatal("an error came with a report")
			}
			return
		}
		if m.GetUsedBytes() > m.GetTotalBytes() || m.GetAvailableBytes() > m.GetTotalBytes() {
			t.Fatalf("used %d or available %d is more than total %d", m.GetUsedBytes(), m.GetAvailableBytes(), m.GetTotalBytes())
		}
		if m.GetUsedBytes()+m.GetAvailableBytes() != m.GetTotalBytes() {
			t.Fatalf("used %d + available %d != total %d", m.GetUsedBytes(), m.GetAvailableBytes(), m.GetTotalBytes())
		}
		if m.GetSwapUsedBytes() > m.GetSwapTotalBytes() {
			t.Fatalf("swap used %d is more than swap total %d", m.GetSwapUsedBytes(), m.GetSwapTotalBytes())
		}
		// A kB value is multiplied by 1024: the result is a multiple of 1024
		// that did not overflow, or the line was not used at all.
		if strings.Contains(content, "MemTotal:") {
			for line := range strings.SplitSeq(content, "\n") {
				name, rest, ok := strings.Cut(line, ":")
				fields := strings.Fields(rest)
				if !ok || name != "MemTotal" || len(fields) < 2 || fields[1] != "kB" {
					continue
				}
				var kb uint64
				if _, err := fmt.Sscan(fields[0], &kb); err == nil && kb > math.MaxUint64/1024 && m.GetTotalBytes() == kb*1024 {
					t.Fatalf("MemTotal %d kB overflowed into %d bytes", kb, m.GetTotalBytes())
				}
			}
		}
	})
}

// FuzzParseMounts checks that any /proc/mounts content gives at most one
// mount per device, only of real filesystems, each with a mount point.
func FuzzParseMounts(f *testing.F) {
	f.Add(mountsSample)
	f.Add(`/dev/sda1 /a\040b ext4 rw 0 0` + "\n" + `/dev/sda1 / ext4 rw 0 0`)
	f.Add(`/dev/sda1 /end\04 ext4 rw 0 0` + "\n" + `/dev/sdb1 \ xfs rw 0 0`)
	f.Add(`/dev/x /mnt\134\040 vfat rw` + "\n" + "   \n" + "a b\n")
	f.Fuzz(func(t *testing.T, content string) {
		mounts := parseMounts(content)
		seen := map[string]bool{}
		for _, m := range mounts {
			if seen[m.device] {
				t.Fatalf("device %q reported twice: %+v", m.device, mounts)
			}
			seen[m.device] = true
			if !slices.Contains(realFilesystems, m.fsType) {
				t.Fatalf("pseudo filesystem %q reported", m.fsType)
			}
			if m.device == "" || m.mountPoint == "" {
				t.Fatalf("empty device or mount point: %+v", m)
			}
		}
	})
}

// FuzzParseNetDev checks that any /proc/net/dev content gives named
// interfaces other than loopback, each at most once (the hub pairs the
// counters of two reports by name).
func FuzzParseNetDev(f *testing.F) {
	f.Add(netDevSample)
	f.Add("eth0: 18446744073709551615 0 0 0 0 0 0 0 18446744073709551615 0 0 0 0 0 0 0\n")
	f.Add("eth0: 1 2 3 4 5 6 7 8\n: 1 2 3 4 5 6 7 8 9\n")
	f.Add("eth0:1 0 0 0 0 0 0 0 2 0 0 0 0 0 0 0\neth0: 3 0 0 0 0 0 0 0 4 0 0 0 0 0 0 0\n")
	f.Add("  lo : 1 0 0 0 0 0 0 0 2\nwlan0: -1 0 0 0 0 0 0 0 2\n")
	f.Fuzz(func(t *testing.T, content string) {
		seen := map[string]bool{}
		for _, n := range parseNetDev(content) {
			name := n.GetInterface()
			if name == "" || name == "lo" || strings.ContainsAny(name, ":\n") || strings.TrimSpace(name) != name {
				t.Fatalf("bad interface name %q", name)
			}
			if seen[name] {
				t.Fatalf("interface %q reported twice", name)
			}
			seen[name] = true
		}
	})
}

// escapeMount is the kernel's encoding of a path in /proc/mounts: space, tab,
// newline and backslash as three octal digits.
func escapeMount(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case ' ', '\t', '\n', '\\':
			fmt.Fprintf(&b, `\%03o`, c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// FuzzUnescapeMount checks that decoding never panics, never makes a path
// longer, undoes the kernel's encoding exactly and leaves a path without a
// backslash as it is, including an escape cut short at the end.
func FuzzUnescapeMount(f *testing.F) {
	for _, s := range []string{"", "/", `/mnt/my\040disk`, `a\040`, `a\04`, `a\0`, `a\`, `\`, `\\`, `\777`, `\400`, `\0400`, `\134\040`, `/x\08y`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := unescapeMount(s)
		if len(got) > len(s) {
			t.Fatalf("unescapeMount(%q) = %q is longer", s, got)
		}
		if !strings.Contains(s, `\`) && got != s {
			t.Fatalf("unescapeMount(%q) = %q changed a path without escapes", s, got)
		}
		if back := unescapeMount(escapeMount(s)); back != s {
			t.Fatalf("unescapeMount(escapeMount(%q)) = %q", s, back)
		}
		if utf8.ValidString(s) && !strings.Contains(s, `\`) && !utf8.ValidString(got) {
			t.Fatalf("unescapeMount(%q) made invalid UTF-8", s)
		}
	})
}
