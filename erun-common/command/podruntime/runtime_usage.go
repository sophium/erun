package podruntime

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
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
//
// Two limits of this data are load-bearing enough to state here, because a
// consumer that does not know them will draw the wrong conclusion:
//
//  1. memory.peak resets when the container restarts, so a single reading is a
//     high-water mark for the current container lifetime, not for the
//     environment. RuntimeUsageHistory (runtime_usage_history.go) retains the
//     true peak across restarts; nothing may treat one reading as a lifetime
//     maximum. The warning decision follows from that: memory.events' oom_kill
//     resets on the same restart, and a restart is often how an OOM manifests,
//     so a warning read only from the live counters goes quiet at the exact
//     moment it matters. applyRetainedUsageWarnings adds what the retained
//     history proves and the live counters cannot.
//  2. PSI is absent on some kernels -- memory.pressure and cpu.pressure simply
//     do not exist in every runtime container's cgroup, so nothing here or
//     downstream may depend on pressure stall information. nr_throttled over
//     nr_periods (RuntimeCPUUsage.ThrottledPeriods / .Periods) is the
//     CPU-starvation signal that is actually available wherever cgroup v2 is.
//  3. A build-capable environment's real build work never happens in the
//     runtime container -- every image build runs in the erun-dind sidecar, a
//     separate cgroup the runtime container's own reading cannot see: the
//     sidecar's build containers are cgroup siblings, not descendants, so
//     there is no path from inside the runtime container to their usage.
//     RunRuntimeUsage closes most of that gap directly: on an environment
//     that carries the sidecar (EnvironmentType.UsesDindSidecar), it execs
//     the same reading script into the erun-dind container too (the doctor
//     inspection's own target, runtimeDindContainerName) and reports it as
//     RuntimeUsage.Dind, so a busy build no longer reads as an idle
//     environment. RuntimeUsage.ExcludesBuilds still names the runtime
//     container's own CPU/Memory as excluding the sidecar's contribution --
//     that stays true regardless of whether Dind could be read -- see
//     erun-cli/cmd/shared/usage_render.go and the desktop's matching
//     Sidebar.EnvHoverCard.tsx caveat. What no reading anywhere in this
//     process can produce is one host-wide figure spanning every
//     build-capable pod on the node; that view exists only outside any one
//     environment's own container set.
//  4. The watched mount's df figures (RuntimeDiskUsage.TotalBytes/UsedBytes/
//     PercentUsed) describe the node's filesystem, not this environment's own
//     footprint -- df has no concept of "this container's share", so every
//     environment scheduled on the same node reports the identical
//     total/used/percent regardless of which of them actually wrote the
//     bytes. RuntimeDiskUsage.NodeShared names this explicitly, and
//     OwnUsedBytes (a `du` of the watched mount, scoped to this environment's
//     own filesystem tree) is the number an operator can actually act on by
//     cleaning up this one environment -- see erun-cli/cmd/shared/usage_render.go's
//     "(node, shared)" label and the own-usage line rendered beneath it.
//
// RunRuntimeUsage is the one reader. It is reached two ways: exec'ing the
// script below into the container over kubectl, for an on-demand `erun usage`
// report from any namespace the caller's kubeconfig reaches; or
// ReadLocalRuntimeUsage reading the same cgroup files directly, for the in-pod
// monitor tick that already runs inside the container being measured, where
// kubectl-exec'ing into itself every 30s would trade a local file read for a
// Kubernetes API round trip to reach data already on its own filesystem. Both
// acquisition paths feed the same parsing and threshold logic below, so a
// remote read and a local read of the same container agree on what the
// numbers mean.

