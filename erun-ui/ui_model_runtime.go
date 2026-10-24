package main

type uiRuntimeResourceInput struct {
	KubernetesContext string `json:"kubernetesContext"`
	Tenant            string `json:"tenant,omitempty"`
	Environment       string `json:"environment,omitempty"`
}

// uiRuntimeResourceStatus is a point-in-time node capacity reading. Schedulable
// and WorstCase answer different questions and must not be conflated.
type uiRuntimeResourceStatus struct {
	KubernetesContext string `json:"kubernetesContext"`
	Available         bool   `json:"available"`
	// Message is why no reading was taken; empty whenever one was.
	Message string `json:"message,omitempty"`
	Node    string `json:"node,omitempty"`
	// Schedulable is allocatable minus declared requests; the dialog's bounds use it.
	Schedulable uiRuntimeResourceReading `json:"schedulable"`
	// SchedulableComplete false makes the free figure an upper bound.
	SchedulableComplete bool `json:"schedulableComplete"`
	// WorstCase assumes every container runs at its limit; it is not scheduling capacity.
	WorstCase uiRuntimeResourceReading `json:"worstCase"`
	// Floored: the worst case was clamped up to this environment's current limit.
	Floored       bool `json:"floored"`
	MeasuredUsage bool `json:"measuredUsage"`
	// UnmeasuredContainers have no limits and no measured usage, so WorstCase misses them.
	UnmeasuredContainers int                     `json:"unmeasuredContainers,omitempty"`
	UnreadableRequests   int                     `json:"unreadableRequests,omitempty"`
	Nodes                []uiRuntimeResourceNode `json:"nodes,omitempty"`
}

// uiRuntimeResourceReading is one node's answer to one capacity question.
type uiRuntimeResourceReading struct {
	CPU     uiRuntimeResourceMetric `json:"cpu"`
	Memory  uiRuntimeResourceMetric `json:"memory"`
	Message string                  `json:"message,omitempty"`
	Notice  string                  `json:"notice,omitempty"`
}

type uiRuntimeResourceMetric struct {
	Total     float64 `json:"total"`
	Used      float64 `json:"used"`
	Free      float64 `json:"free"`
	Unit      string  `json:"unit"`
	Formatted string  `json:"formatted"`
	Floored   bool    `json:"floored"`
}

// uiRuntimeActivity is a live reading of the runtime pod's sessions and processes.
type uiRuntimeActivity struct {
	Tenant          string                  `json:"tenant"`
	Environment     string                  `json:"environment"`
	Available       bool                    `json:"available"`
	Message         string                  `json:"message,omitempty"`
	SessionsRunning int                     `json:"sessionsRunning"`
	Sessions        []uiRuntimeSession      `json:"sessions,omitempty"`
	Processes       []uiRuntimeProcessGroup `json:"processes,omitempty"`
	MemoryHeld      string                  `json:"memoryHeld,omitempty"`
	MemoryHeldMiB   int64                   `json:"memoryHeldMiB,omitempty"`
}

// uiRuntimeSession.Running is observed, never inferred from recent output.
type uiRuntimeSession struct {
	ID      string `json:"id"`
	Running bool   `json:"running"`
	Program string `json:"program,omitempty"`
}

// uiRuntimeProcessGroup is a class of resource-holding process, reclaimable when Reclaim is set.
type uiRuntimeProcessGroup struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Count        int    `json:"count"`
	Memory       string `json:"memory"`
	MemoryMiB    int64  `json:"memoryMiB"`
	Reclaim      string `json:"reclaim,omitempty"`
	ReclaimLabel string `json:"reclaimLabel,omitempty"`
}

type uiRuntimeReclaimInput struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Action      string `json:"action"`
}

type uiRuntimeReclaimResult struct {
	Action  string `json:"action"`
	Message string `json:"message"`
}

