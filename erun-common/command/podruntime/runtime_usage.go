package podruntime

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/orchestration/diagnosis"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// Usage is read from the runtime container's cgroup v2 files over `kubectl exec`, since
// `kubectl top` needs a metrics-server many clusters lack. Each reading reports its own
// unavailability; only failing to reach the pod is an error. Caveats consumers must know:
// memory.peak and oom_kill reset on restart (RuntimeUsageHistory retains them), PSI may be
// absent, builds run in the separate erun-dind cgroup (read as Dind), and df figures are
// node-wide (OwnUsedBytes is this environment's share).

const (
	// runtimeUsageContainer is the pod's main container, not the erun-dind sidecar.
	runtimeUsageContainer = config.DevopsComponentName

	// The runtime chart sets HOME to runtimeUsageWatchedMount.
	runtimeUsageWatchedMount = "/home/erun"

	DefaultRuntimeUsageInterval = time.Second
	minRuntimeUsageInterval     = 100 * time.Millisecond
	maxRuntimeUsageInterval     = 30 * time.Second

	// runtimeBuildThrottleMinPeriods is one minute at cgroup v2's 100ms period: far below
	// sizing's floor, which would outlast the 10-20 minute rebuild this warning explains.
	runtimeBuildThrottleMinPeriods = 600
	// RuntimeUsageMemoryPeakWarnPercent: memory.peak is a high-water mark, so 95% is a near kill.
	RuntimeUsageMemoryPeakWarnPercent = 95.0
	// RuntimeUsageDiskWarnPercent sits lower: disk has no close-call counter, so warn ahead of ENOSPC.
	RuntimeUsageDiskWarnPercent = 90.0

	// diskOwnUsageTimeoutSeconds bounds the `du` walk; a timeout reports OwnUsageObserved=false.
	diskOwnUsageTimeoutSeconds = 30
)

// RuntimeUsageParams configures one usage read.
type RuntimeUsageParams struct {
	// Interval is the CPU sample window, clamped to [100ms, 30s]; zero uses the default.
	Interval time.Duration
}

// RunRuntimeUsage execs the reading script into the runtime container, and into the
// erun-dind sidecar when present. The sidecar read and the pod-spec requests read fail
// soft so the runtime reading is never lost.
func RunRuntimeUsage(ctx core.Context, runner diagnosis.RuntimeContainerCommandRunnerFunc, podRequests RuntimePodRequestsRunnerFunc, req config.ShellLaunchParams, params RuntimeUsageParams) (config.RuntimeUsage, error) {
	interval := clampRuntimeUsageInterval(params.Interval)
	script := runtimeUsageScript(interval)
	result, err := diagnosis.RunTracedRuntimeContainerCommand(ctx, runner, req, runtimeUsageContainer, "usage", script)
	if err != nil {
		return config.RuntimeUsage{}, err
	}
	usesDind := req.Type.UsesDindSidecar()
	var dindResult kube.RemoteCommandResult
	var dindErr error
	if usesDind {
		dindResult, dindErr = diagnosis.RunTracedRuntimeContainerCommand(ctx, runner, req, build.RuntimeDindContainerName, "usage-dind", script)
	}
	// Read after the execs so a dry run traces actions in run order.
	requests := readRuntimeUsageRequests(ctx, podRequests, req)
	if ctx.DryRun {
		return config.RuntimeUsage{Tenant: req.Tenant, Environment: req.Environment, ExcludesBuilds: usesDind}, nil
	}
	usage := parseRuntimeUsage(req, result.Stdout, interval)
	usage.Requests = requests
	if usesDind && dindErr == nil {
		usage.Dind = parseRuntimeDindUsage(dindResult.Stdout, interval)
		usage.Warnings = runtimeUsageWarnings(usage)
	}
	return applyRetainedUsageWarnings(usage), nil
}

