package main

type uiRuntimeResourceInput struct {
	KubernetesContext string `json:"kubernetesContext"`
	Tenant            string `json:"tenant,omitempty"`
	Environment       string `json:"environment,omitempty"`
}

// uiRuntimeResourceStatus is a reading of node capacity taken at one instant,
// not a fixed ceiling: allocatable moves as the node's own reservations move,
// and every free figure depends on what the other pods currently hold.
//
// It carries two readings because the two capacity questions have different
// answers, and reporting one of them without saying which it is was the defect
// this shape exists to close: the limits sum can read zero on a node with ample
// room to place a pod, and an operator who reads that as scheduling then
// shrinks a limit the node's own capacity planning assumes will be burst into.
type uiRuntimeResourceStatus struct {
	KubernetesContext string `json:"kubernetesContext"`
	Available         bool   `json:"available"`
	// Message is why no reading was taken. Empty whenever one was: the headline
	// an operator reads then belongs to a reading, because the two differ.
	Message string `json:"message,omitempty"`
	Node    string `json:"node,omitempty"`
	// Schedulable is what the scheduler will do on the chosen node: allocatable
	// minus the pods' declared requests. This is the reading a deploy's outcome
	// follows, and the one the configuration dialog's bounds come from.
	Schedulable uiRuntimeResourceReading `json:"schedulable"`
	// SchedulableComplete is false when a pod on the chosen node declares a
	// request the reading could not parse. The free figure above is then an
	// upper bound, never a zero and never an exact answer.
	SchedulableComplete bool `json:"schedulableComplete"`
	// WorstCase is what would be left on the chosen node if every container on
	// it ran to its declared limit at once -- oversubscription headroom, a
	// capacity-planning figure that is not scheduling capacity.
	WorstCase uiRuntimeResourceReading `json:"worstCase"`
	// Floored marks a worst-case reading clamped up to what this environment
	// already holds, so its maximum equals its current limit. Without it the
	// slider reads as a hard product limit rather than "the node is full".
	Floored bool `json:"floored"`
	// MeasuredUsage reports whether a metrics source answered, so the UI can say
	// whether unlimited containers were counted at their real usage or not
	// counted at all.
	MeasuredUsage bool `json:"measuredUsage"`
	// UnmeasuredContainers counts containers on the chosen node that declare no
	// limits and had no measured usage either — capacity the worst-case reading
	// cannot see.
	UnmeasuredContainers int `json:"unmeasuredContainers,omitempty"`
	// UnreadableRequests counts pods on the chosen node whose declared requests
	// could not be read, which is what makes SchedulableComplete false.
	UnreadableRequests int                     `json:"unreadableRequests,omitempty"`
	Nodes              []uiRuntimeResourceNode `json:"nodes,omitempty"`
}

// uiRuntimeResourceReading is one node's answer to one capacity question, and
// states which question that is. Message and Notice travel with the reading
// rather than with the status because the two readings answer different
// questions and need different remedies named.
type uiRuntimeResourceReading struct {
	CPU    uiRuntimeResourceMetric `json:"cpu"`
	Memory uiRuntimeResourceMetric `json:"memory"`
	// Message says which question this reading answers, in its own figures.
	Message string `json:"message,omitempty"`
	// Notice carries what the figures alone cannot: why a figure is capped,
	// what is invisible to it, and which lever actually moves it.
	Notice string `json:"notice,omitempty"`
}

type uiRuntimeResourceMetric struct {
	Total     float64 `json:"total"`
	Used      float64 `json:"used"`
	Free      float64 `json:"free"`
	Unit      string  `json:"unit"`
	Formatted string  `json:"formatted"`
	// Floored marks this metric's free value as clamped up to the environment's
	// own current limit because the node had nothing left to give.
	Floored bool `json:"floored"`
}

// uiRuntimeActivity is one live reading of what an environment's runtime pod is
// running: its persistent desktop sessions and the processes holding memory.
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

// uiRuntimeSession is one persistent desktop session as the pod reports it:
// Running is observed (a live program behind the socket), never inferred from
// how recently the session printed something.
type uiRuntimeSession struct {
	ID      string `json:"id"`
	Running bool   `json:"running"`
	Program string `json:"program,omitempty"`
}

