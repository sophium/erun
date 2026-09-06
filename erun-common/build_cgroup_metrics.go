package eruncommon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// buildCgroupSysFSRoot is where cgroup v2 is mounted inside the erun-dind
// sidecar -- the container dind-entrypoint.sh actually wrote the cap cgroup
// into (see build_cpu_cap.go).
const buildCgroupSysFSRoot = "/sys/fs/cgroup"

// buildCgroupDindContainerName is the erun-dind sidecar's fixed container
// name (erun-devops/k8s/erun-devops/templates/service.yaml). It is the only
// container in the pod that runs with the host's cgroup namespace -- this
// process's own container has a private one, so its own view of
// /sys/fs/cgroup/docker/... simply does not exist, confirmed empirically
// against a real deployed pod (erun#2274): `inInjectedRuntimePod()` was true,
// ERUN_TENANT/ERUN_ENVIRONMENT were set, a real build ran, and the cap cgroup
// directory was still absent from this container's own filesystem. Every
// read below therefore goes through `kubectl exec` into this one named
// container instead of a local file read.
const buildCgroupDindContainerName = "erun-dind"

// buildCgroupExecTimeout bounds one remote read. A build samples this at
// every step's start and end, so a slow or hung kubectl must not compound
// into a real delay on the build it exists to measure.
const buildCgroupExecTimeout = 5 * time.Second

// buildCgroupMetricsDir resolves the absolute path, as seen from inside the
// erun-dind sidecar, of the build's own capped cgroup -- the same
// deterministic path buildContainerCPUCapCgroupParent hands docker as
// --cgroup-parent, rooted at the real cgroup v2 mount rather than the bare
// cgroup path that flag expects. Empty outside an injected runtime pod,
// where no such cgroup exists.
func buildCgroupMetricsDir() string {
	parent := buildContainerCPUCapCgroupParent()
	if parent == "" {
		return ""
	}
	return filepath.Join(buildCgroupSysFSRoot, parent)
}

// buildCgroupReadScriptTemplate reads one snapshot's worth of counters in a
// single remote command: the cap cgroup aggregates every RUN-instruction
// container docker nests under it (cgroup v2 propagates resource accounting
// to every ancestor), but that aggregation depends on the cap directory
// itself having populated stat files, which is not guaranteed on every
// cgroup driver/version combination -- so the script falls back to a
// "buildkit" child one level down, verified live against a real deployed pod
// to hold the same counters when the parent's own do not populate. Doing the
// fallback decision here, inside the one exec, is what keeps this to one
// kubectl call per snapshot instead of up to eight (four files across two
// candidate directories).
const buildCgroupReadScriptTemplate = `set -eu
dir="__BASE__"
grep -q '^usage_usec ' "$dir/cpu.stat" 2>/dev/null || dir="__BASE__/buildkit"
printf 'usage_usec=%s\n' "$(awk '$1=="usage_usec"{print $2}' "$dir/cpu.stat" 2>/dev/null || true)"
printf 'nr_periods=%s\n' "$(awk '$1=="nr_periods"{print $2}' "$dir/cpu.stat" 2>/dev/null || true)"
printf 'nr_throttled=%s\n' "$(awk '$1=="nr_throttled"{print $2}' "$dir/cpu.stat" 2>/dev/null || true)"
printf 'throttled_usec=%s\n' "$(awk '$1=="throttled_usec"{print $2}' "$dir/cpu.stat" 2>/dev/null || true)"
printf 'cpu_max=%s\n' "$(cat "$dir/cpu.max" 2>/dev/null || true)"
printf 'memory_peak=%s\n' "$(cat "$dir/memory.peak" 2>/dev/null || true)"
printf 'io_stat_begin\n'
cat "$dir/io.stat" 2>/dev/null || true
printf 'io_stat_end\n'
`

func buildCgroupReadScript(base string) string {
	return strings.ReplaceAll(buildCgroupReadScriptTemplate, "__BASE__", base)
}

// buildCgroupCounters is one point-in-time reading of the build cgroup's
// cumulative cpu.stat/io.stat counters plus the cgroup's static cpu.max quota
// and current memory.peak high-water mark.
type buildCgroupCounters struct {
	quotaCores       float64
	usageUsec        int64
	periods          int64
	throttledPeriods int64
	throttledUsec    int64
	ioReadBytes      int64
	ioWriteBytes     int64
	peakMemoryBytes  int64
	peakObserved     bool
}

