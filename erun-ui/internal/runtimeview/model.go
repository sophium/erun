// Package runtimeview is the desktop's live view of an environment's runtime:
// the activity and usage sweeps with their cached readings, the node backing
// it, its health, working issue, pod activity and reclaim, node capacity,
// sizing, and the runtime chart a deploy would install.
package runtimeview

type ResourceInput struct {
	KubernetesContext string `json:"kubernetesContext"`
	Tenant            string `json:"tenant,omitempty"`
	Environment       string `json:"environment,omitempty"`
}

// ResourceStatus is a point-in-time node capacity reading. Schedulable
// and WorstCase answer different questions and must not be conflated.
type ResourceStatus struct {
	KubernetesContext string `json:"kubernetesContext"`
	Available         bool   `json:"available"`
	// Message is why no reading was taken; empty whenever one was.
	Message string `json:"message,omitempty"`
	Node    string `json:"node,omitempty"`
	// Schedulable is allocatable minus declared requests; the dialog's bounds use it.
	Schedulable ResourceReading `json:"schedulable"`
	// SchedulableComplete false makes the free figure an upper bound.
	SchedulableComplete bool `json:"schedulableComplete"`
	// WorstCase assumes every container runs at its limit; it is not scheduling capacity.
	WorstCase ResourceReading `json:"worstCase"`
	// Floored: the worst case was clamped up to this environment's current limit.
	Floored       bool `json:"floored"`
	MeasuredUsage bool `json:"measuredUsage"`
	// UnmeasuredContainers have no limits and no measured usage, so WorstCase misses them.
	UnmeasuredContainers int            `json:"unmeasuredContainers,omitempty"`
	UnreadableRequests   int            `json:"unreadableRequests,omitempty"`
	Nodes                []ResourceNode `json:"nodes,omitempty"`
}

// ResourceReading is one node's answer to one capacity question.
type ResourceReading struct {
	CPU     ResourceMetric `json:"cpu"`
	Memory  ResourceMetric `json:"memory"`
	Message string         `json:"message,omitempty"`
	Notice  string         `json:"notice,omitempty"`
}

type ResourceMetric struct {
	Total     float64 `json:"total"`
	Used      float64 `json:"used"`
	Free      float64 `json:"free"`
	Unit      string  `json:"unit"`
	Formatted string  `json:"formatted"`
	Floored   bool    `json:"floored"`
}

// Activity is a live reading of the runtime pod's sessions and processes.
type Activity struct {
	Tenant          string         `json:"tenant"`
	Environment     string         `json:"environment"`
	Available       bool           `json:"available"`
	Message         string         `json:"message,omitempty"`
	SessionsRunning int            `json:"sessionsRunning"`
	Sessions        []Session      `json:"sessions,omitempty"`
	Processes       []ProcessGroup `json:"processes,omitempty"`
	MemoryHeld      string         `json:"memoryHeld,omitempty"`
	MemoryHeldMiB   int64          `json:"memoryHeldMiB,omitempty"`
}

// Session.Running is observed, never inferred from recent output.
type Session struct {
	ID      string `json:"id"`
	Running bool   `json:"running"`
	Program string `json:"program,omitempty"`
}

// ProcessGroup is a class of resource-holding process, reclaimable when Reclaim is set.
type ProcessGroup struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Count        int    `json:"count"`
	Memory       string `json:"memory"`
	MemoryMiB    int64  `json:"memoryMiB"`
	Reclaim      string `json:"reclaim,omitempty"`
	ReclaimLabel string `json:"reclaimLabel,omitempty"`
}

type ReclaimInput struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Action      string `json:"action"`
}

type ReclaimResult struct {
	Action  string `json:"action"`
	Message string `json:"message"`
}

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

// ClusterRegistryStatus reports whether the context has an in-cluster erun-registry.
type ClusterRegistryStatus struct {
	KubernetesContext string `json:"kubernetesContext"`
	Deployed          bool   `json:"deployed"`
	Service           string `json:"service,omitempty"`
	Namespace         string `json:"namespace,omitempty"`
	Port              int    `json:"port,omitempty"`
	Insecure          bool   `json:"insecure"`
	Message           string `json:"message,omitempty"`
}

// HostedRegistryStatus reports whether erun's hosted registry is reachable.
type HostedRegistryStatus struct {
	Host      string `json:"host"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Recovery  string `json:"recovery,omitempty"`
}

// ResourceNode is one node's pair of readings.
type ResourceNode struct {
	Name                string          `json:"name"`
	Schedulable         ResourceReading `json:"schedulable"`
	SchedulableComplete bool            `json:"schedulableComplete"`
	WorstCase           ResourceReading `json:"worstCase"`
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

type HealthCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

type Health struct {
	Tenant      string        `json:"tenant"`
	Environment string        `json:"environment"`
	Healthy     bool          `json:"healthy"`
	Checks      []HealthCheck `json:"checks"`
}

// NodeSnapshot describes the node, kept apart from environment state: a stopped
// node and a stopped environment have different remedies.
type NodeSnapshot struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	// Status "unknown" (stale) and "" (unobserved) must never render as stopped.
	Status string `json:"status"`
}

// NodePayload is emitted on transitions; a nil Node is still published.
type NodePayload struct {
	Tenant      string        `json:"tenant"`
	Environment string        `json:"environment"`
	Node        *NodeSnapshot `json:"node,omitempty"`
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

// WorkingIssue: Available is false when the worktree is not on this host.
type WorkingIssue struct {
	Available   bool   `json:"available"`
	Branch      string `json:"branch,omitempty"`
	IssueNumber int    `json:"issueNumber,omitempty"`
	IssueTitle  string `json:"issueTitle,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// ActivityPayload is replaced every poll, unlike the sticky uimodel.EnvStatusPayload.
type ActivityPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Reachable   bool   `json:"reachable"`
	// Observed separates "answered, no work" from "no answer".
	Observed bool `json:"observed"`
	// Outage diagnoses a dropped forward that reconnecting did not fix, so the row
	// does not look merely quiet.
	Outage      bool   `json:"outage"`
	CheckFailed bool   `json:"checkFailed,omitempty"`
	Busy        bool   `json:"busy"`
	Detail      string `json:"detail,omitempty"`
}

// UsagePayload is published on the sweep cadence, not per hover.
type UsagePayload struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment"`
	Usage             Usage  `json:"usage"`
	ObservedAtUnix    int64  `json:"observedAtUnix"`
	StaleAfterSeconds int64  `json:"staleAfterSeconds"`
}