const (
	// runtimeUsageContainer is the pod's main container -- the one whose
	// resources.limits are what `erun list` reports as "runtime-pod:
	// cpu=.../memory=..." (service.yaml's $runtimeCPU/$runtimeMemory) -- as
	// opposed to the erun-dind sidecar doctor's disk inspection targets.
	runtimeUsageContainer = config.DevopsComponentName

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

	// runtimeBuildThrottleMinPeriods is the fewest scheduling periods the
	// sidecar's throttle ratio must be read over before the build-starvation
	// warning acts on it -- deliberately far below sizing's own floor
	// (runtimeSizingThrottlePeriods). The floor answers a question about the
	// verdict behind it, not about the ratio: sizing recommends a pod size
	// over a 24-hour window and can afford to wait out a few hundred swinging
	// periods, while this warning is about the build running now, and an
	// erun-devops rebuild occupies 10-20 minutes. One minute (600 periods at
	// cgroup v2's 100ms default) is long enough that a sidecar pinned
	// throughout it is past its own startup rather than merely young, and it
	// leaves most of a rebuild inside the window where the warning can still
	// explain a build that looks stuck.
	runtimeBuildThrottleMinPeriods = 600
	// RuntimeUsageMemoryPeakWarnPercent: memory.peak is a high-water mark, so
	// 95% means the container came within a hair of being killed even if
	// current usage has since dropped back down -- worth surfacing on its own.
	RuntimeUsageMemoryPeakWarnPercent = 95.0
	// RuntimeUsageDiskWarnPercent: disk fills silently (no kernel counter
	// tracks "close calls" the way memory.peak does for RAM), so the warning
	// threshold sits lower, ahead of ENOSPC rather than reacting to it.
	RuntimeUsageDiskWarnPercent = 90.0

	// diskOwnUsageTimeoutSeconds bounds the `du` walk of the watched mount so
	// a huge tree cannot stall the whole reading; a timeout renders as
	// OwnUsageObserved=false, the same "report unavailability, do not fail
	// the call" contract every other field here follows.
	diskOwnUsageTimeoutSeconds = 30
)

// RuntimeUsageParams configures one usage read.
type RuntimeUsageParams struct {
	// Interval is the CPU sample window: usage_usec is read, the script
	// sleeps this long, then it is read again, so utilisation is a rate over
	// the interval rather than an instantaneous (and mostly meaningless)
	// cumulative counter. Clamped to [100ms, 30s]; zero uses the default.
	Interval time.Duration
}

