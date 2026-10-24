package core

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

const buildCgroupSysFSRoot = "/sys/fs/cgroup"

// buildCgroupDindContainerName is the only container with the host cgroup
// namespace; this container cannot see the cap cgroup, so reads exec into it.
const buildCgroupDindContainerName = "erun-dind"

// buildCgroupExecTimeout keeps a hung kubectl from delaying the build it measures.
const buildCgroupExecTimeout = 5 * time.Second

// buildCgroupMetricsDir is the cap cgroup's path inside the sidecar; empty
// outside an injected runtime pod.
func buildCgroupMetricsDir() string {
	parent := BuildContainerCPUCapCgroupParent()
	if parent == "" {
		return ""
	}
	return filepath.Join(buildCgroupSysFSRoot, parent)
}

// buildCgroupReadScriptTemplate reads a snapshot in one exec, falling back to the
// "buildkit" child when the cap cgroup's own stat files are not populated.
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

func BuildCgroupReadScript(base string) string {
	return strings.ReplaceAll(buildCgroupReadScriptTemplate, "__BASE__", base)
}

// BuildCgroupCounters is one point-in-time reading of the build cgroup.
type BuildCgroupCounters struct {
	QuotaCores       float64
	UsageUsec        int64
	Periods          int64
	ThrottledPeriods int64
	ThrottledUsec    int64
	IoReadBytes      int64
	IoWriteBytes     int64
	PeakMemoryBytes  int64
	PeakObserved     bool
}

// ParseBuildCgroupExecOutput requires usage_usec; every other field degrades independently.
func ParseBuildCgroupExecOutput(output string) (BuildCgroupCounters, bool) {
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

	usage, ok := ParseRuntimeInt64(values["usage_usec"])
	if !ok {
		return BuildCgroupCounters{}, false
	}
	counters := BuildCgroupCounters{UsageUsec: usage}
	counters.Periods, _ = ParseRuntimeInt64(values["nr_periods"])
	counters.ThrottledPeriods, _ = ParseRuntimeInt64(values["nr_throttled"])
	counters.ThrottledUsec, _ = ParseRuntimeInt64(values["throttled_usec"])
	if quotaUsec, periodUsec, ok := ParseRuntimeCPUMax(strings.TrimSpace(values["cpu_max"])); ok {
		counters.QuotaCores = float64(quotaUsec) / float64(periodUsec)
	}
	counters.IoReadBytes, counters.IoWriteBytes = sumBuildCgroupIOBytes(strings.Join(ioLines, "\n"))
	if peak, ok := ParseRuntimeInt64(strings.TrimSpace(values["memory_peak"])); ok {
		counters.PeakMemoryBytes = peak
		counters.PeakObserved = true
	}
	return counters, true
}

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

// sampleBuildCgroup never fails the caller: any exec error yields ok=false.
func sampleBuildCgroup(pod, base string) (BuildCgroupCounters, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), buildCgroupExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "exec", pod, "-c", buildCgroupDindContainerName, "--", "sh", "-c", BuildCgroupReadScript(base))
	output, err := cmd.Output()
	if err != nil {
		return BuildCgroupCounters{}, false
	}
	return ParseBuildCgroupExecOutput(string(output))
}

// BuildCgroupSnapshot is one sample at a step's start or end. applicable is false
// outside an injected runtime pod; ok is false when the read failed.
type BuildCgroupSnapshot struct {
	Applicable bool
	Ok         bool
	Counters   BuildCgroupCounters
}

func CaptureBuildCgroupSnapshot() BuildCgroupSnapshot {
	base := buildCgroupMetricsDir()
	if base == "" {
		return BuildCgroupSnapshot{}
	}
	pod, err := os.Hostname()
	if err != nil || strings.TrimSpace(pod) == "" {
		return BuildCgroupSnapshot{Applicable: true, Ok: false}
	}
	counters, ok := sampleBuildCgroup(pod, base)
	return BuildCgroupSnapshot{Applicable: true, Ok: ok, Counters: counters}
}

// BuildCgroupMetrics is one step's cgroup cost. It is nil when not applicable and
// has Available false when the read failed.
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

func BuildCgroupMetricsFromSnapshots(before, after BuildCgroupSnapshot, elapsed time.Duration) *BuildCgroupMetrics {
	if !before.Applicable {
		return nil
	}
	if !before.Ok || !after.Ok {
		return &BuildCgroupMetrics{Unavailable: "build cgroup counters were not readable"}
	}
	deltaUsage := nonNegativeDelta(before.Counters.UsageUsec, after.Counters.UsageUsec)
	metrics := &BuildCgroupMetrics{
		Available:        true,
		CPUSeconds:       float64(deltaUsage) / 1e6,
		ThrottledPeriods: nonNegativeDelta(before.Counters.ThrottledPeriods, after.Counters.ThrottledPeriods),
		TotalPeriods:     nonNegativeDelta(before.Counters.Periods, after.Counters.Periods),
		ThrottledSeconds: float64(nonNegativeDelta(before.Counters.ThrottledUsec, after.Counters.ThrottledUsec)) / 1e6,
		IOReadBytes:      nonNegativeDelta(before.Counters.IoReadBytes, after.Counters.IoReadBytes),
		IOWriteBytes:     nonNegativeDelta(before.Counters.IoWriteBytes, after.Counters.IoWriteBytes),
	}
	if after.Counters.PeakObserved {
		metrics.PeakMemoryBytes = after.Counters.PeakMemoryBytes
	}
	if after.Counters.QuotaCores > 0 && elapsed > 0 {
		metrics.CPUPercentOfQuota = 100 * metrics.CPUSeconds / (after.Counters.QuotaCores * elapsed.Seconds())
	}
	return metrics
}

// BuildCgroupSummary renders a step row's cgroup detail, or "" when there is none.
func BuildCgroupSummary(m *BuildCgroupMetrics) string {
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
		summary += fmt.Sprintf(" io=%s read/%s written", FormatMebibytes(m.IOReadBytes), FormatMebibytes(m.IOWriteBytes))
	}
	return summary
}

// nonNegativeDelta clamps at zero: a negative delta means the cgroup was recreated mid-build.
func nonNegativeDelta(before, after int64) int64 {
	if after < before {
		return 0
	}
	return after - before
}
