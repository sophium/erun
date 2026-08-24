package eruncommon

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// `kubectl top` needs a metrics-server add-on that clusters like orbstack
// simply do not run ("error: Metrics API not available" on every local
// environment), so it cannot be the base for an orchestrator's usage reading
// (erun-ui/runtime_resources.go's loadKubernetesContainerUsage already
// concedes the gap). The data an orchestrator actually wants -- CPU quota
// utilisation, memory against the container's own cgroup limit, disk on the
// workspace mount -- lives in the runtime container's cgroup v2 files and is
// readable with no cluster add-on at all. RunRuntimeUsage reaches it the same
// way doctor's inspection does: `kubectl exec` a small script into the
// erun-devops container and parse its stdout, so it works against any
// namespace this process's kubeconfig can reach, self or remote, with no
// separate in-pod code path to keep in sync.
//
// Every reading reports its own unavailability instead of failing the call --
// cgroup v1, an unlimited memory.max, and a script that could not read a file
// are all normal, matching the fail-soft posture runtime_resources.go already
// takes for kubectl-top-less clusters. Only a kubectl/exec failure to reach
// the pod at all is a hard error.

const (
	// runtimeUsageContainer is the pod's main container -- the one whose
	// resources.limits are what `erun list` reports as "runtime-pod:
	// cpu=.../memory=..." (service.yaml's $runtimeCPU/$runtimeMemory) -- as
	// opposed to the erun-dind sidecar doctor's disk inspection targets.
	runtimeUsageContainer = DevopsComponentName

	// runtimeUsageWatchedMount is the one path always worth watching: the
	// runtime chart sets HOME to it, so the workspace, outputs, and any
	// non-PVC worktree all live under it.
	runtimeUsageWatchedMount = "/home/erun"

	// DefaultRuntimeUsageInterval is the CPU sample window when a caller
	// supplies none. One second gives a stable read without making every
	// `erun usage` call visibly slow.
	DefaultRuntimeUsageInterval = time.Second
	minRuntimeUsageInterval     = 100 * time.Millisecond
	maxRuntimeUsageInterval     = 30 * time.Second

	// cgroupV2FSType is what `stat -fc %T /sys/fs/cgroup` reports on a cgroup
	// v2 host; anything else (a v1 hierarchy, or the path missing) means the
	// files this reader depends on do not exist.
	cgroupV2FSType = "cgroup2fs"

	// Thresholds below are the values #1233 measured and fixed as named
	// constants with the reasoning attached, per the issue's own ask.
	//
	// RuntimeUsageMemoryWarnPercent: a container that has already used 85% of
	// its limit is one build step away from the OOM kill
	// erun-common/ai_launch.go can currently only report after the fact.
	RuntimeUsageMemoryWarnPercent = 85.0
	// RuntimeUsageMemoryPeakWarnPercent: memory.peak is a high-water mark, so
	// 95% means the container came within a hair of being killed even if
	// current usage has since dropped back down -- worth surfacing on its own.
	RuntimeUsageMemoryPeakWarnPercent = 95.0
	// RuntimeUsageDiskWarnPercent: disk fills silently (no kernel counter
	// tracks "close calls" the way memory.peak does for RAM), so the warning
	// threshold sits lower, ahead of ENOSPC rather than reacting to it.
	RuntimeUsageDiskWarnPercent = 90.0
)

// RuntimeUsageParams configures one usage read.
type RuntimeUsageParams struct {
	// Interval is the CPU sample window: usage_usec is read, the script
	// sleeps this long, then it is read again, so utilisation is a rate over
	// the interval rather than an instantaneous (and mostly meaningless)
	// cumulative counter. Clamped to [100ms, 30s]; zero uses the default.
	Interval time.Duration
}

// RuntimeUsage is one environment's live resource reading, scoped to the
// runtime container -- not the job that happens to be running in it, since
// two concurrent jobs in the same container would otherwise let one
// misattribute the other's consumption to itself.
type RuntimeUsage struct {
	Tenant      string             `json:"tenant"`
	Environment string             `json:"environment"`
	CPU         RuntimeCPUUsage    `json:"cpu"`
	Memory      RuntimeMemoryUsage `json:"memory"`
	Disk        []RuntimeDiskUsage `json:"disk"`
	// Warnings are named threshold crossings a caller can branch on without
	// parsing prose -- see the RuntimeUsage*WarnPercent constants.
	Warnings []string `json:"warnings,omitempty"`
}

