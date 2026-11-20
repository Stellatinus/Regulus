package agent

import (
	"context"
	"path/filepath"
	"testing"

	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
)

const (
	gib = 1 << 30
	mib = 1 << 20
)

// TestCgroupV2Limits is #325 on a cgroup v2 fixture: a Docker container whose
// cgroup namespace shows its own group as the root of the mount.
func TestCgroupV2Limits(t *testing.T) {
	proc, root := t.TempDir(), t.TempDir()
	writeProc(t, proc, map[string]string{"self/cgroup": "0::/\n"})
	writeProc(t, root, map[string]string{
		"cgroup.controllers": "cpu memory\n",
		"memory.max":         "536870912\n", // 512 MiB
		"memory.current":     "314572800\n", // 300 MiB
		"memory.stat":        "anon 1\ninactive_file 104857600\nactive_file 5\n",
		"cpu.max":            "150000 100000\n",
	})
	got := readCgroupLimits(filepath.Join(proc), root)
	if got.MemoryLimit != 512*mib || !got.HaveMemoryUsed || got.MemoryUsed != 200*mib {
		t.Errorf("memory = limit %d used %d (read %v), want 512 MiB and 300-100 MiB", got.MemoryLimit, got.MemoryUsed, got.HaveMemoryUsed)
	}
	if got.CPUQuota != 1.5 || got.cpuCount(8) != 2 || got.cpuCount(1) != 1 {
		t.Errorf("cpu quota %v, count on 8 CPUs %d, on 1 CPU %d; want 1.5, 2, 1", got.CPUQuota, got.cpuCount(8), got.cpuCount(1))
	}
}

func TestCgroupV2WithoutLimits(t *testing.T) {
	proc, root := t.TempDir(), t.TempDir()
	writeProc(t, proc, map[string]string{"self/cgroup": "0::/\n"})
	writeProc(t, root, map[string]string{
		"cgroup.controllers": "cpu memory\n",
		"memory.max":         "max\n",
		"memory.current":     "1000\n",
		"cpu.max":            "max 100000\n",
	})
	got := readCgroupLimits(proc, root)
	if got.MemoryLimit != 0 || got.CPUQuota != 0 || got.cpuCount(4) != 4 {
		t.Errorf("limits = %+v, want none", got)
	}
}

// A group below the root: the limit of the group counts, and so does a lower
// one set on a group above it.
func TestCgroupV2NestedGroupsTakeTheLowestLimit(t *testing.T) {
	proc, root := t.TempDir(), t.TempDir()
	writeProc(t, proc, map[string]string{"self/cgroup": "0::/machine.slice/ct\n"})
	writeProc(t, root, map[string]string{
		"cgroup.controllers":              "cpu memory\n",
		"machine.slice/memory.max":        "1073741824\n", // 1 GiB on the slice
		"machine.slice/cpu.max":           "max 100000\n",
		"machine.slice/ct/memory.max":     "max\n", // none on the container itself
		"machine.slice/ct/memory.current": "1048576\n",
		"machine.slice/ct/cpu.max":        "200000 100000\n",
	})
	got := readCgroupLimits(proc, root)
	if got.MemoryLimit != gib || got.MemoryUsed != mib || got.CPUQuota != 2 {
		t.Errorf("limits = %+v, want 1 GiB from the slice, 1 MiB used, 2 CPUs", got)
	}
}

// TestCgroupV1Limits: where cgroup v1 is still in use.
func TestCgroupV1Limits(t *testing.T) {
	proc, root := t.TempDir(), t.TempDir()
	writeProc(t, proc, map[string]string{"self/cgroup": "12:memory:/docker/abc\n4:cpu,cpuacct:/docker/abc\n1:name=systemd:/docker/abc\n"})
	writeProc(t, root, map[string]string{
		"memory/docker/abc/memory.limit_in_bytes":  "268435456\n",
		"memory/docker/abc/memory.usage_in_bytes":  "134217728\n",
		"memory/docker/abc/memory.stat":            "cache 1\ntotal_inactive_file 33554432\n",
		"memory/memory.limit_in_bytes":             "9223372036854771712\n", // the root has no limit
		"cpu,cpuacct/docker/abc/cpu.cfs_quota_us":  "50000\n",
		"cpu,cpuacct/docker/abc/cpu.cfs_period_us": "100000\n",
		"cpu,cpuacct/cpu.cfs_quota_us":             "-1\n",
		"cpu,cpuacct/cpu.cfs_period_us":            "100000\n",
	})
	got := readCgroupLimits(proc, root)
	if got.MemoryLimit != 256*mib || got.MemoryUsed != 96*mib || got.CPUQuota != 0.5 || got.cpuCount(4) != 1 {
		t.Errorf("limits = %+v (count %d), want 256 MiB, 96 MiB used, half a CPU", got, got.cpuCount(4))
	}
}

