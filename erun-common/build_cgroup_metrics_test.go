package eruncommon

import (
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
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
	counters, ok := core.ParseBuildCgroupExecOutput(normalBuildCgroupExecOutputFixture())
	if !ok {
		t.Fatalf("expected a readable normal fixture")
	}
	if counters.UsageUsec != 12345678 {
		t.Errorf("usageUsec = %d, want 12345678", counters.UsageUsec)
	}
	if counters.Periods != 200 || counters.ThrottledPeriods != 40 {
		t.Errorf("periods = %d/%d, want 200/40", counters.ThrottledPeriods, counters.Periods)
	}
	if counters.ThrottledUsec != 987654 {
		t.Errorf("throttledUsec = %d, want 987654", counters.ThrottledUsec)
	}
	if counters.QuotaCores != 4 {
		t.Errorf("quotaCores = %v, want 4", counters.QuotaCores)
	}
}

func TestParseBuildCgroupExecOutputNormalIOAndPeak(t *testing.T) {
	counters, ok := core.ParseBuildCgroupExecOutput(normalBuildCgroupExecOutputFixture())
	if !ok {
		t.Fatalf("expected a readable normal fixture")
	}
	if counters.IoReadBytes != 1048576+512 || counters.IoWriteBytes != 2097152+1024 {
		t.Errorf("io = %d/%d, want summed across both device lines", counters.IoReadBytes, counters.IoWriteBytes)
	}
	if !counters.PeakObserved || counters.PeakMemoryBytes != 104857600 {
		t.Errorf("peak = observed=%v bytes=%d, want observed=true bytes=104857600", counters.PeakObserved, counters.PeakMemoryBytes)
	}
}

func TestParseBuildCgroupExecOutputUnlimitedQuota(t *testing.T) {
	output := "usage_usec=500\nnr_periods=1\nnr_throttled=0\nthrottled_usec=0\ncpu_max=max 100000\n"
	counters, ok := core.ParseBuildCgroupExecOutput(output)
	if !ok {
		t.Fatalf("expected usage_usec alone to make this readable")
	}
	if counters.QuotaCores != 0 {
		t.Errorf("quotaCores = %v, want 0 for an unlimited cgroup.max", counters.QuotaCores)
	}
}

func TestParseBuildCgroupExecOutputEmpty(t *testing.T) {
	if _, ok := core.ParseBuildCgroupExecOutput(""); ok {
		t.Fatalf("expected empty output (e.g. a failed exec) to be unreadable")
	}
}

func TestParseBuildCgroupExecOutputMissingUsageUsec(t *testing.T) {
	output := "nr_periods=1\nnr_throttled=0\n"
	if _, ok := core.ParseBuildCgroupExecOutput(output); ok {
		t.Fatalf("expected output with no usage_usec key to be unreadable")
	}
}

func TestParseBuildCgroupExecOutputMalformedIOStatIsSkippedNotFatal(t *testing.T) {
	output := "usage_usec=1\nnr_periods=1\nnr_throttled=0\n" +
		"io_stat_begin\n" +
		"garbage line with no equals signs\n" +
		"8:0 rbytes=notanumber wbytes=1024\n" +
		"io_stat_end\n"
	counters, ok := core.ParseBuildCgroupExecOutput(output)
	if !ok {
		t.Fatalf("expected usage_usec alone to make this readable despite malformed io.stat")
	}
	if counters.IoReadBytes != 0 {
		t.Errorf("ioReadBytes = %d, want 0 for an unparseable rbytes value", counters.IoReadBytes)
	}
	if counters.IoWriteBytes != 1024 {
		t.Errorf("ioWriteBytes = %d, want 1024 (the one parseable field)", counters.IoWriteBytes)
	}
}

func TestParseBuildCgroupExecOutputMissingMemoryPeakIsUnobserved(t *testing.T) {
	output := "usage_usec=1\nnr_periods=1\nnr_throttled=0\n"
	counters, ok := core.ParseBuildCgroupExecOutput(output)
	if !ok {
		t.Fatalf("expected usage_usec alone to make this readable")
	}
	if counters.PeakObserved {
		t.Errorf("expected peakObserved=false when memory_peak is absent from the output")
	}
}

func TestParseBuildCgroupExecOutputMissingIOStatMarkersYieldsZero(t *testing.T) {
	output := "usage_usec=1\nnr_periods=1\nnr_throttled=0\n"
	counters, ok := core.ParseBuildCgroupExecOutput(output)
	if !ok {
		t.Fatalf("expected usage_usec alone to make this readable")
	}
	if counters.IoReadBytes != 0 || counters.IoWriteBytes != 0 {
		t.Errorf("io = %d/%d, want 0/0 with no io_stat markers present", counters.IoReadBytes, counters.IoWriteBytes)
	}
}

