package agent

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// cgroupRoot is where the cgroup file systems are mounted.
const cgroupRoot = "/sys/fs/cgroup"

// cgroupLimits are the limits of the control group the agent runs in, read
// from the cgroup files (#325). An agent in a container reads /proc as it
// finds it, and without LXCFS that is the host's memory and CPU; the limits of
// the container are only in its cgroup. The zero value means "no limit found".
type cgroupLimits struct {
	// MemoryLimit is the memory limit in bytes, 0 when there is none.
	MemoryLimit uint64
	// MemoryUsed is the memory in use inside the limit, the page cache that
	// can be given back not counted (what `docker stats` shows). HaveMemoryUsed
	// says it was read.
	MemoryUsed     uint64
	HaveMemoryUsed bool
	// CPUQuota is the CPU time the group may use, in CPUs (1.5 for one and a
	// half), 0 when there is none.
	CPUQuota float64
	// CPUUsageUsec is the CPU time the group has used since it was created, in
	// microseconds (usage_usec of cpu.stat, or cpuacct.usage of v1 over 1000);
	// HaveCPUUsage says it was read. Two readings give the share of CPU the
	// group used between them (#355).
	CPUUsageUsec uint64
	HaveCPUUsage bool
	// SwapLimit is the swap the group may use, in bytes; 0 with HaveSwapLimit
	// means none (the limit is 0), and without HaveSwapLimit there is no limit.
	// SwapUsed is the swap in use, read when HaveSwapUsed is set.
	SwapLimit     uint64
	HaveSwapLimit bool
	SwapUsed      uint64
	HaveSwapUsed  bool
}

// cpuCount is the number of CPUs to report for a host with hostCPUs: the quota
// rounded up, when it is lower.
func (l cgroupLimits) cpuCount(hostCPUs int) int {
	if l.CPUQuota <= 0 {
		return hostCPUs
	}
	n := int(math.Ceil(l.CPUQuota - 1e-9))
	if n < 1 {
		n = 1
	}
	return min(n, hostCPUs)
}

// readCgroupLimits reads the limits of the cgroup of this process: cgroup v2
// (memory.max, memory.current, cpu.max) and, where v1 is still in use, its
// equivalents (memory.limit_in_bytes, memory.usage_in_bytes, cpu.cfs_quota_us).
// proc is "/proc" and root the cgroup mount, except in tests. The limit of an
// enclosing group counts too: the lowest one wins, as the kernel applies it.
func readCgroupLimits(proc, root string) cgroupLimits {
	self := readFile(filepath.Join(proc, "self/cgroup"))
	var l cgroupLimits
	if v2Path, ok := cgroupV2Path(self); ok && fileExists(filepath.Join(root, "cgroup.controllers")) {
		readV2(&l, root, v2Path)
		return l
	}
	readV1(&l, root, self)
	return l
}

// cgroupV2Path is the path of the "0::/path" line of /proc/self/cgroup.
func cgroupV2Path(self string) (string, bool) {
	for _, line := range strings.Split(self, "\n") {
		if path, ok := strings.CutPrefix(line, "0::"); ok {
			return strings.TrimSpace(path), true
		}
	}
	return "", false
}

// groupDirs lists the directories of a cgroup and of its ancestors, deepest
// first, ending with base. A container with its own cgroup namespace sees its
// group as the root of the mount; if the path of the group is not under the
// mount (a namespace that hides it), only base is used.
func groupDirs(base, path string) []string {
	dir := filepath.Join(base, filepath.Clean("/"+path))
	if !fileExists(dir) {
		return []string{base}
	}
	var dirs []string
	for {
		dirs = append(dirs, dir)
		if dir == base || len(dir) <= len(base) {
			break
		}
		dir = filepath.Dir(dir)
	}
	return dirs
}

func readV2(l *cgroupLimits, root, path string) {
	dirs := groupDirs(root, path)
	for i, dir := range dirs {
		if max, ok := readUintFile(filepath.Join(dir, "memory.max")); ok && max > 0 && (l.MemoryLimit == 0 || max < l.MemoryLimit) {
			l.MemoryLimit = max
		}
		if i == 0 {
			if cur, ok := readUintFile(filepath.Join(dir, "memory.current")); ok {
				l.MemoryUsed, l.HaveMemoryUsed = cur, true
				if inactive, ok := statValue(filepath.Join(dir, "memory.stat"), "inactive_file"); ok && inactive < cur {
					l.MemoryUsed = cur - inactive
				}
			}
		}
		if q := parseCPUMax(readFile(filepath.Join(dir, "cpu.max"))); q > 0 && (l.CPUQuota == 0 || q < l.CPUQuota) {
			l.CPUQuota = q
		}
		if i == 0 {
			if usec, ok := statValue(filepath.Join(dir, "cpu.stat"), "usage_usec"); ok {
				l.CPUUsageUsec, l.HaveCPUUsage = usec, true
			}
			if cur, ok := readUintFile(filepath.Join(dir, "memory.swap.current")); ok {
				l.SwapUsed, l.HaveSwapUsed = cur, true
			}
		}
		// "max" is not a number, so a group without a swap limit is skipped.
		if max, ok := readUintFile(filepath.Join(dir, "memory.swap.max")); ok && (!l.HaveSwapLimit || max < l.SwapLimit) {
			l.SwapLimit, l.HaveSwapLimit = max, true
		}
	}
}