// v1 with no limit at all reports none: the huge number means "unlimited".
func TestCgroupV1UnlimitedIsNoLimit(t *testing.T) {
	proc, root := t.TempDir(), t.TempDir()
	writeProc(t, proc, map[string]string{"self/cgroup": "5:memory:/\n"})
	writeProc(t, root, map[string]string{"memory/memory.limit_in_bytes": "9223372036854771712\n", "memory/memory.usage_in_bytes": "5\n"})
	if got := readCgroupLimits(proc, root); got.MemoryLimit != 0 {
		t.Errorf("limit = %d, want none", got.MemoryLimit)
	}
}

func TestNoCgroupFilesIsNoLimit(t *testing.T) {
	if got := readCgroupLimits(t.TempDir(), t.TempDir()); got != (cgroupLimits{}) {
		t.Errorf("limits = %+v, want none", got)
	}
}

// TestCollectorReportsTheContainersMemory is #325: in a cgroup-limited
// container the Memory widget gets the container's limit and use, not the
// host's; without a limit, or outside a container, the host's figures stay.
func TestCollectorReportsTheContainersMemory(t *testing.T) {
	proc := t.TempDir()
	writeProc(t, proc, map[string]string{"stat": statSample, "meminfo": meminfoSample})
	c := newLinuxCollector(proc, nil)
	host, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	c.cgroup = func() cgroupLimits {
		return cgroupLimits{MemoryLimit: 512 * mib, MemoryUsed: 200 * mib, HaveMemoryUsed: true}
	}
	r, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := r.GetMemory()
	if m.GetTotalBytes() != 512*mib || m.GetUsedBytes() != 200*mib || m.GetAvailableBytes() != 312*mib {
		t.Errorf("memory = %+v, want the container's 512 MiB with 200 MiB used", m)
	}
	if m.GetSwapTotalBytes() != host.GetMemory().GetSwapTotalBytes() {
		t.Errorf("swap changed: %d, want the host's %d", m.GetSwapTotalBytes(), host.GetMemory().GetSwapTotalBytes())
	}

	// A limit above the host's memory is not a limit; neither is no reading of the use.
	for name, lim := range map[string]cgroupLimits{
		"above the host": {MemoryLimit: 1 << 50, MemoryUsed: 5, HaveMemoryUsed: true},
		"no usage":       {MemoryLimit: 512 * mib},
		"no limit":       {},
	} {
		c.cgroup = func() cgroupLimits { return lim }
		r, err := c.Collect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if r.GetMemory().GetTotalBytes() != host.GetMemory().GetTotalBytes() {
			t.Errorf("%s: total = %d, want the host's %d", name, r.GetMemory().GetTotalBytes(), host.GetMemory().GetTotalBytes())
		}
	}
}

func TestContainerSystemInfoFollowsTheLimits(t *testing.T) {
	info := &agentv1.SystemInfo{CpuCount: 8, MemoryTotalBytes: 16 * gib}
	applyContainerLimits(info, cgroupLimits{MemoryLimit: 512 * mib, CPUQuota: 1.5})
	if info.CpuCount != 2 || info.MemoryTotalBytes != 512*mib {
		t.Errorf("info = %+v, want 2 CPUs and 512 MiB", info)
	}
	info = &agentv1.SystemInfo{CpuCount: 4, MemoryTotalBytes: 16 * gib}
	applyContainerLimits(info, cgroupLimits{})
	if info.CpuCount != 4 || info.MemoryTotalBytes != 16*gib {
		t.Errorf("without limits info = %+v, want the host's", info)
	}
}