// uiRuntimeProcessGroup is a class of resource-holding process the operator can
// recognise and, when Reclaim is set, act on.
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

// uiRuntimeUsage is one live reading of the selected environment's own CPU,
// memory and disk usage against its cgroup limits — as opposed to
// uiRuntimeResourceStatus, which reads the node. Available is the probe's own
// reachability; CPU/Memory/Disk each additionally state their own
// unavailability, since cgroup v1, an unlimited limit, or an unreadable file
// are all normal readings, not probe failures.
type uiRuntimeUsage struct {
	Tenant      string               `json:"tenant"`
	Environment string               `json:"environment"`
	Available   bool                 `json:"available"`
	Message     string               `json:"message,omitempty"`
	CPU         uiRuntimeCPUUsage    `json:"cpu"`
	Memory      uiRuntimeMemoryUsage `json:"memory"`
	Disk        []uiRuntimeDiskUsage `json:"disk,omitempty"`
	Warnings    []string             `json:"warnings,omitempty"`
	// ExcludesBuilds marks an environment whose runtime pod carries the
	// erun-dind sidecar builds actually run in: CPU and Memory above are the
	// runtime container's alone and can never see a build. Mirrors
	// config.RuntimeUsage.ExcludesBuilds.
	ExcludesBuilds bool `json:"excludesBuilds,omitempty"`
	// Dind is the sidecar's own reading, and is what makes a build-capable
	// environment's CPU legible: a release lane waiting on bounded `erun exec
	// job await` calls is near-idle in the runtime container by construction, so
	// without this the one figure a build-capable env reports is always ~0 and a
	// healthy build is indistinguishable from a wedged one.
	//
	// Nil on every other environment, and nil (not a zero value) when the exec
	// into the sidecar failed, so "could not read it" never renders as "read as
	// zero".
	Dind *uiRuntimeDindUsage `json:"dind,omitempty"`
	// Requests is what the scheduler admits this environment's pod on -- the
	// declared resources.requests, which no cgroup file records. It is what the
	// limits above must be read against: `Mem 0% of 27.0GiB` measures a ceiling
	// that reserves nothing, so the card states the reservation beside it.
	// Mirrors config.RuntimeUsage.Requests; nil when nothing was read, and
	// carrying Unavailable (never a zero) when the pod spec could not be.
	Requests *uiRuntimeUsageRequests `json:"requests,omitempty"`
}

// uiRuntimeUsageRequests mirrors the half of config.RuntimeUsageRequests
// this surface renders: the runtime container's own request, which is the
// reservation the CPU and Memory rows above are measured against, resolved here
// rather than in the component so no frontend code has to know the chart's
// container name.
type uiRuntimeUsageRequests struct {
	Runtime     uiRuntimeContainerRequests `json:"runtime"`
	Unavailable string                     `json:"unavailable,omitempty"`
}

// uiRuntimeContainerRequests is one container's declared request in the units a
// comparison needs (millicores, bytes) and in the strings a card shows. A
// zero/empty pair is a container that declares nothing for that resource, which
// reserves nothing -- it is rendered as absent, never as a measured zero.
type uiRuntimeContainerRequests struct {
	CPUMilli    int64  `json:"cpuMilli,omitempty"`
	MemoryBytes int64  `json:"memoryBytes,omitempty"`
	CPU         string `json:"cpu,omitempty"`
	Memory      string `json:"memory,omitempty"`
}

// uiRuntimeDindUsage is the erun-dind sidecar's own CPU/memory reading.
// Deliberately carries no disk field: the sidecar mounts the same workspace
// volume uiRuntimeUsage.Disk already reports, and a second figure under its own
// name invites reading it as an independent filesystem.
type uiRuntimeDindUsage struct {
	CPU    uiRuntimeCPUUsage    `json:"cpu"`
	Memory uiRuntimeMemoryUsage `json:"memory"`
}