func TestBuildCgroupReadScriptEmbedsTheResolvedBaseDirectory(t *testing.T) {
	script := core.BuildCgroupReadScript("/sys/fs/cgroup/docker/erun-build-cpu-cap-my-pod")
	if !strings.Contains(script, `dir="/sys/fs/cgroup/docker/erun-build-cpu-cap-my-pod"`) {
		t.Errorf("expected the script to embed the resolved base dir, got:\n%s", script)
	}
	if !strings.Contains(script, "/buildkit") {
		t.Errorf("expected the script to carry a buildkit-child fallback, got:\n%s", script)
	}
}

func TestBuildCgroupMetricsFromSnapshotsNotApplicableOutsideRuntimePod(t *testing.T) {
	metrics := core.BuildCgroupMetricsFromSnapshots(core.BuildCgroupSnapshot{}, core.BuildCgroupSnapshot{}, time.Second)
	if metrics != nil {
		t.Fatalf("expected nil when the before snapshot was not applicable, got %+v", metrics)
	}
}

func TestBuildCgroupMetricsFromSnapshotsUnavailableWhenUnreadable(t *testing.T) {
	before := core.BuildCgroupSnapshot{Applicable: true, Ok: false}
	after := core.BuildCgroupSnapshot{Applicable: true, Ok: true}
	metrics := core.BuildCgroupMetricsFromSnapshots(before, after, time.Second)
	if metrics == nil || metrics.Available || metrics.Unavailable == "" {
		t.Fatalf("expected Available=false with a reason, got %+v", metrics)
	}
}

func buildCgroupDeltaAndUtilizationFixture() (core.BuildCgroupSnapshot, core.BuildCgroupSnapshot) {
	before := core.BuildCgroupSnapshot{Applicable: true, Ok: true, Counters: core.BuildCgroupCounters{
		UsageUsec: 1_000_000, Periods: 10, ThrottledPeriods: 1, ThrottledUsec: 100_000,
		IoReadBytes: 1000, IoWriteBytes: 2000,
	}}
	after := core.BuildCgroupSnapshot{Applicable: true, Ok: true, Counters: core.BuildCgroupCounters{
		UsageUsec: 3_000_000, Periods: 20, ThrottledPeriods: 5, ThrottledUsec: 300_000,
		IoReadBytes: 1500, IoWriteBytes: 2500, QuotaCores: 2, PeakMemoryBytes: 555, PeakObserved: true,
	}}
	return before, after
}

func TestBuildCgroupMetricsFromSnapshotsComputesDeltasAndUtilization(t *testing.T) {
	before, after := buildCgroupDeltaAndUtilizationFixture()
	metrics := core.BuildCgroupMetricsFromSnapshots(before, after, time.Second)
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
	metrics := core.BuildCgroupMetricsFromSnapshots(before, after, time.Second)
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
	before := core.BuildCgroupSnapshot{Applicable: true, Ok: true, Counters: core.BuildCgroupCounters{UsageUsec: 5_000_000, Periods: 50}}
	after := core.BuildCgroupSnapshot{Applicable: true, Ok: true, Counters: core.BuildCgroupCounters{UsageUsec: 1_000_000, Periods: 10}}
	metrics := core.BuildCgroupMetricsFromSnapshots(before, after, time.Second)
	if metrics.CPUSeconds != 0 {
		t.Errorf("CPUSeconds = %v, want 0 when the counter went backwards", metrics.CPUSeconds)
	}
	if metrics.TotalPeriods != 0 {
		t.Errorf("TotalPeriods = %d, want 0 when the counter went backwards", metrics.TotalPeriods)
	}
}

func TestBuildCgroupSummaryOmitsUnavailableAndNil(t *testing.T) {
	if got := core.BuildCgroupSummary(nil); got != "" {
		t.Errorf("nil metrics: got %q, want empty", got)
	}
	if got := core.BuildCgroupSummary(&core.BuildCgroupMetrics{Available: false, Unavailable: "no cgroup"}); got != "" {
		t.Errorf("unavailable metrics: got %q, want empty", got)
	}
}

func TestBuildCgroupSummaryIncludesThrottlingWhenPresent(t *testing.T) {
	summary := core.BuildCgroupSummary(&core.BuildCgroupMetrics{
		Available: true, CPUSeconds: 12.3, CPUPercentOfQuota: 87, ThrottledPeriods: 9, TotalPeriods: 65,
	})
	for _, want := range []string{"cpu=12.3s", "87% of quota", "throttled 9/65 periods"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q missing %q", summary, want)
		}
	}
}
