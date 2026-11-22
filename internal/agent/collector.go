package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
)

// realFilesystems are the filesystem types reported as disks. Pseudo
// filesystems (proc, tmpfs, overlay, cgroup, ...) are left out on purpose, and
// so are network filesystems (nfs, nfs4, cifs, smb3, ...): their space belongs
// to the server that exports them, which reports it with its own agent, and a
// share whose server is gone would only show as a mount that does not answer.
// A container whose root is overlay therefore reports no disk; see
// docs/remote-agents.md, "Supported platforms".
var realFilesystems = []string{
	"ext2", "ext3", "ext4", "xfs", "btrfs", "zfs", "f2fs", "jfs", "reiserfs",
	"vfat", "exfat", "ntfs", "ntfs3", "fuseblk",
}

// statfsFunc returns the total and used bytes of the filesystem mounted at path.
type statfsFunc func(path string) (total, used uint64, err error)

// LinuxCollector reads CPU, memory, disk, network and uptime metrics from
// /proc and statfs. CPU usage is computed between two consecutive calls, so
// the first report covers the time since the collector was created; when that
// is too short to mean anything (the first report usually follows at once),
// it is the average since boot instead.
type LinuxCollector struct {
	proc   string
	statfs statfsFunc
	// cgroup, if set, reads the limits of the control group; it is set only
	// when the agent runs in a container (#325), where /proc shows the host.
	cgroup func() cgroupLimits

	mu      sync.Mutex
	prevCPU cpuTimes
	// prevGroup is the last reading of the CPU time of the container's cgroup
	// (#355); now and hostCPUs are replaced in tests.
	prevGroup cgroupCPUSample
	now       func() time.Time
	hostCPUs  func() int

	// statTimeout is how long a report waits for the disks. A filesystem that
	// does not answer (a network mount whose server is gone, dead storage) can
	// block statfs for as long as it stays that way, in a state no signal
	// ends; the report goes out without it.
	statTimeout time.Duration
	statMu      sync.Mutex
	stats       map[string]*statJob // statfs calls that were started and not collected yet, by mount point
	hung        map[string]bool     // mounts already reported as not answering
}

// statJob is one statfs call, run on its own goroutine.
type statJob struct {
	done        chan struct{}
	total, used uint64
	err         error
}

// defaultStatTimeout is well below the heartbeat interval times the number of
// missed heartbeats the hub tolerates.
const defaultStatTimeout = 2 * time.Second

// NewLinuxCollector returns a collector that reads /proc.
func NewLinuxCollector() *LinuxCollector {
	c := newLinuxCollector("/proc", diskUsage)
	if detectContainer("/") != "" {
		c.cgroup = func() cgroupLimits { return readCgroupLimits("/proc", cgroupRoot) }
		c.groupCPUUsage(c.cgroup()) // the first reading, to compare the next one with
	}
	return c
}

func newLinuxCollector(proc string, statfs statfsFunc) *LinuxCollector {
	c := &LinuxCollector{proc: proc, statfs: statfs, statTimeout: defaultStatTimeout, stats: map[string]*statJob{}, hung: map[string]bool{}, now: time.Now, hostCPUs: runtime.NumCPU}
	if t, err := parseCPUTimes(c.read("stat")); err == nil {
		c.prevCPU = t
	}
	return c
}

func (c *LinuxCollector) read(name string) string {
	data, _ := readFileOK(filepath.Join(c.proc, name))
	return data
}