// RuntimeCPUUsage reports quota-relative utilisation. Unavailable is set,
// and every other field left zero, when cgroup v2 is absent or cpu.max
// reports no quota to divide by (an unlimited container has no ceiling to be
// "close to").
type RuntimeCPUUsage struct {
	QuotaCores         float64 `json:"quotaCores,omitempty"`
	UtilizationPercent float64 `json:"utilizationPercent,omitempty"`
	IntervalSeconds    float64 `json:"intervalSeconds,omitempty"`
	Unavailable        string  `json:"unavailable,omitempty"`
}

// RuntimeMemoryUsage mirrors what erun-common/ai_launch.go's post-mortem OOM
// message would like to have had beforehand: current usage, the high-water
// mark, and a real OOM-kill counter instead of "likely out of memory".
type RuntimeMemoryUsage struct {
	CurrentBytes   int64   `json:"currentBytes,omitempty"`
	PeakBytes      int64   `json:"peakBytes,omitempty"`
	LimitBytes     int64   `json:"limitBytes,omitempty"`
	Unlimited      bool    `json:"unlimited,omitempty"`
	PercentOfLimit float64 `json:"percentOfLimit,omitempty"`
	OOMKills       int64   `json:"oomKills,omitempty"`
	Unavailable    string  `json:"unavailable,omitempty"`
}

// RuntimeDiskUsage reports usage for one watched mount (the workspace path,
// at minimum).
type RuntimeDiskUsage struct {
	Mount       string  `json:"mount"`
	TotalBytes  int64   `json:"totalBytes,omitempty"`
	UsedBytes   int64   `json:"usedBytes,omitempty"`
	PercentUsed float64 `json:"percentUsed,omitempty"`
	Unavailable string  `json:"unavailable,omitempty"`
}

// RunRuntimeUsage execs the reading script into the runtime container and
// parses its output. Dry-run traces the exec and returns an empty reading,
// matching RunObservation's dry-run contract.
func RunRuntimeUsage(ctx Context, runner RuntimeContainerCommandRunnerFunc, req ShellLaunchParams, params RuntimeUsageParams) (RuntimeUsage, error) {
	interval := clampRuntimeUsageInterval(params.Interval)
	script := runtimeUsageScript(interval)
	result, err := RunTracedRuntimeContainerCommand(ctx, runner, req, runtimeUsageContainer, "usage", script)
	if err != nil {
		return RuntimeUsage{}, err
	}
	if ctx.DryRun {
		return RuntimeUsage{Tenant: req.Tenant, Environment: req.Environment}, nil
	}
	return parseRuntimeUsage(req, result.Stdout, interval), nil
}

func clampRuntimeUsageInterval(interval time.Duration) time.Duration {
	switch {
	case interval <= 0:
		return DefaultRuntimeUsageInterval
	case interval < minRuntimeUsageInterval:
		return minRuntimeUsageInterval
	case interval > maxRuntimeUsageInterval:
		return maxRuntimeUsageInterval
	default:
		return interval
	}
}

// runtimeUsageScriptTemplate reads cgroup v2 memory/cpu accounting and the
// watched mount's disk usage as plain key=value lines so the Go side never has
// to re-derive shell quoting rules from parsed output. Every read is guarded
// so a missing file (cgroup v1, no PSI, an already-removed pod) prints an
// empty value instead of aborting the script under `set -eu` -- the "fail
// soft, report per-field" contract lives here as much as in the Go parser.
const runtimeUsageScriptTemplate = `set -eu
cg=/sys/fs/cgroup
cg_type=""
[ -d "$cg" ] && cg_type=$(stat -fc %T "$cg" 2>/dev/null || true)
printf 'cgroup_type=%s\n' "$cg_type"
read_value() { [ -r "$1" ] && cat "$1" 2>/dev/null || true; }
printf 'memory_current=%s\n' "$(read_value $cg/memory.current)"
printf 'memory_max=%s\n' "$(read_value $cg/memory.max)"
printf 'memory_peak=%s\n' "$(read_value $cg/memory.peak)"
printf 'memory_oom_kill=%s\n' "$(awk '$1=="oom_kill"{print $2}' $cg/memory.events 2>/dev/null || true)"
printf 'cpu_max=%s\n' "$(read_value $cg/cpu.max)"
cpu_usage_before=$(awk '$1=="usage_usec"{print $2}' $cg/cpu.stat 2>/dev/null || true)
time_before=$(date +%s%N)
sleep __RUNTIME_USAGE_INTERVAL_SECONDS__
cpu_usage_after=$(awk '$1=="usage_usec"{print $2}' $cg/cpu.stat 2>/dev/null || true)
time_after=$(date +%s%N)
printf 'cpu_usage_before=%s\n' "$cpu_usage_before"
printf 'cpu_usage_after=%s\n' "$cpu_usage_after"
printf 'cpu_time_before_ns=%s\n' "$time_before"
printf 'cpu_time_after_ns=%s\n' "$time_after"
printf 'disk_workspace=%s\n' "$(df -Pk ` + runtimeUsageWatchedMount + ` 2>/dev/null | tail -n1 || true)"
`