// parseBuildCgroupExecOutput parses buildCgroupReadScriptTemplate's stdout.
// It succeeds only when usage_usec was readable -- the minimum needed to
// attribute any cost at all -- and degrades every other field independently,
// matching runtimeCPUUsageFromValues/runtimeMemoryUsageFromValues's
// per-field fail-soft posture in runtime_usage.go. This is the pure parsing
// half of the read, kept separate from the exec plumbing (sampleBuildCgroup
// below) so it is directly unit-testable against captured/synthetic output
// with no kubectl, cluster, or filesystem involved.
func parseBuildCgroupExecOutput(output string) (buildCgroupCounters, bool) {
	values := map[string]string{}
	var ioLines []string
	inIOStat := false
	for _, line := range strings.Split(output, "\n") {
		switch line {
		case "io_stat_begin":
			inIOStat = true
			continue
		case "io_stat_end":
			inIOStat = false
			continue
		}
		if inIOStat {
			ioLines = append(ioLines, line)
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		values[key] = value
	}

	usage, ok := parseRuntimeInt64(values["usage_usec"])
	if !ok {
		return buildCgroupCounters{}, false
	}
	counters := buildCgroupCounters{usageUsec: usage}
	counters.periods, _ = parseRuntimeInt64(values["nr_periods"])
	counters.throttledPeriods, _ = parseRuntimeInt64(values["nr_throttled"])
	counters.throttledUsec, _ = parseRuntimeInt64(values["throttled_usec"])
	if quotaUsec, periodUsec, ok := parseRuntimeCPUMax(strings.TrimSpace(values["cpu_max"])); ok {
		counters.quotaCores = float64(quotaUsec) / float64(periodUsec)
	}
	counters.ioReadBytes, counters.ioWriteBytes = sumBuildCgroupIOBytes(strings.Join(ioLines, "\n"))
	if peak, ok := parseRuntimeInt64(strings.TrimSpace(values["memory_peak"])); ok {
		counters.peakMemoryBytes = peak
		counters.peakObserved = true
	}
	return counters, true
}

// sumBuildCgroupIOBytes sums io.stat's rbytes/wbytes across every device
// line -- a build touches at least the docker state device and often an
// overlay/tmpfs mount too, and the per-step question ("how much I/O did this
// step do") wants the total, not one device chosen arbitrarily.
func sumBuildCgroupIOBytes(ioStat string) (readBytes, writeBytes int64) {
	for _, line := range strings.Split(ioStat, "\n") {
		for _, field := range strings.Fields(line) {
			key, value, found := strings.Cut(field, "=")
			if !found {
				continue
			}
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				continue
			}
			switch key {
			case "rbytes":
				readBytes += n
			case "wbytes":
				writeBytes += n
			}
		}
	}
	return readBytes, writeBytes
}

// sampleBuildCgroup execs buildCgroupReadScript into the erun-dind sidecar of
// this pod (pod, from os.Hostname -- a Kubernetes pod's hostname is its own
// pod name) and parses the result. Never fails the caller: any exec error
// (kubectl missing, RBAC denied, the sidecar not ready, the exec timing out)
// yields ok=false, exactly like an unreadable local file would.
func sampleBuildCgroup(pod, base string) (buildCgroupCounters, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), buildCgroupExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "exec", pod, "-c", buildCgroupDindContainerName, "--", "sh", "-c", buildCgroupReadScript(base))
	output, err := cmd.Output()
	if err != nil {
		return buildCgroupCounters{}, false
	}
	return parseBuildCgroupExecOutput(string(output))
}

// buildCgroupSnapshot is one sample taken at a step's start or end.
// applicable is false outside an injected runtime pod (no build cgroup to
// read at all, e.g. a bare host build or a `type: runtime` env); ok is false
// when a pod-injected build's remote read failed for any reason.
type buildCgroupSnapshot struct {
	applicable bool
	ok         bool
	counters   buildCgroupCounters
}

func captureBuildCgroupSnapshot() buildCgroupSnapshot {
	base := buildCgroupMetricsDir()
	if base == "" {
		return buildCgroupSnapshot{}
	}
	pod, err := os.Hostname()
	if err != nil || strings.TrimSpace(pod) == "" {
		return buildCgroupSnapshot{applicable: true, ok: false}
	}
	counters, ok := sampleBuildCgroup(pod, base)
	return buildCgroupSnapshot{applicable: true, ok: ok, counters: counters}
}

