package uimodel

// The runtime readings an environment card renders; the runtime view takes them
// and the orchestrator cards show them for each linked environment.

// Usage is the environment's own cgroup usage. Available is probe
// reachability; each metric states its own unavailability.
type Usage struct {
	Tenant      string      `json:"tenant"`
	Environment string      `json:"environment"`
	Available   bool        `json:"available"`
	Message     string      `json:"message,omitempty"`
	CPU         CPUUsage    `json:"cpu"`
	Memory      MemoryUsage `json:"memory"`
	Disk        []DiskUsage `json:"disk,omitempty"`
	Warnings    []string    `json:"warnings,omitempty"`
	// ExcludesBuilds: builds run in the erun-dind sidecar, invisible to CPU/Memory.
	ExcludesBuilds bool `json:"excludesBuilds,omitempty"`
	// Dind is nil when there is no sidecar or it could not be read, never a zero reading.
	Dind *DindUsage `json:"dind,omitempty"`
	// Requests are the declared reservations; nil when nothing was read.
	Requests *UsageRequests `json:"requests,omitempty"`
}

// UsageRequests is the runtime container's part of config.RuntimeUsageRequests.
type UsageRequests struct {
	Runtime     ContainerRequests `json:"runtime"`
	Unavailable string            `json:"unavailable,omitempty"`
}

// ContainerRequests: a zero value means no request declared, rendered as absent.
type ContainerRequests struct {
	CPUMilli    int64  `json:"cpuMilli,omitempty"`
	MemoryBytes int64  `json:"memoryBytes,omitempty"`
	CPU         string `json:"cpu,omitempty"`
	Memory      string `json:"memory,omitempty"`
}

// DindUsage has no disk field: the sidecar shares Usage.Disk's volume.
type DindUsage struct {
	CPU    CPUUsage    `json:"cpu"`
	Memory MemoryUsage `json:"memory"`
}

// CPUUsage reports unmeasurable utilisation as Unavailable, never as zero.
type CPUUsage struct {
	Available          bool    `json:"available"`
	Unavailable        string  `json:"unavailable,omitempty"`
	QuotaCores         float64 `json:"quotaCores,omitempty"`
	Quota              string  `json:"quota,omitempty"`
	UtilizationPercent float64 `json:"utilizationPercent,omitempty"`
	Utilization        string  `json:"utilization,omitempty"`
	// UsageUsec is set even when unavailable: it is the only figure without a cpu.max quota.
	UsageUsec int64 `json:"usageUsec,omitempty"`
}

// MemoryUsage: Unlimited is a valid reading; only Unavailable is an error.
type MemoryUsage struct {
	Available      bool    `json:"available"`
	Unavailable    string  `json:"unavailable,omitempty"`
	Unlimited      bool    `json:"unlimited,omitempty"`
	CurrentBytes   int64   `json:"currentBytes,omitempty"`
	Current        string  `json:"current,omitempty"`
	PeakBytes      int64   `json:"peakBytes,omitempty"`
	Peak           string  `json:"peak,omitempty"`
	LimitBytes     int64   `json:"limitBytes,omitempty"`
	Limit          string  `json:"limit,omitempty"`
	PercentOfLimit float64 `json:"percentOfLimit,omitempty"`
	OOMKills       int64   `json:"oomKills"`
}

type DiskUsage struct {
	Mount       string  `json:"mount"`
	Available   bool    `json:"available"`
	Unavailable string  `json:"unavailable,omitempty"`
	TotalBytes  int64   `json:"totalBytes,omitempty"`
	Total       string  `json:"total,omitempty"`
	UsedBytes   int64   `json:"usedBytes,omitempty"`
	Used        string  `json:"used,omitempty"`
	PercentUsed float64 `json:"percentUsed,omitempty"`
	Percent     string  `json:"percent,omitempty"`
}

// ActivitySnapshot mirrors ActivityPayload without the identity.
type ActivitySnapshot struct {
	Reachable bool `json:"reachable"`
	Observed  bool `json:"observed"`
	Outage    bool `json:"outage"`
	// CheckFailed is a kubectl-exec probe that did not answer, distinct from Outage.
	CheckFailed bool   `json:"checkFailed,omitempty"`
	Busy        bool   `json:"busy"`
	Detail      string `json:"detail,omitempty"`
}

// UsageSnapshot lets the renderer mark a reading stale without knowing the
// sweep interval.
type UsageSnapshot struct {
	Usage             Usage `json:"usage"`
	ObservedAtUnix    int64 `json:"observedAtUnix"`
	StaleAfterSeconds int64 `json:"staleAfterSeconds"`
}