// applyRetainedUsageWarnings adds memory warnings the retained history proves but the
// restart-reset cgroup counters cannot. Retained figures are scored against the current
// limit, so raising runtimepod clears the peak warning.
func applyRetainedUsageWarnings(usage config.RuntimeUsage) config.RuntimeUsage {
	history, err := deploy.LoadRuntimeUsageHistory(usage.Tenant, usage.Environment)
	if err != nil {
		// No readable history means nothing observed yet, not a warning.
		return usage
	}
	for _, warning := range retainedMemoryUsageWarnings(usage.Memory, history) {
		if !runtimeUsageHasWarning(usage.Warnings, warning) {
			usage.Warnings = append(usage.Warnings, warning)
		}
	}
	return usage
}

func retainedMemoryUsageWarnings(memory config.RuntimeMemoryUsage, history deploy.RuntimeUsageHistory) []string {
	if memory.Unavailable != "" || memory.Unlimited || memory.LimitBytes <= 0 {
		return nil
	}
	var warnings []string

	var liveKills int64
	if memory.OOMKillsObserved {
		liveKills = memory.OOMKills
	}
	if history.ObservedOOMKills > liveKills {
		warnings = append(warnings, fmt.Sprintf(
			"the environment's retained history recorded %d OOM kill(s), which the current container does not account for -- memory.events resets when the container restarts",
			history.ObservedOOMKills))
	}

	// Only when the retained peak exceeds the live one; otherwise the live warning already fired.
	if history.ObservedPeakMemoryBytes > memory.PeakBytes {
		retainedPercent := 100 * float64(history.ObservedPeakMemoryBytes) / float64(memory.LimitBytes)
		if retainedPercent >= RuntimeUsageMemoryPeakWarnPercent {
			warnings = append(warnings, fmt.Sprintf(
				"the environment's retained memory peak reached %.0f%% of the limit (warns at %.0f%%) before the container restarted -- this environment came close to an OOM kill",
				retainedPercent, RuntimeUsageMemoryPeakWarnPercent))
		}
	}
	return warnings
}

