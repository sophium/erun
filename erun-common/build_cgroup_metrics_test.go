package eruncommon

import (
	"strings"
	"testing"
	"time"
)

func normalBuildCgroupExecOutputFixture() string {
	return "usage_usec=12345678\n" +
		"nr_periods=200\n" +
		"nr_throttled=40\n" +
		"throttled_usec=987654\n" +
		"cpu_max=400000 100000\n" +
		"memory_peak=104857600\n" +
		"io_stat_begin\n" +
		"8:0 rbytes=1048576 wbytes=2097152 rios=10 wios=20 dbytes=0 dios=0\n" +
		"253:0 rbytes=512 wbytes=1024 rios=1 wios=1 dbytes=0 dios=0\n" +
		"io_stat_end\n"
}

func TestParseBuildCgroupExecOutputNormal(t *testing.T) {
	counters, ok := parseBuildCgroupExecOutput(normalBuildCgroupExecOutputFixture())
	if !ok {
		t.Fatalf("expected a readable normal fixture")
	}
	if counters.usageUsec != 12345678 {
		t.Errorf("usageUsec = %d, want 12345678", counters.usageUsec)
	}
	if counters.periods != 200 || counters.throttledPeriods != 40 {
		t.Errorf("periods = %d/%d, want 200/40", counters.throttledPeriods, counters.periods)
	}
	if counters.throttledUsec != 987654 {
		t.Errorf("throttledUsec = %d, want 987654", counters.throttledUsec)
	}
	if counters.quotaCores != 4 {
		t.Errorf("quotaCores = %v, want 4", counters.quotaCores)
	}
}

func TestParseBuildCgroupExecOutputNormalIOAndPeak(t *testing.T) {
	counters, ok := parseBuildCgroupExecOutput(normalBuildCgroupExecOutputFixture())
	if !ok {
		t.Fatalf("expected a readable normal fixture")
	}
	if counters.ioReadBytes != 1048576+512 || counters.ioWriteBytes != 2097152+1024 {
		t.Errorf("io = %d/%d, want summed across both device lines", counters.ioReadBytes, counters.ioWriteBytes)
	}
	if !counters.peakObserved || counters.peakMemoryBytes != 104857600 {
		t.Errorf("peak = observed=%v bytes=%d, want observed=true bytes=104857600", counters.peakObserved, counters.peakMemoryBytes)
	}
}

func TestParseBuildCgroupExecOutputUnlimitedQuota(t *testing.T) {
	output := "usage_usec=500\nnr_periods=1\nnr_throttled=0\nthrottled_usec=0\ncpu_max=max 100000\n"
	counters, ok := parseBuildCgroupExecOutput(output)
	if !ok {
		t.Fatalf("expected usage_usec alone to make this readable")
	}
	if counters.quotaCores != 0 {
		t.Errorf("quotaCores = %v, want 0 for an unlimited cgroup.max", counters.quotaCores)
	}
}

func TestParseBuildCgroupExecOutputEmpty(t *testing.T) {
	if _, ok := parseBuildCgroupExecOutput(""); ok {
		t.Fatalf("expected empty output (e.g. a failed exec) to be unreadable")
	}
}

func TestParseBuildCgroupExecOutputMissingUsageUsec(t *testing.T) {
	output := "nr_periods=1\nnr_throttled=0\n"
	if _, ok := parseBuildCgroupExecOutput(output); ok {
		t.Fatalf("expected output with no usage_usec key to be unreadable")
	}
}

