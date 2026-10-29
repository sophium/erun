package model

import (
	"time"
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

// RuntimeUsageParams configures one usage read.
type RuntimeUsageParams struct {
	// Interval is the CPU sample window, clamped to [100ms, 30s]; zero uses the default.
	Interval time.Duration
}
