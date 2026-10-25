package runtimeresources

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

const (

	// CgroupV2FSType is printed when $cg/cgroup.controllers exists; `stat -fc %T`
	// is avoided because BusyBox stat reports every type as "UNKNOWN".
	CgroupV2FSType = "cgroup2fs"

	// DefaultCgroupRoot is the container's own cgroup v2 directory.
	DefaultCgroupRoot = "/sys/fs/cgroup"
)

func RuntimeCgroupV2Available(values map[string]string) bool {
	return values["cgroup_type"] == CgroupV2FSType
}

func RuntimeMemoryUsageFromValues(v map[string]string) model.RuntimeMemoryUsage {
	m := model.RuntimeMemoryUsage{}
	if !RuntimeCgroupV2Available(v) {
		m.Unavailable = "cgroup v2 not detected under /sys/fs/cgroup; memory usage needs memory.current/memory.max"
		return m
	}
	current, ok := core.ParseRuntimeInt64(v["memory_current"])
	if !ok {
		m.Unavailable = "memory.current was not readable"
		return m
	}
	m.CurrentBytes = current
	if peak, ok := core.ParseRuntimeInt64(v["memory_peak"]); ok {
		m.PeakBytes = peak
		m.PeakObserved = true
	}
	if killed, ok := core.ParseRuntimeInt64(v["memory_oom_kill"]); ok {
		m.OOMKills = killed
		m.OOMKillsObserved = true
	}
	if hits, ok := core.ParseRuntimeInt64(v["memory_ceiling_hits"]); ok {
		m.CeilingHits = hits
		m.CeilingHitsObserved = true
	}
	maxRaw := v["memory_max"]
	if maxRaw == "max" {
		m.Unlimited = true
		return m
	}
	limit, ok := core.ParseRuntimeInt64(maxRaw)
	if !ok {
		m.Unavailable = "memory.max was not readable"
		return m
	}
	m.LimitBytes = limit
	if limit > 0 {
		m.PercentOfLimit = 100 * float64(current) / float64(limit)
	}
	return m
}

type runtimeCPUCounters struct {
	QuotaCores       float64
	UsageUsec        int64
	Periods          int64
	ThrottledPeriods int64
}

func RuntimeCPUCountersFromValues(v map[string]string) (runtimeCPUCounters, bool) {
	var counters runtimeCPUCounters
	if usage, ok := core.ParseRuntimeInt64(v["cpu_usage_after"]); ok {
		counters.UsageUsec = usage
	}
	if periods, ok := core.ParseRuntimeInt64(v["cpu_periods"]); ok {
		counters.Periods = periods
	}
	if throttled, ok := core.ParseRuntimeInt64(v["cpu_throttled_periods"]); ok {
		counters.ThrottledPeriods = throttled
	}
	quotaUsec, periodUsec, ok := core.ParseRuntimeCPUMax(v["cpu_max"])
	if !ok {
		return counters, false
	}
	counters.QuotaCores = float64(quotaUsec) / float64(periodUsec)
	return counters, true
}

// ReadLocalRuntimeUsage reads the container's own cgroup files through the same
// parser as RunRuntimeUsage. It never fails: unreadable files report unavailable.
func ReadLocalRuntimeUsage(cgroupRoot string) model.RuntimeUsage {
	root := strings.TrimSpace(cgroupRoot)
	if root == "" {
		root = DefaultCgroupRoot
	}
	values := readLocalCgroupValues(root)
	return model.RuntimeUsage{
		CPU:    localRuntimeCPUUsage(values),
		Memory: RuntimeMemoryUsageFromValues(values),
	}
}

// readLocalCgroupValues produces the same map the exec'd script prints, so both
// paths share one parser.
func readLocalCgroupValues(root string) map[string]string {
	return map[string]string{
		"cgroup_type":           CgroupV2FSType,
		"memory_current":        readLocalCgroupFile(filepath.Join(root, "memory.current")),
		"memory_max":            readLocalCgroupFile(filepath.Join(root, "memory.max")),
		"memory_peak":           readLocalCgroupFile(filepath.Join(root, "memory.peak")),
		"memory_oom_kill":       localCgroupStatValue(filepath.Join(root, "memory.events"), "oom_kill"),
		"memory_ceiling_hits":   localCgroupStatValue(filepath.Join(root, "memory.events"), "max"),
		"cpu_max":               readLocalCgroupFile(filepath.Join(root, "cpu.max")),
		"cpu_usage_after":       localCgroupStatValue(filepath.Join(root, "cpu.stat"), "usage_usec"),
		"cpu_periods":           localCgroupStatValue(filepath.Join(root, "cpu.stat"), "nr_periods"),
		"cpu_throttled_periods": localCgroupStatValue(filepath.Join(root, "cpu.stat"), "nr_throttled"),
	}
}

func readLocalCgroupFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func localCgroupStatValue(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == key {
			return fields[1]
		}
	}
	return ""
}

// localRuntimeCPUUsage reports only cumulative counters: a single local read has
// no interval to compute a rate from.
func localRuntimeCPUUsage(v map[string]string) model.RuntimeCPUUsage {
	c := model.RuntimeCPUUsage{}
	if !RuntimeCgroupV2Available(v) {
		c.Unavailable = "cgroup v2 not detected under /sys/fs/cgroup; CPU usage needs cpu.max/cpu.stat"
		return c
	}
	counters, ok := RuntimeCPUCountersFromValues(v)
	c.UsageUsec = counters.UsageUsec
	c.Periods = counters.Periods
	c.ThrottledPeriods = counters.ThrottledPeriods
	if !ok {
		c.Unavailable = "cpu.max reports no quota (unlimited or not readable); utilisation needs a quota to measure against"
		return c
	}
	c.QuotaCores = counters.QuotaCores
	return c
}