// uiRuntimeUsage is the environment's own cgroup usage. Available is probe
// reachability; each metric states its own unavailability.
type uiRuntimeUsage struct {
	Tenant      string               `json:"tenant"`
	Environment string               `json:"environment"`
	Available   bool                 `json:"available"`
	Message     string               `json:"message,omitempty"`
	CPU         uiRuntimeCPUUsage    `json:"cpu"`
	Memory      uiRuntimeMemoryUsage `json:"memory"`
	Disk        []uiRuntimeDiskUsage `json:"disk,omitempty"`
	Warnings    []string             `json:"warnings,omitempty"`
	// ExcludesBuilds: builds run in the erun-dind sidecar, invisible to CPU/Memory.
	ExcludesBuilds bool `json:"excludesBuilds,omitempty"`
	// Dind is nil when there is no sidecar or it could not be read, never a zero reading.
	Dind *uiRuntimeDindUsage `json:"dind,omitempty"`
	// Requests are the declared reservations; nil when nothing was read.
	Requests *uiRuntimeUsageRequests `json:"requests,omitempty"`
}

// uiRuntimeUsageRequests is the runtime container's part of config.RuntimeUsageRequests.
type uiRuntimeUsageRequests struct {
	Runtime     uiRuntimeContainerRequests `json:"runtime"`
	Unavailable string                     `json:"unavailable,omitempty"`
}

// uiRuntimeContainerRequests: a zero value means no request declared, rendered as absent.
type uiRuntimeContainerRequests struct {
	CPUMilli    int64  `json:"cpuMilli,omitempty"`
	MemoryBytes int64  `json:"memoryBytes,omitempty"`
	CPU         string `json:"cpu,omitempty"`
	Memory      string `json:"memory,omitempty"`
}

// uiRuntimeDindUsage has no disk field: the sidecar shares uiRuntimeUsage.Disk's volume.
type uiRuntimeDindUsage struct {
	CPU    uiRuntimeCPUUsage    `json:"cpu"`
	Memory uiRuntimeMemoryUsage `json:"memory"`
}

// uiRuntimeCPUUsage reports unmeasurable utilisation as Unavailable, never as zero.
type uiRuntimeCPUUsage struct {
	Available          bool    `json:"available"`
	Unavailable        string  `json:"unavailable,omitempty"`
	QuotaCores         float64 `json:"quotaCores,omitempty"`
	Quota              string  `json:"quota,omitempty"`
	UtilizationPercent float64 `json:"utilizationPercent,omitempty"`
	Utilization        string  `json:"utilization,omitempty"`
	// UsageUsec is set even when unavailable: it is the only figure without a cpu.max quota.
	UsageUsec int64 `json:"usageUsec,omitempty"`
}

// uiRuntimeMemoryUsage: Unlimited is a valid reading; only Unavailable is an error.
type uiRuntimeMemoryUsage struct {
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

type uiRuntimeDiskUsage struct {
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

// uiClusterRegistryStatus reports whether the context has an in-cluster erun-registry.
type uiClusterRegistryStatus struct {
	KubernetesContext string `json:"kubernetesContext"`
	Deployed          bool   `json:"deployed"`
	Service           string `json:"service,omitempty"`
	Namespace         string `json:"namespace,omitempty"`
	Port              int    `json:"port,omitempty"`
	Insecure          bool   `json:"insecure"`
	Message           string `json:"message,omitempty"`
}

// uiHostedRegistryStatus reports whether erun's hosted registry is reachable.
type uiHostedRegistryStatus struct {
	Host      string `json:"host"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Recovery  string `json:"recovery,omitempty"`
}

// uiRuntimeResourceNode is one node's pair of readings.
type uiRuntimeResourceNode struct {
	Name                string                   `json:"name"`
	Schedulable         uiRuntimeResourceReading `json:"schedulable"`
	SchedulableComplete bool                     `json:"schedulableComplete"`
	WorstCase           uiRuntimeResourceReading `json:"worstCase"`
}

const (
	healthCheckStatusOK      = "ok"
	healthCheckStatusError   = "error"
	healthCheckStatusUnknown = "unknown"
)

const (
	healthCheckFixNone        = ""
	healthCheckFixDeploy      = "deploy"
	healthCheckFixSetRegistry = "set-registry"
)

// Stable health-check ids the frontend keys on instead of prose.
const (
	healthCheckIDRegistry = "container-registry"
	healthCheckIDRuntime  = "runtime-deployed"
)

type uiEnvironmentHealthCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

type uiEnvironmentHealth struct {
	Tenant      string                     `json:"tenant"`
	Environment string                     `json:"environment"`
	Healthy     bool                       `json:"healthy"`
	Checks      []uiEnvironmentHealthCheck `json:"checks"`
}