// v1Path is the path of the group of a controller in /proc/self/cgroup, whose
// lines are "id:controller,controller:/path".
func v1Path(self, controller string) string {
	for _, line := range strings.Split(self, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		for _, c := range strings.Split(parts[1], ",") {
			if c == controller {
				return strings.TrimSpace(parts[2])
			}
		}
	}
	return "/"
}

// v1Mount is the directory a v1 controller is mounted at: its own, or one
// shared with others (cpu,cpuacct).
func v1Mount(root, controller string) string {
	for _, name := range []string{controller, controller + ",cpuacct", "cpu," + controller} {
		if dir := filepath.Join(root, name); fileExists(dir) {
			return dir
		}
	}
	return ""
}

func readV1(l *cgroupLimits, root, self string) {
	if mem := v1Mount(root, "memory"); mem != "" {
		for i, dir := range groupDirs(mem, v1Path(self, "memory")) {
			// "No limit" is a huge number, not a missing file.
			if max, ok := readUintFile(filepath.Join(dir, "memory.limit_in_bytes")); ok && max > 0 && max < 1<<60 && (l.MemoryLimit == 0 || max < l.MemoryLimit) {
				l.MemoryLimit = max
			}
			if i == 0 {
				if cur, ok := readUintFile(filepath.Join(dir, "memory.usage_in_bytes")); ok {
					l.MemoryUsed, l.HaveMemoryUsed = cur, true
					if inactive, ok := statValue(filepath.Join(dir, "memory.stat"), "total_inactive_file"); ok && inactive < cur {
						l.MemoryUsed = cur - inactive
					}
				}
				readV1Swap(l, dir)
			}
		}
	}
	if acct := v1Mount(root, "cpuacct"); acct != "" {
		if ns, ok := readUintFile(filepath.Join(acct, filepath.Clean("/"+v1Path(self, "cpuacct")), "cpuacct.usage")); ok {
			l.CPUUsageUsec, l.HaveCPUUsage = ns/1000, true
		}
	}
	if cpu := v1Mount(root, "cpu"); cpu != "" {
		for _, dir := range groupDirs(cpu, v1Path(self, "cpu")) {
			quota, okQ := readIntFile(filepath.Join(dir, "cpu.cfs_quota_us"))
			period, okP := readIntFile(filepath.Join(dir, "cpu.cfs_period_us"))
			if okQ && okP && quota > 0 && period > 0 {
				if q := float64(quota) / float64(period); l.CPUQuota == 0 || q < l.CPUQuota {
					l.CPUQuota = q
				}
			}
		}
	}
}

// readV1Swap reads the swap of a v1 memory group. v1 counts memory plus swap
// together (memsw), so the swap is what memsw has beyond the memory figure.
// Without the memsw files (the kernel was booted without swap accounting)
// nothing is read.
func readV1Swap(l *cgroupLimits, dir string) {
	if memsw, ok := readUintFile(filepath.Join(dir, "memory.memsw.usage_in_bytes")); ok {
		if mem, ok := readUintFile(filepath.Join(dir, "memory.usage_in_bytes")); ok {
			l.SwapUsed, l.HaveSwapUsed = memsw-min(memsw, mem), true
		}
	}
	memswMax, ok1 := readUintFile(filepath.Join(dir, "memory.memsw.limit_in_bytes"))
	memMax, ok2 := readUintFile(filepath.Join(dir, "memory.limit_in_bytes"))
	if ok1 && ok2 && memswMax < 1<<60 && memMax < 1<<60 {
		l.SwapLimit, l.HaveSwapLimit = memswMax-min(memswMax, memMax), true
	}
}

// parseCPUMax reads cpu.max of cgroup v2: "max 100000" (no limit) or
// "150000 100000" (quota and period in microseconds). It returns the quota in
// CPUs, 0 for none or for something unreadable.
func parseCPUMax(content string) float64 {
	fields := strings.Fields(content)
	if len(fields) != 2 || fields[0] == "max" {
		return 0
	}
	quota, err1 := strconv.ParseFloat(fields[0], 64)
	period, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil || quota <= 0 || period <= 0 {
		return 0
	}
	return quota / period
}

// readUintFile reads a file with one number; "max" and anything else that is
// not a non-negative integer is reported as not read.
func readUintFile(path string) (uint64, bool) {
	data, ok := readFileOK(path)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(data), 10, 64)
	return n, err == nil
}

func readIntFile(path string) (int64, bool) {
	data, ok := readFileOK(path)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(data), 10, 64)
	return n, err == nil
}

// statValue is the value of a key in a "key value" file such as memory.stat.
func statValue(path, key string) (uint64, bool) {
	data, ok := readFileOK(path)
	if !ok {
		return 0, false
	}
	for _, line := range strings.Split(data, "\n") {
		if name, value, ok := strings.Cut(line, " "); ok && name == key {
			n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
