package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// buildCgroupSysFSRoot is where cgroup v2 is mounted inside the runtime pod.
// Unlike ReadLocalRuntimeUsage (runtime_usage.go), which reads this process's
// *own* cgroup, the build cgroup lives at a fixed, unrelated path this process
// shares only because it runs in the same pod's cgroup namespace as the
// erun-dind sidecar that created it (see build_cpu_cap.go).
const buildCgroupSysFSRoot = "/sys/fs/cgroup"

// buildCgroupMetricsDir resolves the filesystem directory this process can
// read the build's own capped cgroup from -- the same deterministic path
// buildContainerCPUCapCgroupParent hands docker as --cgroup-parent, rooted at
// the real cgroup v2 mount rather than the bare cgroup path that flag expects.
// Empty outside an injected runtime pod, where no such cgroup exists.
func buildCgroupMetricsDir() string {
	parent := buildContainerCPUCapCgroupParent()
	if parent == "" {
		return ""
	}
	return filepath.Join(buildCgroupSysFSRoot, parent)
}

// buildCgroupCandidateDirs orders the directories worth sampling for one
// build cgroup root, most-authoritative first. cgroup v2 aggregates a
// descendant's resource accounting into every ancestor's own stat files, so
// the cap cgroup itself (dind-entrypoint.sh's mkdir target) already reflects
// every RUN-instruction container docker nests under it -- but that
// aggregation depends on the parent directory having its own stat files
// populated, which is not guaranteed on every cgroup driver/version
// combination. "buildkit" is checked as a fallback single level down in case
// the parent itself never gets populated.
func buildCgroupCandidateDirs(root string) []string {
	if root == "" {
		return nil
	}
	return []string{root, filepath.Join(root, "buildkit")}
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

// readBuildCgroupCounters reads one candidate directory's counters. It
// succeeds only when cpu.stat's usage_usec is readable -- the minimum needed
// to attribute any cost at all -- and degrades every other field
// independently, matching runtimeMemoryUsageFromValues/
// runtimeCPUUsageFromValues's per-field fail-soft posture in runtime_usage.go.
func readBuildCgroupCounters(dir string) (buildCgroupCounters, bool) {
	usage, ok := parseRuntimeInt64(localCgroupStatValue(filepath.Join(dir, "cpu.stat"), "usage_usec"))
	if !ok {
		return buildCgroupCounters{}, false
	}
	counters := buildCgroupCounters{usageUsec: usage}
	counters.periods, _ = parseRuntimeInt64(localCgroupStatValue(filepath.Join(dir, "cpu.stat"), "nr_periods"))
	counters.throttledPeriods, _ = parseRuntimeInt64(localCgroupStatValue(filepath.Join(dir, "cpu.stat"), "nr_throttled"))
	counters.throttledUsec, _ = parseRuntimeInt64(localCgroupStatValue(filepath.Join(dir, "cpu.stat"), "throttled_usec"))
	if quotaUsec, periodUsec, ok := parseRuntimeCPUMax(readLocalCgroupFile(filepath.Join(dir, "cpu.max"))); ok {
		counters.quotaCores = float64(quotaUsec) / float64(periodUsec)
	}
	counters.ioReadBytes, counters.ioWriteBytes = readBuildCgroupIOBytes(filepath.Join(dir, "io.stat"))
	if peak, ok := parseRuntimeInt64(readLocalCgroupFile(filepath.Join(dir, "memory.peak"))); ok {
		counters.peakMemoryBytes = peak
		counters.peakObserved = true
	}
	return counters, true
}

// readBuildCgroupIOBytes sums io.stat's rbytes/wbytes across every device
// line -- a build touches at least the docker state device and often an
// overlay/tmpfs mount too, and the per-step question ("how much I/O did this
// step do") wants the total, not one device chosen arbitrarily.
func readBuildCgroupIOBytes(path string) (readBytes, writeBytes int64) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
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

// sampleBuildCgroup reads the first candidate directory with a readable
// cpu.stat, so a fallback to the "buildkit" child only happens when the
// parent genuinely has nothing.
func sampleBuildCgroup(root string) (buildCgroupCounters, bool) {
	for _, dir := range buildCgroupCandidateDirs(root) {
		if counters, ok := readBuildCgroupCounters(dir); ok {
			return counters, true
		}
	}
	return buildCgroupCounters{}, false
}

// buildCgroupSnapshot is one sample taken at a step's start or end.
// applicable is false outside an injected runtime pod (no build cgroup to
// read at all, e.g. a bare host build or a `type: runtime` env); ok is false
// when a pod-injected build's cgroup files were not readable at this instant
// (an older runtime image predating #2257, or the sidecar has not run its
// mirroring step yet).
type buildCgroupSnapshot struct {
	applicable bool
	ok         bool
	counters   buildCgroupCounters
}

func captureBuildCgroupSnapshot() buildCgroupSnapshot {
	dir := buildCgroupMetricsDir()
	if dir == "" {
		return buildCgroupSnapshot{}
	}
	counters, ok := sampleBuildCgroup(dir)
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