func TestParseBuildCgroupExecOutputMalformedIOStatIsSkippedNotFatal(t *testing.T) {
	output := "usage_usec=1\nnr_periods=1\nnr_throttled=0\n" +
		"io_stat_begin\n" +
		"garbage line with no equals signs\n" +
		"8:0 rbytes=notanumber wbytes=1024\n" +
		"io_stat_end\n"
	counters, ok := parseBuildCgroupExecOutput(output)
	if !ok {
		t.Fatalf("expected usage_usec alone to make this readable despite malformed io.stat")
	}
	if counters.ioReadBytes != 0 {
		t.Errorf("ioReadBytes = %d, want 0 for an unparseable rbytes value", counters.ioReadBytes)
	}
	if counters.ioWriteBytes != 1024 {
		t.Errorf("ioWriteBytes = %d, want 1024 (the one parseable field)", counters.ioWriteBytes)
	}
}

func TestParseBuildCgroupExecOutputMissingMemoryPeakIsUnobserved(t *testing.T) {
	output := "usage_usec=1\nnr_periods=1\nnr_throttled=0\n"
	counters, ok := parseBuildCgroupExecOutput(output)
	if !ok {
		t.Fatalf("expected usage_usec alone to make this readable")
	}
	if counters.peakObserved {
		t.Errorf("expected peakObserved=false when memory_peak is absent from the output")
	}
}

func TestParseBuildCgroupExecOutputMissingIOStatMarkersYieldsZero(t *testing.T) {
	output := "usage_usec=1\nnr_periods=1\nnr_throttled=0\n"
	counters, ok := parseBuildCgroupExecOutput(output)
	if !ok {
		t.Fatalf("expected usage_usec alone to make this readable")
	}
	if counters.ioReadBytes != 0 || counters.ioWriteBytes != 0 {
		t.Errorf("io = %d/%d, want 0/0 with no io_stat markers present", counters.ioReadBytes, counters.ioWriteBytes)
	}
}

func TestBuildCgroupReadScriptEmbedsTheResolvedBaseDirectory(t *testing.T) {
	script := buildCgroupReadScript("/sys/fs/cgroup/docker/erun-build-cpu-cap-my-pod")
	if !strings.Contains(script, `dir="/sys/fs/cgroup/docker/erun-build-cpu-cap-my-pod"`) {
		t.Errorf("expected the script to embed the resolved base dir, got:\n%s", script)
	}
	if !strings.Contains(script, "/buildkit") {
		t.Errorf("expected the script to carry a buildkit-child fallback, got:\n%s", script)
	}
}

func TestBuildCgroupMetricsFromSnapshotsNotApplicableOutsideRuntimePod(t *testing.T) {
	metrics := buildCgroupMetricsFromSnapshots(buildCgroupSnapshot{}, buildCgroupSnapshot{}, time.Second)
	if metrics != nil {
		t.Fatalf("expected nil when the before snapshot was not applicable, got %+v", metrics)
	}
}

func TestBuildCgroupMetricsFromSnapshotsUnavailableWhenUnreadable(t *testing.T) {
	before := buildCgroupSnapshot{applicable: true, ok: false}
	after := buildCgroupSnapshot{applicable: true, ok: true}
	metrics := buildCgroupMetricsFromSnapshots(before, after, time.Second)
	if metrics == nil || metrics.Available || metrics.Unavailable == "" {
		t.Fatalf("expected Available=false with a reason, got %+v", metrics)
	}
}

func buildCgroupDeltaAndUtilizationFixture() (buildCgroupSnapshot, buildCgroupSnapshot) {
	before := buildCgroupSnapshot{applicable: true, ok: true, counters: buildCgroupCounters{
		usageUsec: 1_000_000, periods: 10, throttledPeriods: 1, throttledUsec: 100_000,
		ioReadBytes: 1000, ioWriteBytes: 2000,
	}}
	after := buildCgroupSnapshot{applicable: true, ok: true, counters: buildCgroupCounters{
		usageUsec: 3_000_000, periods: 20, throttledPeriods: 5, throttledUsec: 300_000,
		ioReadBytes: 1500, ioWriteBytes: 2500, quotaCores: 2, peakMemoryBytes: 555, peakObserved: true,
	}}
	return before, after
}

