package agent

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
)

// systemInfo describes this machine for Hello. Fields that cannot be read
// (for example on a system without /proc) are left empty.
func systemInfo() *agentv1.SystemInfo {
	hostname, _ := os.Hostname()
	info := &agentv1.SystemInfo{
		Hostname:         hostname,
		Os:               runtime.GOOS,
		OsVersion:        parseOSRelease(readFile("/etc/os-release")),
		Kernel:           strings.TrimSpace(readFile("/proc/sys/kernel/osrelease")),
		Arch:             runtime.GOARCH,
		CpuCount:         uint32(runtime.NumCPU()),
		MemoryTotalBytes: parseMemTotal(readFile("/proc/meminfo")),
		Container:        detectContainer("/"),
	}
	if info.Container != "" {
		applyContainerLimits(info, readCgroupLimits("/proc", cgroupRoot))
	}
	return info
}

// applyContainerLimits makes the memory and the CPU count of a container's
// system information follow its cgroup limits instead of the host's (#325).
func applyContainerLimits(info *agentv1.SystemInfo, lim cgroupLimits) {
	if lim.MemoryLimit > 0 && (info.MemoryTotalBytes == 0 || lim.MemoryLimit < info.MemoryTotalBytes) {
		info.MemoryTotalBytes = lim.MemoryLimit
	}
	info.CpuCount = uint32(lim.cpuCount(int(info.CpuCount)))
}

// detectContainer names the container the agent runs in, or returns "" when
// it finds none. It looks where the container managers leave a mark: systemd
// writes the name to /run/systemd/container (for LXC, nspawn, Docker and
// Podman with systemd inside), Docker creates /.dockerenv and Podman
// /run/.containerenv. root is "/" except in tests. It does not read
// /proc/1/environ, which an agent that is not root cannot open.
func detectContainer(root string) string {
	if name := strings.TrimSpace(readFile(filepath.Join(root, "run/systemd/container"))); name != "" {
		return name
	}
	if _, err := os.Stat(filepath.Join(root, ".dockerenv")); err == nil {
		return "docker"
	}
	if _, err := os.Stat(filepath.Join(root, "run/.containerenv")); err == nil {
		return "podman"
	}
	return ""
}

func readFile(path string) string {
	data, _ := readFileOK(path)
	return data
}

// readFileOK is readFile that also tells whether the file could be read.
func readFileOK(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// parseOSRelease returns PRETTY_NAME from the content of /etc/os-release.
func parseOSRelease(content string) string {
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := sc.Text()
		if value, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(value, `"'`)
		}
	}
	return ""
}

// parseMemTotal returns MemTotal, in bytes, from the content of /proc/meminfo.
func parseMemTotal(content string) uint64 {
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		rest, ok := strings.CutPrefix(sc.Text(), "MemTotal:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest) // for example ["16318464", "kB"]
		if len(fields) == 0 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

// parseUptime returns the whole seconds in the content of /proc/uptime.
func parseUptime(content string) uint64 {
	fields := strings.Fields(content) // for example ["350735.47", "234388.90"]
	if len(fields) == 0 {
		return 0
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || seconds < 0 {
		return 0
	}
	return uint64(seconds)
}

// Collector gathers the metrics sent to the hub.
type Collector interface {
	Collect(ctx context.Context) (*agentv1.MetricsReport, error)
}

// Names of the collectors and of the parts of a report, as sent in
// MetricsReport.collector and MetricsReport.unavailable.
const (
	collectorLinux = "linux"
	collectorBasic = "basic"

	partCPU     = "cpu"
	partLoad    = "load"
	partMemory  = "memory"
	partDisks   = "disks"
	partNetwork = "network"
	partUptime  = "uptime"
)

// basicCollector reports only the time and the uptime. It is the fallback on
// systems where /proc cannot be read, and says in every report that the rest
// is unavailable, so that the dashboard shows "not available" rather than
// zeros.
type basicCollector struct{}

func (basicCollector) Collect(context.Context) (*agentv1.MetricsReport, error) {
	r := &agentv1.MetricsReport{
		CollectedAt: timestamppb.New(time.Now()),
		Collector:   collectorBasic,
		Unavailable: []string{partCPU, partLoad, partMemory, partDisks, partNetwork},
	}
	if uptime, ok := readFileOK("/proc/uptime"); ok {
		r.UptimeSeconds = parseUptime(uptime)
	} else {
		r.Unavailable = append(r.Unavailable, partUptime)
	}
	return r, nil
}

// defaultCollector is the Linux collector when /proc is available, and the
// basic one otherwise.
func defaultCollector() Collector {
	if _, err := parseCPUTimes(readFile("/proc/stat")); err != nil {
		return basicCollector{}
	}
	return NewLinuxCollector()
}