// BuildCgroupMetrics is one step's CPU/throttling/I/O cost, derived from the
// delta between two buildCgroupSnapshots taken at that step's start and end.
// nil (never constructed) when the step ran outside an injected runtime pod,
// since there is no build cgroup to have an opinion about; Available is false
// when a pod-injected build's counters were not readable, so a caller can
// always tell "not applicable here" (nil) apart from "should have worked but
// didn't" (Available: false, Unavailable: reason).
type BuildCgroupMetrics struct {
	Available         bool    `json:"available"`
	Unavailable       string  `json:"unavailable,omitempty"`
	CPUSeconds        float64 `json:"cpuSeconds,omitempty"`
	CPUPercentOfQuota float64 `json:"cpuPercentOfQuota,omitempty"`
	ThrottledPeriods  int64   `json:"throttledPeriods,omitempty"`
	TotalPeriods      int64   `json:"totalPeriods,omitempty"`
	ThrottledSeconds  float64 `json:"throttledSeconds,omitempty"`
	IOReadBytes       int64   `json:"ioReadBytes,omitempty"`
	IOWriteBytes      int64   `json:"ioWriteBytes,omitempty"`
	PeakMemoryBytes   int64   `json:"peakMemoryBytes,omitempty"`
}

// buildCgroupMetricsFromSnapshots never fails a build over unreadable
// instrumentation: an unreadable snapshot yields Available: false, not an
// error, and a step outside an injected runtime pod yields nil -- one fewer
// field for a caller (or a human reading the JSON record) to wonder about.
func buildCgroupMetricsFromSnapshots(before, after buildCgroupSnapshot, elapsed time.Duration) *BuildCgroupMetrics {
	if !before.applicable {
		return nil
	}
	if !before.ok || !after.ok {
		return &BuildCgroupMetrics{Unavailable: "build cgroup counters were not readable"}
	}
	deltaUsage := nonNegativeDelta(before.counters.usageUsec, after.counters.usageUsec)
	metrics := &BuildCgroupMetrics{
		Available:        true,
		CPUSeconds:       float64(deltaUsage) / 1e6,
		ThrottledPeriods: nonNegativeDelta(before.counters.throttledPeriods, after.counters.throttledPeriods),
		TotalPeriods:     nonNegativeDelta(before.counters.periods, after.counters.periods),
		ThrottledSeconds: float64(nonNegativeDelta(before.counters.throttledUsec, after.counters.throttledUsec)) / 1e6,
		IOReadBytes:      nonNegativeDelta(before.counters.ioReadBytes, after.counters.ioReadBytes),
		IOWriteBytes:     nonNegativeDelta(before.counters.ioWriteBytes, after.counters.ioWriteBytes),
	}
	if after.counters.peakObserved {
		metrics.PeakMemoryBytes = after.counters.peakMemoryBytes
	}
	if after.counters.quotaCores > 0 && elapsed > 0 {
		metrics.CPUPercentOfQuota = 100 * metrics.CPUSeconds / (after.counters.quotaCores * elapsed.Seconds())
	}
	return metrics
}

// buildCgroupSummary renders a step-timing table row's trailing cgroup
// detail — cpu cost against quota, throttling (the "starved vs merely busy"
// distinction #2266 needed), and I/O — or "" when there is nothing to add
// (no cgroup applicable, or an unavailable read already implied by its
// absence from the table).
func buildCgroupSummary(m *BuildCgroupMetrics) string {
	if m == nil || !m.Available {
		return ""
	}
	summary := fmt.Sprintf(" cpu=%.1fs", m.CPUSeconds)
	if m.CPUPercentOfQuota > 0 {
		summary += fmt.Sprintf(" (%.0f%% of quota)", m.CPUPercentOfQuota)
	}
	if m.TotalPeriods > 0 {
		summary += fmt.Sprintf(" throttled %d/%d periods", m.ThrottledPeriods, m.TotalPeriods)
	}
	if m.IOReadBytes > 0 || m.IOWriteBytes > 0 {
		summary += fmt.Sprintf(" io=%s read/%s written", formatMebibytes(m.IOReadBytes), formatMebibytes(m.IOWriteBytes))
	}
	return summary
}

// nonNegativeDelta clamps a counter difference at zero. Cumulative cgroup
// counters never decrease in the ordinary case; a negative delta means the
// cgroup was recreated between samples (a pod restart mid-build), and
// reporting that as a negative cost would be more misleading than zero.
func nonNegativeDelta(before, after int64) int64 {
	if after < before {
		return 0
	}
	return after - before
}