// Collect gathers a report. A source that cannot be read leaves its part of the
// report empty and is named in the report's unavailable list, so that the hub
// can tell "cannot read" from "nothing there"; it fails only when neither the
// CPU nor the memory can be read (for example on a system without /proc).
func (c *LinuxCollector) Collect(context.Context) (*agentv1.MetricsReport, error) {
	now, err := parseCPUTimes(c.read("stat"))
	var usage float64
	c.mu.Lock()
	if err == nil {
		prev := c.prevCPU
		if now.total-prev.total < minCPUJiffies {
			prev = cpuTimes{} // too short an interval: average since boot
		}
		usage = cpuUsagePercent(prev, now)
		c.prevCPU = now
	}
	c.mu.Unlock()

	groupCPU := false
	mem, memErr := parseMemInfo(c.read("meminfo"))
	if err != nil && memErr != nil {
		return nil, fmt.Errorf("cannot read /proc: %w", errors.Join(err, memErr))
	}

	if c.cgroup != nil {
		lim := c.cgroup()
		if memErr == nil {
			applyMemoryLimit(mem, lim)
			applySwapLimit(mem, lim)
		}
		if share, ok := c.groupCPUUsage(lim); ok {
			// The container's own figure replaces the host's (#355).
			usage = share
			groupCPU = true
		}
	}

	report := &agentv1.MetricsReport{
		CollectedAt: timestamppb.New(time.Now()),
		Collector:   collectorLinux,
		Cpu:         &agentv1.CpuMetrics{UsagePercent: usage},
		Memory:      mem,
	}
	if err != nil && !groupCPU {
		report.Unavailable = append(report.Unavailable, partCPU)
	}
	if loadavg, ok := c.readOK("loadavg"); ok {
		report.Cpu.Load_1, report.Cpu.Load_5, report.Cpu.Load_15 = parseLoadAvg(loadavg)
	} else {
		report.Unavailable = append(report.Unavailable, partLoad)
	}
	if memErr != nil {
		report.Unavailable = append(report.Unavailable, partMemory)
	}
	if mounts, ok := c.readOK("mounts"); ok {
		report.Disks = c.disks(mounts)
	} else {
		report.Unavailable = append(report.Unavailable, partDisks)
	}
	if netDev, ok := c.readOK("net/dev"); ok {
		report.Networks = parseNetDev(netDev)
	} else {
		report.Unavailable = append(report.Unavailable, partNetwork)
	}
	if uptime, ok := c.readOK("uptime"); ok {
		report.UptimeSeconds = parseUptime(uptime)
	} else {
		report.Unavailable = append(report.Unavailable, partUptime)
	}
	return report, nil
}

// applyMemoryLimit replaces the host's memory figures with the container's
// when its cgroup has a memory limit lower than the host's memory. Swap stays
// as /proc shows it. Without a limit, or without a reading of the use, the
// figures stay the host's.
func applyMemoryLimit(mem *agentv1.MemoryMetrics, lim cgroupLimits) {
	if mem == nil || lim.MemoryLimit == 0 || lim.MemoryLimit >= mem.TotalBytes || !lim.HaveMemoryUsed {
		return
	}
	used := min(lim.MemoryUsed, lim.MemoryLimit)
	mem.TotalBytes = lim.MemoryLimit
	mem.UsedBytes = used
	mem.AvailableBytes = lim.MemoryLimit - used
}

func (c *LinuxCollector) readOK(name string) (string, bool) {
	return readFileOK(filepath.Join(c.proc, name))
}

func (c *LinuxCollector) disks(mountsFile string) []*agentv1.DiskMetrics {
	mounts := parseMounts(mountsFile)
	// Ask every mount at the same time and wait for all of them, once, up to
	// statTimeout: the report takes as long as the slowest answer, or that
	// limit, not the sum of them.
	jobs := make([]*statJob, len(mounts))
	for i, m := range mounts {
		jobs[i] = c.startStat(m.mountPoint)
	}
	deadline := time.NewTimer(c.statTimeout)
	defer deadline.Stop()
	var disks []*agentv1.DiskMetrics
	expired := false
	for i, m := range mounts {
		job := jobs[i]
		if job == nil {
			continue // still stuck from an earlier report
		}
		if !expired {
			select {
			case <-job.done:
			case <-deadline.C:
				expired = true
			}
		}
		select {
		case <-job.done:
			c.finishStat(m.mountPoint, job)
		default:
			c.noteHung(m.mountPoint)
			continue
		}
		if job.err != nil || job.total == 0 {
			continue
		}
		disks = append(disks, &agentv1.DiskMetrics{
			MountPoint: m.mountPoint, Filesystem: m.fsType, TotalBytes: job.total, UsedBytes: job.used,
		})
	}
	return disks
}

// startStat starts statfs on the mount, unless an earlier call on it has not
// returned: another call would only add one more stuck thread per report. It
// returns nil in that case, and the mount is left out of the report until the
// earlier call returns.
func (c *LinuxCollector) startStat(mount string) *statJob {
	c.statMu.Lock()
	defer c.statMu.Unlock()
	if old := c.stats[mount]; old != nil {
		select {
		case <-old.done: // it came back since: its answer is stale, ask again
			delete(c.stats, mount)
			delete(c.hung, mount)
		default:
			return nil
		}
	}
	job := &statJob{done: make(chan struct{})}
	c.stats[mount] = job
	go func() {
		job.total, job.used, job.err = c.statfs(mount)
		close(job.done)
	}()
	return job
}

