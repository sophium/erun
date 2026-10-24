package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

const (

	// CgroupV2FSType is printed when $cg/cgroup.controllers exists; `stat -fc %T`
	// is avoided because BusyBox stat reports every type as "UNKNOWN".
	CgroupV2FSType = "cgroup2fs"

	// DefaultCgroupRoot is the container's own cgroup v2 directory.
	DefaultCgroupRoot = "/sys/fs/cgroup"
)

// RuntimeUsage is a live reading scoped to the runtime container, not a job,
// so concurrent jobs cannot misattribute each other's consumption.
type RuntimeUsage struct {
	Tenant      string             `json:"tenant"`
	Environment string             `json:"environment"`
	CPU         RuntimeCPUUsage    `json:"cpu"`
	Memory      RuntimeMemoryUsage `json:"memory"`
	Disk        []RuntimeDiskUsage `json:"disk"`
	Warnings    []string           `json:"warnings,omitempty"`
	// ExcludesBuilds marks that CPU/Memory cannot see builds in the erun-dind sidecar.
	ExcludesBuilds bool `json:"excludesBuilds,omitempty"`
	// Requests is the scheduler reservation, as opposed to the cgroup ceiling. Nil
	// in dry-run; an unreadable pod spec arrives with Unavailable set.
	Requests *RuntimeUsageRequests `json:"requests,omitempty"`
	// Dind is nil when the environment has no sidecar or the sidecar read failed.
	Dind *RuntimeDindUsage `json:"dind,omitempty"`
}

// RuntimeDindUsage is the erun-dind sidecar's reading. It has no Disk field: the
// sidecar shares the workspace volume RuntimeUsage.Disk already reports.
type RuntimeDindUsage struct {
	CPU    RuntimeCPUUsage    `json:"cpu"`
	Memory RuntimeMemoryUsage `json:"memory"`
}

// HasCounters reports whether the read found anything worth retaining.
func (u RuntimeUsage) HasCounters() bool {
	return u.Memory.LimitBytes > 0 || u.Memory.PeakBytes > 0 || u.CPU.QuotaCores > 0 || u.CPU.Periods > 0
}

// RuntimeCPUUsage reports quota-relative utilisation; Unavailable when cgroup v2
// is absent or cpu.max sets no quota.
type RuntimeCPUUsage struct {
	QuotaCores         float64 `json:"quotaCores,omitempty"`
	UtilizationPercent float64 `json:"utilizationPercent,omitempty"`
	IntervalSeconds    float64 `json:"intervalSeconds,omitempty"`
	// Cumulative cpu.stat counters, so RuntimeUsageHistory can derive rates from deltas.
	UsageUsec        int64  `json:"usageUsec,omitempty"`
	Periods          int64  `json:"periods,omitempty"`
	ThrottledPeriods int64  `json:"throttledPeriods,omitempty"`
	Unavailable      string `json:"unavailable,omitempty"`
}

type RuntimeMemoryUsage struct {
	CurrentBytes int64 `json:"currentBytes,omitempty"`
	PeakBytes    int64 `json:"peakBytes,omitempty"`
	// PeakObserved distinguishes an unreadable memory.peak (cgroup v1, older
	// kernels) from a genuine zero.
	PeakObserved   bool    `json:"peakObserved,omitempty"`
	LimitBytes     int64   `json:"limitBytes,omitempty"`
	Unlimited      bool    `json:"unlimited,omitempty"`
	PercentOfLimit float64 `json:"percentOfLimit,omitempty"`
	OOMKills       int64   `json:"oomKills,omitempty"`
	// OOMKillsObserved distinguishes an unreadable oom_kill counter from zero kills.
	OOMKillsObserved bool `json:"oomKillsObserved,omitempty"`
	// CeilingHits is memory.events' "max" counter: hard reclaims at memory.max,
	// which happen without any OOM kill.
	CeilingHits         int64  `json:"ceilingHits,omitempty"`
	CeilingHitsObserved bool   `json:"ceilingHitsObserved,omitempty"`
	Unavailable         string `json:"unavailable,omitempty"`
}

// RuntimeDiskUsage totals come from statfs of the whole node filesystem;
// OwnUsedBytes is the only figure scoped to this environment.
type RuntimeDiskUsage struct {
	Mount       string  `json:"mount"`
	NodeShared  bool    `json:"nodeShared,omitempty"`
	TotalBytes  int64   `json:"totalBytes,omitempty"`
	UsedBytes   int64   `json:"usedBytes,omitempty"`
	PercentUsed float64 `json:"percentUsed,omitempty"`
	// OwnUsedBytes is a `du` of this environment's tree; OwnUsageObserved is false
	// when du fails or times out.
	OwnUsedBytes     int64  `json:"ownUsedBytes,omitempty"`
	OwnUsageObserved bool   `json:"ownUsageObserved,omitempty"`
	Unavailable      string `json:"unavailable,omitempty"`
}

func RuntimeCgroupV2Available(values map[string]string) bool {
	return values["cgroup_type"] == CgroupV2FSType
}

func RuntimeMemoryUsageFromValues(v map[string]string) RuntimeMemoryUsage {
	m := RuntimeMemoryUsage{}
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
func ReadLocalRuntimeUsage(cgroupRoot string) RuntimeUsage {
	root := strings.TrimSpace(cgroupRoot)
	if root == "" {
		root = DefaultCgroupRoot
	}
	values := readLocalCgroupValues(root)
	return RuntimeUsage{
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
func localRuntimeCPUUsage(v map[string]string) RuntimeCPUUsage {
	c := RuntimeCPUUsage{}
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
