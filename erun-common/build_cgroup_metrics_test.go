package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadBuildCgroupCountersParsesNormalFixture(t *testing.T) {
	dir := t.TempDir()
	writeCgroupFixture(t, dir, map[string]string{
		"cpu.stat": "usage_usec 12345678\n" +
			"user_usec 10000000\n" +
			"system_usec 2345678\n" +
			"nr_periods 200\n" +
			"nr_throttled 40\n" +
			"throttled_usec 987654\n",
		"cpu.max":     "400000 100000\n",
		"io.stat":     "8:0 rbytes=1048576 wbytes=2097152 rios=10 wios=20 dbytes=0 dios=0\n253:0 rbytes=512 wbytes=1024 rios=1 wios=1 dbytes=0 dios=0\n",
		"memory.peak": "104857600\n",
	})

	counters, ok := readBuildCgroupCounters(dir)
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
	if counters.ioReadBytes != 1048576+512 || counters.ioWriteBytes != 2097152+1024 {
		t.Errorf("io = %d/%d, want summed across both device lines", counters.ioReadBytes, counters.ioWriteBytes)
	}
	if !counters.peakObserved || counters.peakMemoryBytes != 104857600 {
		t.Errorf("peak = observed=%v bytes=%d, want observed=true bytes=104857600", counters.peakObserved, counters.peakMemoryBytes)
	}
}

func TestReadBuildCgroupCountersUnlimitedQuota(t *testing.T) {
	dir := t.TempDir()
	writeCgroupFixture(t, dir, map[string]string{
		"cpu.stat": "usage_usec 500\nnr_periods 1\nnr_throttled 0\nthrottled_usec 0\n",
		"cpu.max":  "max 100000\n",
	})

	counters, ok := readBuildCgroupCounters(dir)
	if !ok {
		t.Fatalf("expected usage_usec alone to make this readable")
	}
	if counters.quotaCores != 0 {
		t.Errorf("quotaCores = %v, want 0 for an unlimited cgroup.max", counters.quotaCores)
	}
}

func TestReadBuildCgroupCountersMissingDirectory(t *testing.T) {
	_, ok := readBuildCgroupCounters(filepath.Join(t.TempDir(), "does-not-exist"))
	if ok {
		t.Fatalf("expected a missing directory to be unreadable, not ok")
	}
}

func TestReadBuildCgroupCountersMalformedCPUStat(t *testing.T) {
	dir := t.TempDir()
	writeCgroupFixture(t, dir, map[string]string{
		"cpu.stat": "not a keyed stat file at all\n",
	})
	_, ok := readBuildCgroupCounters(dir)
	if ok {
		t.Fatalf("expected a cpu.stat with no usage_usec key to be unreadable")
	}
}

func TestReadBuildCgroupCountersEmptyCPUStat(t *testing.T) {
	dir := t.TempDir()
	writeCgroupFixture(t, dir, map[string]string{"cpu.stat": ""})
	_, ok := readBuildCgroupCounters(dir)
	if ok {
		t.Fatalf("expected an empty cpu.stat to be unreadable")
	}
}

func TestReadBuildCgroupCountersMalformedIOStatIsSkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	writeCgroupFixture(t, dir, map[string]string{
		"cpu.stat": "usage_usec 1\nnr_periods 1\nnr_throttled 0\n",
		"io.stat":  "garbage line with no equals signs\n8:0 rbytes=notanumber wbytes=1024\n",
	})
	counters, ok := readBuildCgroupCounters(dir)
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

func TestReadBuildCgroupCountersMissingMemoryPeakIsUnobserved(t *testing.T) {
	dir := t.TempDir()
	writeCgroupFixture(t, dir, map[string]string{
		"cpu.stat": "usage_usec 1\nnr_periods 1\nnr_throttled 0\n",
	})
	counters, ok := readBuildCgroupCounters(dir)
	if !ok {
		t.Fatalf("expected usage_usec alone to make this readable")
	}
	if counters.peakObserved {
		t.Errorf("expected peakObserved=false when memory.peak is missing")
	}
}

func TestSampleBuildCgroupFallsBackToBuildkitChild(t *testing.T) {
	dir := t.TempDir()
	buildkitDir := filepath.Join(dir, "buildkit")
	if err := os.MkdirAll(buildkitDir, 0o755); err != nil {
		t.Fatalf("mkdir buildkit child: %v", err)
	}
	// The parent has no readable cpu.stat at all (e.g. never populated on this
	// cgroup driver); "buildkit" underneath it does.
	writeCgroupFixture(t, buildkitDir, map[string]string{
		"cpu.stat": "usage_usec 42\nnr_periods 1\nnr_throttled 0\n",
	})
	counters, ok := sampleBuildCgroup(dir)
	if !ok {
		t.Fatalf("expected the buildkit child fallback to be readable")
	}
	if counters.usageUsec != 42 {
		t.Errorf("usageUsec = %d, want 42 (from the buildkit child)", counters.usageUsec)
	}
}

func TestSampleBuildCgroupPrefersParentOverBuildkitChild(t *testing.T) {
	dir := t.TempDir()
	writeCgroupFixture(t, dir, map[string]string{
		"cpu.stat": "usage_usec 100\nnr_periods 1\nnr_throttled 0\n",
	})
	buildkitDir := filepath.Join(dir, "buildkit")
	if err := os.MkdirAll(buildkitDir, 0o755); err != nil {
		t.Fatalf("mkdir buildkit child: %v", err)
	}
	writeCgroupFixture(t, buildkitDir, map[string]string{
		"cpu.stat": "usage_usec 999\nnr_periods 1\nnr_throttled 0\n",
	})
	counters, ok := sampleBuildCgroup(dir)
	if !ok {
		t.Fatalf("expected the parent to be readable")
	}
	if counters.usageUsec != 100 {
		t.Errorf("usageUsec = %d, want 100 (the parent wins when readable)", counters.usageUsec)
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

func TestBuildCgroupMetricsFromSnapshotsComputesDeltasAndUtilization(t *testing.T) {
	before := buildCgroupSnapshot{applicable: true, ok: true, counters: buildCgroupCounters{
		usageUsec: 1_000_000, periods: 10, throttledPeriods: 1, throttledUsec: 100_000,
		ioReadBytes: 1000, ioWriteBytes: 2000,
	}}
	after := buildCgroupSnapshot{applicable: true, ok: true, counters: buildCgroupCounters{
		usageUsec: 3_000_000, periods: 20, throttledPeriods: 5, throttledUsec: 300_000,
		ioReadBytes: 1500, ioWriteBytes: 2500, quotaCores: 2, peakMemoryBytes: 555, peakObserved: true,
	}}
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