// finishStat forgets a call whose answer was used.
func (c *LinuxCollector) finishStat(mount string, job *statJob) {
	c.statMu.Lock()
	defer c.statMu.Unlock()
	if c.stats[mount] == job {
		delete(c.stats, mount)
		delete(c.hung, mount)
	}
}

// noteHung says once per stuck mount that it is left out of the reports.
func (c *LinuxCollector) noteHung(mount string) {
	c.statMu.Lock()
	first := !c.hung[mount]
	c.hung[mount] = true
	c.statMu.Unlock()
	if first {
		slog.Warn("a mounted filesystem does not answer; it is left out of the reports until it does", "mount", mount)
	}
}

// cpuTimes are the cumulative jiffies of the aggregate "cpu" line of /proc/stat.
type cpuTimes struct {
	busy, total uint64
}

// parseCPUTimes reads the aggregate "cpu" line of /proc/stat. Idle time
// includes iowait; guest time is already counted in user and nice.
func parseCPUTimes(content string) (cpuTimes, error) {
	for line := range strings.SplitSeq(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var v [8]uint64 // user nice system idle iowait irq softirq steal
		for i := range v {
			if i+1 >= len(fields) {
				break
			}
			n, err := strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				return cpuTimes{}, fmt.Errorf("invalid cpu line %q: %w", line, err)
			}
			v[i] = n
		}
		// The sums are checked: counters that do not fit together in 64 bits
		// are not from a real kernel, and a wrapped total would make busy
		// time larger than the total.
		var total uint64
		for _, n := range v {
			if total+n < total {
				return cpuTimes{}, fmt.Errorf("invalid cpu line %q: counters overflow", line)
			}
			total += n
		}
		idle := v[3] + v[4] // cannot overflow: both are part of total
		return cpuTimes{busy: total - idle, total: total}, nil
	}
	return cpuTimes{}, errors.New("no cpu line in /proc/stat")
}

// minCPUJiffies is the shortest interval, in clock ticks summed over all CPUs
// (100 a second per CPU on most kernels), over which a CPU share is computed.
// Over a few ticks one busy tick reads as 50% on an idle server.
const minCPUJiffies = 100

// cpuUsagePercent is the share of busy time between two samples, 0 to 100.
func cpuUsagePercent(prev, now cpuTimes) float64 {
	if now.total <= prev.total || now.busy < prev.busy {
		return 0
	}
	pct := 100 * float64(now.busy-prev.busy) / float64(now.total-prev.total)
	return min(max(pct, 0), 100)
}

// parseLoadAvg returns the 1, 5 and 15 minute load averages from /proc/loadavg.
func parseLoadAvg(content string) (l1, l5, l15 float64) {
	fields := strings.Fields(content)
	if len(fields) < 3 {
		return 0, 0, 0
	}
	l1, _ = strconv.ParseFloat(fields[0], 64)
	l5, _ = strconv.ParseFloat(fields[1], 64)
	l15, _ = strconv.ParseFloat(fields[2], 64)
	return l1, l5, l15
}

// parseMemInfo reads /proc/meminfo. Used memory is total minus available.
func parseMemInfo(content string) (*agentv1.MemoryMetrics, error) {
	values := map[string]uint64{}
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if len(fields) > 1 && fields[1] == "kB" {
			if n > math.MaxUint64/1024 {
				continue // does not fit in bytes: not a real value
			}
			n *= 1024
		}
		values[name] = n
	}
	total, ok := values["MemTotal"]
	if !ok {
		return nil, errors.New("no MemTotal in /proc/meminfo")
	}
	available, ok := values["MemAvailable"]
	if !ok { // kernels before 3.14
		available = saturatingSum(values["MemFree"], values["Buffers"], values["Cached"])
	}
	available = min(available, total)
	swapTotal := values["SwapTotal"]
	swapFree := min(values["SwapFree"], swapTotal)
	return &agentv1.MemoryMetrics{
		TotalBytes:     total,
		UsedBytes:      total - available,
		AvailableBytes: available,
		SwapTotalBytes: swapTotal,
		SwapUsedBytes:  swapTotal - swapFree,
	}, nil
}