// uiRuntimeCPUUsage carries Available=false with Unavailable set when the
// reader could not measure utilisation at all (no cgroup v2, or cpu.max
// reports no quota) — never a zero utilisation, which would read as "idle"
// rather than "unknown".
type uiRuntimeCPUUsage struct {
	Available          bool    `json:"available"`
	Unavailable        string  `json:"unavailable,omitempty"`
	QuotaCores         float64 `json:"quotaCores,omitempty"`
	Quota              string  `json:"quota,omitempty"`
	UtilizationPercent float64 `json:"utilizationPercent,omitempty"`
	Utilization        string  `json:"utilization,omitempty"`
	// UsageUsec is cpu.stat's cumulative usage for the current container
	// lifetime. It is carried on an unavailable reading as well as an available
	// one, because it is the only CPU figure a container with no cpu.max quota
	// can offer: utilisation needs a ceiling to be a fraction of, and the
	// erun-dind sidecar is commonly declared without one, so without this a
	// build-capable environment's real CPU work has no representation at all.
	UsageUsec int64 `json:"usageUsec,omitempty"`
}

// uiRuntimeMemoryUsage mirrors the reader's own fail-soft shape: Unlimited is
// a real, available reading (the container simply has no ceiling declared),
// distinct from Unavailable (the cgroup file itself could not be read). Both
// suppress the percent-of-limit figure, but only Unavailable is an error
// state the panel should call out.
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

// uiClusterRegistryStatus reports whether the selected Kubernetes context has an
// in-cluster erun-registry deployed, so the new-environment dialog can default to
// a resolvable cluster: registry entry instead of a hardcoded host.
type uiClusterRegistryStatus struct {
	KubernetesContext string `json:"kubernetesContext"`
	Deployed          bool   `json:"deployed"`
	Service           string `json:"service,omitempty"`
	Namespace         string `json:"namespace,omitempty"`
	Port              int    `json:"port,omitempty"`
	Insecure          bool   `json:"insecure"`
	Message           string `json:"message,omitempty"`
}

// uiHostedRegistryStatus reports whether erun's hosted container registry
// (registry.erunpaas.com) is currently reachable, so the new-environment
// dialog can gate offering it the same way uiClusterRegistryStatus gates the
// in-cluster registry. Reason and Recovery are only set when Available is
// false, and name the specific cause the probe observed and the action that
// resolves it.
type uiHostedRegistryStatus struct {
	Host      string `json:"host"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Recovery  string `json:"recovery,omitempty"`
}

// uiRuntimeResourceNode is one node's pair of readings. Unlike the chosen
// node's, a node here carries no message of its own: the panel states the two
// questions once, for the node it anchors to, and a per-node paragraph would
// repeat it once per node on any cluster worth inspecting.
type uiRuntimeResourceNode struct {
	Name                string                   `json:"name"`
	Schedulable         uiRuntimeResourceReading `json:"schedulable"`
	SchedulableComplete bool                     `json:"schedulableComplete"`
	WorstCase           uiRuntimeResourceReading `json:"worstCase"`
}

// Health-check statuses. "ok" is a passing check, "error" a blocking problem
// the operator must fix before the env can run, "unknown" a check that could
// not be evaluated (e.g. no kube-context to query).
const (
	healthCheckStatusOK      = "ok"
	healthCheckStatusError   = "error"
	healthCheckStatusUnknown = "unknown"
)

// Health-check fix affordances the frontend maps to a recovery action.
const (
	healthCheckFixNone        = ""
	healthCheckFixDeploy      = "deploy"
	healthCheckFixSetRegistry = "set-registry"
)

// Stable health-check ids so the frontend can key on the check, not its prose.
const (
	healthCheckIDRegistry = "container-registry"
	healthCheckIDRuntime  = "runtime-deployed"
)

// uiEnvironmentHealthCheck is one out-of-pod diagnostic result. Title is the
// user-facing check name; Detail explains the current outcome; Fix names the
// recovery action the frontend renders (empty when the check passed).
type uiEnvironmentHealthCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// uiEnvironmentHealth is the result of a "Check environment" run: the checks it
// evaluated and whether every one passed.
type uiEnvironmentHealth struct {
	Tenant      string                     `json:"tenant"`
	Environment string                     `json:"environment"`
	Healthy     bool                       `json:"healthy"`
	Checks      []uiEnvironmentHealthCheck `json:"checks"`
}