func runtimeUsageScript(interval time.Duration) string {
	seconds := strconv.FormatFloat(interval.Seconds(), 'f', -1, 64)
	return strings.Replace(runtimeUsageScriptTemplate, "__RUNTIME_USAGE_INTERVAL_SECONDS__", seconds, 1)
}

func parseRuntimeUsage(req ShellLaunchParams, output string, interval time.Duration) RuntimeUsage {
	values := parseRuntimeUsageValues(output)
	usage := RuntimeUsage{Tenant: req.Tenant, Environment: req.Environment}
	usage.CPU = runtimeCPUUsageFromValues(values, interval)
	usage.Memory = runtimeMemoryUsageFromValues(values)
	usage.Disk = []RuntimeDiskUsage{runtimeDiskUsageFromValues(values)}
	usage.Warnings = runtimeUsageWarnings(usage)
	return usage
}

func parseRuntimeUsageValues(output string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return values
}

func runtimeCgroupV2Available(values map[string]string) bool {
	return values["cgroup_type"] == cgroupV2FSType
}

func runtimeMemoryUsageFromValues(v map[string]string) RuntimeMemoryUsage {
	m := RuntimeMemoryUsage{}
	if !runtimeCgroupV2Available(v) {
		m.Unavailable = "cgroup v2 not detected under /sys/fs/cgroup; memory usage needs memory.current/memory.max"
		return m
	}
	current, ok := parseRuntimeInt64(v["memory_current"])
	if !ok {
		m.Unavailable = "memory.current was not readable"
		return m
	}
	m.CurrentBytes = current
	if peak, ok := parseRuntimeInt64(v["memory_peak"]); ok {
		m.PeakBytes = peak
	}
	if killed, ok := parseRuntimeInt64(v["memory_oom_kill"]); ok {
		m.OOMKills = killed
	}
	maxRaw := v["memory_max"]
	if maxRaw == "max" {
		m.Unlimited = true
		return m
	}
	limit, ok := parseRuntimeInt64(maxRaw)
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

func runtimeCPUUsageFromValues(v map[string]string, interval time.Duration) RuntimeCPUUsage {
	c := RuntimeCPUUsage{IntervalSeconds: interval.Seconds()}
	if !runtimeCgroupV2Available(v) {
		c.Unavailable = "cgroup v2 not detected under /sys/fs/cgroup; CPU usage needs cpu.max/cpu.stat"
		return c
	}
	quotaUsec, periodUsec, ok := parseRuntimeCPUMax(v["cpu_max"])
	if !ok {
		c.Unavailable = "cpu.max reports no quota (unlimited or not readable); utilisation needs a quota to measure against"
		return c
	}
	c.QuotaCores = float64(quotaUsec) / float64(periodUsec)

	before, beforeOk := parseRuntimeInt64(v["cpu_usage_before"])
	after, afterOk := parseRuntimeInt64(v["cpu_usage_after"])
	t1, t1Ok := parseRuntimeInt64(v["cpu_time_before_ns"])
	t2, t2Ok := parseRuntimeInt64(v["cpu_time_after_ns"])
	if !beforeOk || !afterOk || !t1Ok || !t2Ok || t2 <= t1 || after < before {
		c.Unavailable = "cpu.stat usage_usec was not readable across the sample interval"
		return c
	}
	elapsedSeconds := float64(t2-t1) / 1e9
	usedCores := (float64(after-before) / 1e6) / elapsedSeconds
	c.UtilizationPercent = 100 * usedCores / c.QuotaCores
	return c
}

// parseRuntimeCPUMax reads cpu.max's "<quota> <period>" pair, both in
// microseconds. A quota of "max" means the container has no CPU ceiling, so
// there is nothing to compute a percentage-of-quota against.
func parseRuntimeCPUMax(raw string) (quotaUsec, periodUsec int64, ok bool) {
	fields := strings.Fields(raw)
	if len(fields) != 2 || fields[0] == "max" {
		return 0, 0, false
	}
	quota, err1 := strconv.ParseInt(fields[0], 10, 64)
	period, err2 := strconv.ParseInt(fields[1], 10, 64)
	if err1 != nil || err2 != nil || quota <= 0 || period <= 0 {
		return 0, 0, false
	}
	return quota, period, true
}

func parseRuntimeInt64(raw string) (int64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}
	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func runtimeDiskUsageFromValues(v map[string]string) RuntimeDiskUsage {
	d := RuntimeDiskUsage{Mount: runtimeUsageWatchedMount}
	total, used, ok := parseRuntimeDFUsage(v["disk_workspace"])
	if !ok || total <= 0 {
		d.Unavailable = "df did not report usage for " + runtimeUsageWatchedMount
		return d
	}
	d.TotalBytes = total
	d.UsedBytes = used
	d.PercentUsed = 100 * float64(used) / float64(total)
	return d
}

// parseRuntimeDFUsage reads the Total/Used columns (1024-byte blocks,
// guaranteed by -Pk) from `df`'s POSIX-format output, locating them by their
// neighbor -- the "Capacity" percentage column -- rather than a fixed index.
// Mirrors parseDFAvailableBytes in release_disk_headroom.go: a long
// filesystem identifier pushes the data row's remaining columns left by one
// when it wraps onto its own line, so a fixed index reads the wrong column on
// exactly the inputs that most need this to work.
func parseRuntimeDFUsage(line string) (totalBytes, usedBytes int64, ok bool) {
	fields := strings.Fields(line)
	for i, field := range fields {
		if i == 0 || !strings.HasSuffix(field, "%") {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSuffix(field, "%")); err != nil {
			continue
		}
		if i < 3 {
			return 0, 0, false
		}
		totalKB, err1 := strconv.ParseInt(fields[i-3], 10, 64)
		usedKB, err2 := strconv.ParseInt(fields[i-2], 10, 64)
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return totalKB * 1024, usedKB * 1024, true
	}
	return 0, 0, false
}

func runtimeUsageWarnings(u RuntimeUsage) []string {
	var warnings []string
	if u.Memory.Unavailable == "" && !u.Memory.Unlimited && u.Memory.LimitBytes > 0 {
		if u.Memory.PercentOfLimit >= RuntimeUsageMemoryWarnPercent {
			warnings = append(warnings, fmt.Sprintf(
				"memory is at %.0f%% of its %s limit (warns at %.0f%%)",
				u.Memory.PercentOfLimit, formatMebibytes(u.Memory.LimitBytes), RuntimeUsageMemoryWarnPercent))
		}
		peakPercent := 100 * float64(u.Memory.PeakBytes) / float64(u.Memory.LimitBytes)
		if peakPercent >= RuntimeUsageMemoryPeakWarnPercent {
			warnings = append(warnings, fmt.Sprintf(
				"memory.peak reached %.0f%% of the limit (warns at %.0f%%) -- this environment came close to an OOM kill",
				peakPercent, RuntimeUsageMemoryPeakWarnPercent))
		}
	}
	if u.Memory.OOMKills > 0 {
		warnings = append(warnings, fmt.Sprintf("the cgroup recorded %d OOM kill(s)", u.Memory.OOMKills))
	}
	for _, d := range u.Disk {
		if d.Unavailable == "" && d.PercentUsed >= RuntimeUsageDiskWarnPercent {
			warnings = append(warnings, fmt.Sprintf(
				"%s is at %.0f%% disk usage (warns at %.0f%%)", d.Mount, d.PercentUsed, RuntimeUsageDiskWarnPercent))
		}
	}
	return warnings
}

func formatMebibytes(bytes int64) string {
	return fmt.Sprintf("%.0fMi", float64(bytes)/(1<<20))
}