// RunRuntimeUsage execs the reading script into the runtime container and
// parses its output. Dry-run traces the exec and returns an empty reading,
// matching RunObservation's dry-run contract.
//
// On an environment that carries the erun-dind sidecar, it execs the same
// script into that container too (runtimeDindContainerName -- doctor's own
// inspection target) and attaches the parsed result as Dind. That second exec
// fails soft, deliberately unlike the runtime container's own exec above: an
// environment whose sidecar cannot be reached today (an older runtime image,
// a sidecar mid-restart) must still get a usable runtime-container reading
// rather than losing the whole call over a container this reading has always
// been unable to see anyway.
//
// It also reads the pod spec for the resources.requests the scheduler admits
// the pod on (runtime_pod_requests.go) and attaches them as Requests. That is
// a second source by necessity, not by preference: the numbers above are cgroup
// readings, and no cgroup file records a request. It too fails soft.
func RunRuntimeUsage(ctx core.Context, runner eruncommon.RuntimeContainerCommandRunnerFunc, podRequests RuntimePodRequestsRunnerFunc, req config.ShellLaunchParams, params RuntimeUsageParams) (config.RuntimeUsage, error) {
	interval := clampRuntimeUsageInterval(params.Interval)
	script := runtimeUsageScript(interval)
	result, err := eruncommon.RunTracedRuntimeContainerCommand(ctx, runner, req, runtimeUsageContainer, "usage", script)
	if err != nil {
		return config.RuntimeUsage{}, err
	}
	usesDind := req.Type.UsesDindSidecar()
	var dindResult kube.RemoteCommandResult
	var dindErr error
	if usesDind {
		dindResult, dindErr = eruncommon.RunTracedRuntimeContainerCommand(ctx, runner, req, eruncommon.RuntimeDindContainerName, "usage-dind", script)
	}
	// Read after the execs so a dry run's trace lists the actions in the order
	// they would run, and never fails the call: a pod spec that cannot be read
	// costs the reservation figures, not the cgroup reading above them.
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

// applyRetainedUsageWarnings adds the memory warnings the environment's
// retained history proves but the current container's cgroup counters cannot.
//
// memory.peak and memory.events' oom_kill are per-container counters, and a
// restart resets both to zero. A restart is also often how an OOM manifests,
// so deciding the warning from the live counters alone tells an operator the
// environment is memory-healthy at exactly the moment it most needs attention.
// That is the same silent loss of the OOM warning an unreadable memory.peak
// produces, reached by a different route. RuntimeUsageHistory already retains
// both across restarts -- its aggregates are monotonic and never rolled off --
// so this consults it instead of deriving a second, parallel signal.
//
// The retained figures are still scored against the *current* container's
// limit, so this cannot latch a warning on forever: raising runtimepod clears
// the peak warning on the next read, with no stored flag for anyone to reset.
// The OOM-kill warning is the standing one the documented `oomKills > 0`
// threshold asks for -- a kill that already happened stays reported -- and
// only kills the current container does not account for are added, so a live
// warning is never repeated.
func applyRetainedUsageWarnings(usage config.RuntimeUsage) config.RuntimeUsage {
	history, err := eruncommon.LoadRuntimeUsageHistory(usage.Tenant, usage.Environment)
	if err != nil {
		// No readable history is "nothing observed yet", not a warning: a host
		// that has never monitored this environment must not manufacture one.
		return usage
	}
	for _, warning := range retainedMemoryUsageWarnings(usage.Memory, history) {
		if !runtimeUsageHasWarning(usage.Warnings, warning) {
			usage.Warnings = append(usage.Warnings, warning)
		}
	}
	return usage
}

// retainedMemoryUsageWarnings is the decision itself, pure so the judgement
// about what survives a restart is testable without a container or a history
// file.
func retainedMemoryUsageWarnings(memory config.RuntimeMemoryUsage, history eruncommon.RuntimeUsageHistory) []string {
	// Both warnings are a percentage of the limit, so a reading that could not
	// supply one has nothing to be scored against.
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

	// Guarded on the retained peak exceeding the live one: an uninterrupted
	// container's own memory.peak still covers it, and the live warning above
	// already fired for the same crossing.
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

// runtimeUsageScriptTemplate reads cgroup v2 memory/cpu accounting and the
// watched mount's disk usage as plain key=value lines so the Go side never has
// to re-derive shell quoting rules from parsed output. Every read is guarded
// so a missing file (cgroup v1, no PSI, an already-removed pod) prints an
// empty value instead of aborting the script under `set -eu` -- the "fail
// soft, report per-field" contract lives here as much as in the Go parser.
//
// $cg resolves to the running process's OWN cgroup rather than assuming
// /sys/fs/cgroup already is it, via /proc/1/cgroup's unified ("0:...")
// entry -- "/" for a normally cgroup-namespaced container (the erun-devops
// runtime container: PID 1's own path is exactly the mount's root, so this
// is a no-op there), but NOT "/" for erun-dind, confirmed live: that sidecar
// runs privileged (service.yaml) and its /sys/fs/cgroup is the host's real
// cgroup2 root, not a namespace scoped to the container -- so a bare
// /sys/fs/cgroup/cpu.max there is either absent (the safe "unavailable" this
// script already reports) or, worse, would be some ancestor slice's own
// limit misattributed to this one environment, and cpu.stat at that same
// bare root aggregates the entire node, not this container. PID 1's cgroup
// path is what actually names this container's own leaf cgroup regardless of
// which of those two shapes /sys/fs/cgroup itself turned out to be.
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

// parseRuntimeDFUsage reads the Total/Used columns (1024-byte blocks,
// guaranteed by -Pk) from `df`'s POSIX-format output, locating them by their
// neighbor -- the "Capacity" percentage column -- rather than a fixed index.
// Mirrors parseDFDiskBytes in release_disk_headroom.go: a long
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

// runtimeBuildThrottleWarnings reports a build the sidecar's own cap is
// starving. The sidecar's CPU line alone cannot raise this: throttling is
// visible only in cpu.stat's nr_throttled, and a build held at its cap and a
// build merely busy at it read the same utilisation percentage. It matters
// operationally -- starvation is what turns a lint step into a timeout -- so a
// throttled build is named as starvation rather than left for an operator to
// infer from a percentage that happens to sit at 100.
//
// The verdict is worth only what the counters behind it are, and they are
// cumulative for the sidecar's lifetime. Reading any throttle at all as
// current starvation therefore turns a lifetime residue into a confident
// diagnosis -- a reading of 3 throttled periods out of 51,123 (0.006%) is
// what that looked like in the field, and it sent the reader hunting a CPU
// problem that was not there. So the warning fires only on a ratio the
// package calls material (runtimeSizingThrottleRatio).
//
// Staying silent below that ratio withholds the named share, not the
// evidence: the sidecar's raw periods and throttledPeriods are carried by the
// reading -- and by its JSON form -- whether or not this warning fires, so a
// consumer that wants the ratio can still derive the one this silence
// declines to name. Sizing is no substitute for it: that line scores the
// *runtime container's* counters under knob=runtimepod, and the sidecar is a
// separate cgroup that container cannot see (see the package comment's third
// limit), so below the bar nothing else in the rendered reading names how
// throttled a build was.
//
// The ratio is not the whole bar. A sidecar seconds old has only a few
// periods behind its ratio, and a build pinned through its own startup is
// what that looks like, so sizing's own floor (runtimeSizingThrottlePeriods,
// 10,000 periods -- ~16.6 minutes at cgroup v2's 100ms default) cannot be the
// floor here: it outlives the 10-20 minute rebuild this warning exists to
// explain, and the rebuild it describes is over before it fires. The build
// path reads the same ratio over runtimeBuildThrottleMinPeriods instead.
//
// An unreadable CPU reading has no counters to speak from and stays silent.
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

// runtimeBuildThrottleIsStarvation is this warning's whole bar: the package's
// one material ratio (runtimeSizingThrottleRatio), over the build path's own
// period floor rather than sizing's. It is deliberately not
// runtimeThrottleIsMaterial -- see runtimeBuildThrottleMinPeriods for why the
// two verdicts cannot share a floor, and runtimeThrottleIsMaterial for why
// they must still share a ratio.
func runtimeBuildThrottleIsStarvation(throttled, periods int64) bool {
	return periods >= runtimeBuildThrottleMinPeriods && float64(throttled) >= float64(periods)*eruncommon.RuntimeSizingThrottleRatio
}

// runtimeMemoryUsageWarnings takes a scope label ("" for the runtime
// container, "erun-dind: " for the sidecar) so the same threshold logic
// produces warnings an operator can tell apart when both containers cross
// one -- the exact "builds being OOM-killed" signal the runtime container's
// own reading can never carry on its own.
func runtimeMemoryUsageWarnings(scope string, memory config.RuntimeMemoryUsage) []string {
	var warnings []string
	if memory.Unavailable == "" && !memory.Unlimited && memory.LimitBytes > 0 {
		if memory.PercentOfLimit >= eruncommon.RuntimeUsageMemoryWarnPercent {
			warnings = append(warnings, fmt.Sprintf(
				"%smemory is at %.0f%% of its %s limit (warns at %.0f%%)",
				scope, memory.PercentOfLimit, core.FormatMebibytes(memory.LimitBytes), eruncommon.RuntimeUsageMemoryWarnPercent))
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

// parseRuntimeDindUsage mirrors parseRuntimeUsage's CPU/memory parsing
// exactly (same values map shape, same underlying helpers) since the sidecar
// is exec'd with the identical script -- only the container differs.
func parseRuntimeDindUsage(output string, interval time.Duration) *config.RuntimeDindUsage {
	values := parseRuntimeUsageValues(output)
	return &config.RuntimeDindUsage{
		CPU:    runtimeCPUUsageFromValues(values, interval),
		Memory: config.RuntimeMemoryUsageFromValues(values),
	}
}