// saturatingSum adds the values, stopping at the largest uint64 instead of
// wrapping around to a small number.
func saturatingSum(values ...uint64) uint64 {
	var sum uint64
	for _, v := range values {
		if sum+v < sum {
			return math.MaxUint64
		}
		sum += v
	}
	return sum
}

type mount struct {
	device, mountPoint, fsType string
}

// parseMounts returns the mounts of real filesystems from /proc/mounts, one
// per device (bind mounts and subvolumes of the same device are reported once,
// at the shortest mount point).
func parseMounts(content string) []mount {
	var mounts []mount
	seen := map[string]int{} // device -> index in mounts
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || !slices.Contains(realFilesystems, fields[2]) {
			continue
		}
		m := mount{device: fields[0], mountPoint: unescapeMount(fields[1]), fsType: fields[2]}
		if i, ok := seen[m.device]; ok {
			if len(m.mountPoint) < len(mounts[i].mountPoint) {
				mounts[i] = m
			}
			continue
		}
		seen[m.device] = len(mounts)
		mounts = append(mounts, m)
	}
	return mounts
}

// unescapeMount decodes the octal escapes /proc/mounts uses for spaces and
// other special characters in paths.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// parseNetDev returns the received and transmitted byte counters of every
// interface except loopback, from /proc/net/dev. A line without a name, and a
// name already seen, are skipped: the hub matches the counters of two reports
// by name to compute a rate.
func parseNetDev(content string) []*agentv1.NetworkMetrics {
	var nets []*agentv1.NetworkMetrics
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok { // the two header lines have no colon
			continue
		}
		name = strings.TrimSpace(name)
		fields := strings.Fields(rest) // rx: bytes packets errs drop fifo frame compressed multicast; tx: bytes ...
		if name == "" || name == "lo" || seen[name] || len(fields) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(fields[0], 10, 64)
		tx, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		seen[name] = true
		nets = append(nets, &agentv1.NetworkMetrics{Interface: name, RxBytes: rx, TxBytes: tx})
	}
	return nets
}

// cgroupCPUSample is a reading of the CPU time of the container's cgroup.
type cgroupCPUSample struct {
	usec uint64
	at   time.Time
}

// minGroupInterval is the shortest interval over which the container's CPU
// share is computed; over less, one scheduler tick reads as a large share.
const minGroupInterval = time.Second

// groupCPUUsage is the share of its CPUs the container used since the last
// report, 0 to 100: the CPU time of its cgroup over the interval, divided by
// the quota, or by the CPUs it may run on when it has no quota (#355). ok is
// false when the cgroup gives no reading, on the first report (nothing to
// compare with yet) and over too short an interval; the host's figure from
// /proc/stat then stays, and the previous reading is kept so the next report
// covers a longer interval.
func (c *LinuxCollector) groupCPUUsage(lim cgroupLimits) (float64, bool) {
	if !lim.HaveCPUUsage {
		return 0, false
	}
	at := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	prev := c.prevGroup
	if prev.at.IsZero() {
		c.prevGroup = cgroupCPUSample{usec: lim.CPUUsageUsec, at: at}
		return 0, false
	}
	elapsed := at.Sub(prev.at)
	if elapsed < minGroupInterval || lim.CPUUsageUsec < prev.usec {
		if lim.CPUUsageUsec < prev.usec { // the counter went back: a new group
			c.prevGroup = cgroupCPUSample{usec: lim.CPUUsageUsec, at: at}
		}
		return 0, false
	}
	cpus := lim.CPUQuota
	if cpus <= 0 {
		cpus = float64(c.hostCPUs())
	}
	pct := 100 * float64(lim.CPUUsageUsec-prev.usec) / (float64(elapsed.Microseconds()) * cpus)
	c.prevGroup = cgroupCPUSample{usec: lim.CPUUsageUsec, at: at}
	return min(max(pct, 0), 100), true
}

// applySwapLimit replaces the host's swap figures with the container's when
// its cgroup says how much swap it may use or has used. A group with a swap
// limit shows that limit (never more than the host has); a group without one
// keeps the host's total and shows its own use.
func applySwapLimit(mem *agentv1.MemoryMetrics, lim cgroupLimits) {
	if mem == nil || !lim.HaveSwapUsed {
		return
	}
	total := mem.SwapTotalBytes
	if lim.HaveSwapLimit {
		total = min(total, lim.SwapLimit)
	}
	mem.SwapTotalBytes = total
	mem.SwapUsedBytes = min(lim.SwapUsed, total)
}