func TestBuildCgroupMetricsFromSnapshotsComputesDeltasAndUtilization(t *testing.T) {
	before, after := buildCgroupDeltaAndUtilizationFixture()
	metrics := buildCgroupMetricsFromSnapshots(before, after, time.Second)
	if metrics == nil || !metrics.Available {
		t.Fatalf("expected an available reading, got %+v", metrics)
	}
	if metrics.CPUSeconds != 2 {
		t.Errorf("CPUSeconds = %v, want 2 (2,000,000 usec / 1e6)", metrics.CPUSeconds)
	}
	if metrics.CPUPercentOfQuota != 100 {
		t.Errorf("CPUPercentOfQuota = %v, want 100 (2 CPU-seconds of a 2-core quota over 1s)", metrics.CPUPercentOfQuota)
	}
	if metrics.ThrottledPeriods != 4 || metrics.TotalPeriods != 10 {
		t.Errorf("throttled/total = %d/%d, want 4/10", metrics.ThrottledPeriods, metrics.TotalPeriods)
	}
	if metrics.ThrottledSeconds != 0.2 {
		t.Errorf("ThrottledSeconds = %v, want 0.2", metrics.ThrottledSeconds)
	}
}

func TestBuildCgroupMetricsFromSnapshotsComputesIOAndPeakDeltas(t *testing.T) {
	before, after := buildCgroupDeltaAndUtilizationFixture()
	metrics := buildCgroupMetricsFromSnapshots(before, after, time.Second)
	if metrics == nil || !metrics.Available {
		t.Fatalf("expected an available reading, got %+v", metrics)
	}
	if metrics.IOReadBytes != 500 || metrics.IOWriteBytes != 500 {
		t.Errorf("io deltas = %d/%d, want 500/500", metrics.IOReadBytes, metrics.IOWriteBytes)
	}
	if metrics.PeakMemoryBytes != 555 {
		t.Errorf("PeakMemoryBytes = %d, want 555 (the after snapshot's peak)", metrics.PeakMemoryBytes)
	}
}

func TestBuildCgroupMetricsFromSnapshotsClampsNegativeDeltaToZero(t *testing.T) {
	// Simulates the cgroup being recreated between samples (e.g. a pod
	// restart mid-build): counters go backwards rather than forwards.
	before := buildCgroupSnapshot{applicable: true, ok: true, counters: buildCgroupCounters{usageUsec: 5_000_000, periods: 50}}
	after := buildCgroupSnapshot{applicable: true, ok: true, counters: buildCgroupCounters{usageUsec: 1_000_000, periods: 10}}
	metrics := buildCgroupMetricsFromSnapshots(before, after, time.Second)
	if metrics.CPUSeconds != 0 {
		t.Errorf("CPUSeconds = %v, want 0 when the counter went backwards", metrics.CPUSeconds)
	}
	if metrics.TotalPeriods != 0 {
		t.Errorf("TotalPeriods = %d, want 0 when the counter went backwards", metrics.TotalPeriods)
	}
}

func TestBuildCgroupSummaryOmitsUnavailableAndNil(t *testing.T) {
	if got := buildCgroupSummary(nil); got != "" {
		t.Errorf("nil metrics: got %q, want empty", got)
	}
	if got := buildCgroupSummary(&BuildCgroupMetrics{Available: false, Unavailable: "no cgroup"}); got != "" {
		t.Errorf("unavailable metrics: got %q, want empty", got)
	}
}

func TestBuildCgroupSummaryIncludesThrottlingWhenPresent(t *testing.T) {
	summary := buildCgroupSummary(&BuildCgroupMetrics{
		Available: true, CPUSeconds: 12.3, CPUPercentOfQuota: 87, ThrottledPeriods: 9, TotalPeriods: 65,
	})
	for _, want := range []string{"cpu=12.3s", "87% of quota", "throttled 9/65 periods"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q missing %q", summary, want)
		}
	}
}