func runtimeUsageHasWarning(warnings []string, warning string) bool {
	for _, existing := range warnings {
		if existing == warning {
			return true
		}
	}
	return false
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

// runtimeUsageScriptTemplate guards every read so a missing file prints an empty value
// under `set -eu`. $cg comes from /proc/1/cgroup because the privileged erun-dind
// sidecar's /sys/fs/cgroup is the host's cgroup2 root, not its own leaf.
const runtimeUsageScriptTemplate = `set -eu
cg=/sys/fs/cgroup
cg_rel=$(awk -F: '$1=="0"{print $3; exit}' /proc/1/cgroup 2>/dev/null || true)
case "$cg_rel" in
  ""|"/") ;;
  *) cg="$cg$cg_rel" ;;
esac
cg_type=""
[ -f "$cg/cgroup.controllers" ] && cg_type=cgroup2fs
printf 'cgroup_type=%s\n' "$cg_type"
read_value() { [ -r "$1" ] && cat "$1" 2>/dev/null || true; }
printf 'memory_current=%s\n' "$(read_value $cg/memory.current)"
printf 'memory_max=%s\n' "$(read_value $cg/memory.max)"
printf 'memory_peak=%s\n' "$(read_value $cg/memory.peak)"
printf 'memory_oom_kill=%s\n' "$(awk '$1=="oom_kill"{print $2}' $cg/memory.events 2>/dev/null || true)"
printf 'memory_ceiling_hits=%s\n' "$(awk '$1=="max"{print $2}' $cg/memory.events 2>/dev/null || true)"
printf 'cpu_max=%s\n' "$(read_value $cg/cpu.max)"
cpu_usage_before=$(awk '$1=="usage_usec"{print $2}' $cg/cpu.stat 2>/dev/null || true)
time_before=$(awk '{printf "%.0f", $1*1000000000}' /proc/uptime 2>/dev/null || true)
sleep __RUNTIME_USAGE_INTERVAL_SECONDS__
cpu_usage_after=$(awk '$1=="usage_usec"{print $2}' $cg/cpu.stat 2>/dev/null || true)
time_after=$(awk '{printf "%.0f", $1*1000000000}' /proc/uptime 2>/dev/null || true)
printf 'cpu_usage_before=%s\n' "$cpu_usage_before"
printf 'cpu_usage_after=%s\n' "$cpu_usage_after"
printf 'cpu_time_before_ns=%s\n' "$time_before"
printf 'cpu_time_after_ns=%s\n' "$time_after"
printf 'cpu_periods=%s\n' "$(awk '$1=="nr_periods"{print $2}' $cg/cpu.stat 2>/dev/null || true)"
printf 'cpu_throttled_periods=%s\n' "$(awk '$1=="nr_throttled"{print $2}' $cg/cpu.stat 2>/dev/null || true)"
printf 'disk_workspace=%s\n' "$(df -Pk ` + runtimeUsageWatchedMount + ` 2>/dev/null | tail -n1 || true)"
printf 'disk_own_used_kb=%s\n' "$(timeout __RUNTIME_USAGE_DISK_OWN_TIMEOUT_SECONDS__ du -sxk ` + runtimeUsageWatchedMount + ` 2>/dev/null | awk '{print $1}' || true)"
`

func runtimeUsageScript(interval time.Duration) string {
	seconds := strconv.FormatFloat(interval.Seconds(), 'f', -1, 64)
	script := strings.Replace(runtimeUsageScriptTemplate, "__RUNTIME_USAGE_INTERVAL_SECONDS__", seconds, 1)
	return strings.Replace(script, "__RUNTIME_USAGE_DISK_OWN_TIMEOUT_SECONDS__", strconv.Itoa(diskOwnUsageTimeoutSeconds), 1)
}

func parseRuntimeUsage(req config.ShellLaunchParams, output string, interval time.Duration) config.RuntimeUsage {
	values := parseRuntimeUsageValues(output)
	usage := config.RuntimeUsage{Tenant: req.Tenant, Environment: req.Environment, ExcludesBuilds: req.Type.UsesDindSidecar()}
	usage.CPU = runtimeCPUUsageFromValues(values, interval)
	usage.Memory = config.RuntimeMemoryUsageFromValues(values)
	usage.Disk = []config.RuntimeDiskUsage{runtimeDiskUsageFromValues(values)}
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

func runtimeCPUUsageFromValues(v map[string]string, interval time.Duration) config.RuntimeCPUUsage {
	c := config.RuntimeCPUUsage{IntervalSeconds: interval.Seconds()}
	if !config.RuntimeCgroupV2Available(v) {
		c.Unavailable = "cgroup v2 not detected under /sys/fs/cgroup; CPU usage needs cpu.max/cpu.stat"
		return c
	}
	counters, ok := config.RuntimeCPUCountersFromValues(v)
	c.UsageUsec = counters.UsageUsec
	c.Periods = counters.Periods
	c.ThrottledPeriods = counters.ThrottledPeriods
	if !ok {
		c.Unavailable = "cpu.max reports no quota (unlimited or not readable); utilisation needs a quota to measure against"
		return c
	}
	c.QuotaCores = counters.QuotaCores

	before, beforeOk := core.ParseRuntimeInt64(v["cpu_usage_before"])
	after, afterOk := core.ParseRuntimeInt64(v["cpu_usage_after"])
	t1, t1Ok := core.ParseRuntimeInt64(v["cpu_time_before_ns"])
	t2, t2Ok := core.ParseRuntimeInt64(v["cpu_time_after_ns"])
	if !beforeOk || !afterOk || !t1Ok || !t2Ok || t2 <= t1 || after < before {
		c.Unavailable = "cpu.stat usage_usec was not readable across the sample interval"
		return c
	}
	elapsedSeconds := float64(t2-t1) / 1e9
	usedCores := (float64(after-before) / 1e6) / elapsedSeconds
	c.UtilizationPercent = 100 * usedCores / c.QuotaCores
	return c
}

func runtimeDiskUsageFromValues(v map[string]string) config.RuntimeDiskUsage {
	d := config.RuntimeDiskUsage{Mount: runtimeUsageWatchedMount, NodeShared: true}
	if ownKB, ok := core.ParseRuntimeInt64(v["disk_own_used_kb"]); ok {
		d.OwnUsedBytes = ownKB * 1024
		d.OwnUsageObserved = true
	}
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

// parseRuntimeDFUsage locates columns relative to "Capacity": a wrapped long filesystem
// name shifts the data row left by one.
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

func runtimeUsageWarnings(u config.RuntimeUsage) []string {
	warnings := runtimeMemoryUsageWarnings("", u.Memory)
	for _, d := range u.Disk {
		if d.Unavailable == "" && d.PercentUsed >= RuntimeUsageDiskWarnPercent {
			warnings = append(warnings, fmt.Sprintf(
				"node disk is at %.0f%% (warns at %.0f%%) -- shared with every environment on this node",
				d.PercentUsed, RuntimeUsageDiskWarnPercent))
		}
	}
	if u.Dind != nil {
		warnings = append(warnings, runtimeMemoryUsageWarnings("erun-dind: ", u.Dind.Memory)...)
		warnings = append(warnings, runtimeBuildThrottleWarnings(u.Dind)...)
	}
	return warnings
}

// runtimeBuildThrottleWarnings names sidecar CPU starvation, which utilisation alone cannot
// show. Counters are cumulative, so it fires only on a material ratio; raw periods stay in
// the reading either way.
func runtimeBuildThrottleWarnings(dind *config.RuntimeDindUsage) []string {
	if dind == nil || dind.CPU.Unavailable != "" {
		return nil
	}
	if !runtimeBuildThrottleIsStarvation(dind.CPU.ThrottledPeriods, dind.CPU.Periods) {
		return nil
	}
	return []string{fmt.Sprintf(
		"the build was throttled in %d of %d cgroup periods -- it is CPU-starved by its own cap, which reads as a running build making no progress, not an idle environment",
		dind.CPU.ThrottledPeriods, dind.CPU.Periods)}
}

// runtimeBuildThrottleIsStarvation shares sizing's ratio but not its period floor.
func runtimeBuildThrottleIsStarvation(throttled, periods int64) bool {
	return periods >= runtimeBuildThrottleMinPeriods && float64(throttled) >= float64(periods)*deploy.RuntimeSizingThrottleRatio
}

// scope prefixes warnings so runtime and erun-dind crossings are distinguishable.
func runtimeMemoryUsageWarnings(scope string, memory config.RuntimeMemoryUsage) []string {
	var warnings []string
	if memory.Unavailable == "" && !memory.Unlimited && memory.LimitBytes > 0 {
		if memory.PercentOfLimit >= deploy.RuntimeUsageMemoryWarnPercent {
			warnings = append(warnings, fmt.Sprintf(
				"%smemory is at %.0f%% of its %s limit (warns at %.0f%%)",
				scope, memory.PercentOfLimit, core.FormatMebibytes(memory.LimitBytes), deploy.RuntimeUsageMemoryWarnPercent))
		}
		if memory.PeakObserved {
			peakPercent := 100 * float64(memory.PeakBytes) / float64(memory.LimitBytes)
			if peakPercent >= RuntimeUsageMemoryPeakWarnPercent {
				warnings = append(warnings, fmt.Sprintf(
					"%smemory.peak reached %.0f%% of the limit (warns at %.0f%%) -- this environment came close to an OOM kill",
					scope, peakPercent, RuntimeUsageMemoryPeakWarnPercent))
			}
		}
	}
	if memory.OOMKillsObserved && memory.OOMKills > 0 {
		warnings = append(warnings, fmt.Sprintf("%sthe cgroup recorded %d OOM kill(s)", scope, memory.OOMKills))
	}
	return warnings
}

func parseRuntimeDindUsage(output string, interval time.Duration) *config.RuntimeDindUsage {
	values := parseRuntimeUsageValues(output)
	return &config.RuntimeDindUsage{
		CPU:    runtimeCPUUsageFromValues(values, interval),
		Memory: config.RuntimeMemoryUsageFromValues(values),
	}
}
