package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	testMi = int64(1024 * 1024)
	testGi = 1024 * testMi
)

// usageHistory builds a history with the aggregates a recommendation reads, plus
// a sample count and window, without running the sampler.
func usageHistory(window time.Duration, samples int, latest RuntimeUsage) RuntimeUsageHistory {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	history := RuntimeUsageHistory{FirstObservedAt: start, LastObservedAt: start.Add(window)}
	for i := 0; i < samples; i++ {
		history.Samples = append(history.Samples, latest)
	}
	if samples == 0 {
		history.Samples = append(history.Samples, latest)
	}
	return history
}

func verdictFor(t *testing.T, recommendation RuntimeSizingRecommendation, resource string) RuntimeSizingVerdict {
	t.Helper()
	for _, verdict := range recommendation.Verdicts {
		if verdict.Resource == resource {
			return verdict
		}
	}
	t.Fatalf("no %s verdict in %+v", resource, recommendation.Verdicts)
	return RuntimeSizingVerdict{}
}

type memoryDirectionCase struct {
	name           string
	history        RuntimeUsageHistory
	ceiling        NamespaceResourceQuota
	wantAction     RuntimeSizingAction
	wantConfidence RuntimeSizingConfidence
	wantSuggested  string
}

// runMemoryDirectionCases asserts one verdict per case and, on every one, that
// the recommendation names runtimepod and carries a reason.
func runMemoryDirectionCases(t *testing.T, cases []memoryDirectionCase) {
	t.Helper()
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recommendation, ok := RecommendRuntimeSizing(RuntimeSizingParams{History: testCase.history, Ceiling: testCase.ceiling})
			if !ok {
				t.Fatal("expected a recommendation")
			}
			if recommendation.Knob != "runtimepod" {
				t.Fatalf("knob = %q, want runtimepod: naming namespacequota sends the operator to a setting that resizes nothing", recommendation.Knob)
			}
			verdict := verdictFor(t, recommendation, "memory")
			if verdict.Action != testCase.wantAction {
				t.Fatalf("action = %q, want %q (reason: %s)", verdict.Action, testCase.wantAction, verdict.Reason)
			}
			if verdict.Confidence != testCase.wantConfidence {
				t.Fatalf("confidence = %q, want %q", verdict.Confidence, testCase.wantConfidence)
			}
			if verdict.Suggested != testCase.wantSuggested {
				t.Fatalf("suggested = %q, want %q", verdict.Suggested, testCase.wantSuggested)
			}
			if verdict.Reason == "" {
				t.Fatal("a verdict with no reason cannot be argued with")
			}
		})
	}
}

// TestRecommendRuntimeSizingRaisesMemory covers the directions taken on
// evidence of harm, which need no long window.
func TestRecommendRuntimeSizingRaisesMemory(t *testing.T) {
	quietWindow := 26 * time.Hour
	quietSamples := 200

	runMemoryDirectionCases(t, []memoryDirectionCase{
		{
			name: "peak at 95 percent of limit raises on high confidence",
			history: func() RuntimeUsageHistory {
				h := usageHistory(quietWindow, quietSamples, RuntimeUsage{Memory: RuntimeMemoryUsage{LimitBytes: 2 * testGi}})
				h.ObservedPeakMemoryBytes = 2*testGi - 100*testMi
				return h
			}(),
			wantAction:     RuntimeSizingRaise,
			wantConfidence: RuntimeSizingConfidenceHigh,
			wantSuggested:  "3072Mi",
		},
		{
			// One kill is enough, and it outranks a comfortable-looking peak:
			// the allocation that was refused never reached memory.peak.
			name: "a single oom kill raises even though the peak looks comfortable",
			history: func() RuntimeUsageHistory {
				h := usageHistory(time.Minute, 2, RuntimeUsage{Memory: RuntimeMemoryUsage{LimitBytes: 2 * testGi}})
				h.ObservedPeakMemoryBytes = 512 * testMi
				h.ObservedOOMKills = 1
				return h
			}(),
			wantAction:     RuntimeSizingRaise,
			wantConfidence: RuntimeSizingConfidenceHigh,
			wantSuggested:  "3072Mi",
		},
		{
			// The quota counts every container in the pod, so a raise above what
			// it can admit is a size Kubernetes would refuse to schedule.
			name: "a raise is bounded by the namespace quota",
			history: func() RuntimeUsageHistory {
				h := usageHistory(quietWindow, quietSamples, RuntimeUsage{Memory: RuntimeMemoryUsage{LimitBytes: 20 * testGi}})
				h.ObservedOOMKills = 2
				return h
			}(),
			ceiling:        NamespaceResourceQuota{CPU: "8", Memory: "32Gi", Storage: "80Gi"},
			wantAction:     RuntimeSizingRaise,
			wantConfidence: RuntimeSizingConfidenceHigh,
			wantSuggested:  "12288Mi",
		},
	})
}

// TestRecommendRuntimeSizingShrinksOrWithholdsMemory covers the directions that
// rest on an absence of harm, and the window gates that must withhold them.
func TestRecommendRuntimeSizingShrinksOrWithholdsMemory(t *testing.T) {
	quietWindow := 26 * time.Hour
	quietSamples := 200

	runMemoryDirectionCases(t, []memoryDirectionCase{
		{
			name: "peak at 20 percent lowers to the headroom multiple",
			history: func() RuntimeUsageHistory {
				h := usageHistory(quietWindow, quietSamples, RuntimeUsage{Memory: RuntimeMemoryUsage{LimitBytes: 10 * testGi}})
				h.ObservedPeakMemoryBytes = 2 * testGi
				return h
			}(),
			wantAction:     RuntimeSizingLower,
			wantConfidence: RuntimeSizingConfidenceLow,
			wantSuggested:  "3072Mi",
		},
		{
			name: "a short window recommends nothing",
			history: func() RuntimeUsageHistory {
				h := usageHistory(time.Hour, 100, RuntimeUsage{Memory: RuntimeMemoryUsage{LimitBytes: 10 * testGi}})
				h.ObservedPeakMemoryBytes = 1 * testGi
				return h
			}(),
			wantAction: RuntimeSizingUnknown,
		},
		{
			// A day of wall time holding a handful of readings watched almost
			// nothing. The span alone is not coverage.
			name: "a long window with too few samples recommends nothing",
			history: func() RuntimeUsageHistory {
				h := usageHistory(quietWindow, 4, RuntimeUsage{Memory: RuntimeMemoryUsage{LimitBytes: 10 * testGi}})
				h.ObservedPeakMemoryBytes = 1 * testGi
				return h
			}(),
			wantAction: RuntimeSizingUnknown,
		},
		{
			name: "no limit is reported as unmeasurable, not as zero",
			history: usageHistory(quietWindow, quietSamples, RuntimeUsage{
				CPU:    RuntimeCPUUsage{QuotaCores: 1},
				Memory: RuntimeMemoryUsage{Unavailable: "memory.max was not readable"},
			}),
			wantAction: RuntimeSizingUnknown,
		},
		{
			name: "a peak just under the raise margin holds rather than shrinking",
			history: func() RuntimeUsageHistory {
				h := usageHistory(quietWindow, quietSamples, RuntimeUsage{Memory: RuntimeMemoryUsage{LimitBytes: 10 * testGi}})
				h.ObservedPeakMemoryBytes = 8 * testGi
				return h
			}(),
			wantAction: RuntimeSizingHold,
		},
	})
}

func TestRecommendRuntimeSizingCPUDirections(t *testing.T) {
	quietWindow := 26 * time.Hour
	quietSamples := 200

	cases := []struct {
		name          string
		history       RuntimeUsageHistory
		wantAction    RuntimeSizingAction
		wantSuggested string
	}{
		{
			// The false-positive check the issue names: frs/local's measured
			// 425/308631 throttled periods must not grow the environment.
			name: "a 0.14 percent throttle ratio does not raise cpu",
			history: func() RuntimeUsageHistory {
				h := usageHistory(quietWindow, quietSamples, RuntimeUsage{CPU: RuntimeCPUUsage{QuotaCores: 1}, Memory: RuntimeMemoryUsage{LimitBytes: 2 * testGi}})
				h.ObservedPeriods = 308631
				h.ObservedThrottledPeriods = 425
				h.ObservedPeakCPUMilli = 200
				return h
			}(),
			wantAction: RuntimeSizingHold,
		},
		{
			// The same ratio on a quota roomy enough that the busiest-interval
			// figure would otherwise propose a shrink. Sub-threshold throttling
			// means "tolerable", not "unused", so it must block the shrink — the
			// frs/local case above cannot prove this on its own, because a
			// one-core quota leaves no room to shrink into either way.
			name: "sub-threshold throttling disqualifies a shrink",
			history: func() RuntimeUsageHistory {
				h := usageHistory(quietWindow, quietSamples, RuntimeUsage{CPU: RuntimeCPUUsage{QuotaCores: 4}, Memory: RuntimeMemoryUsage{LimitBytes: 2 * testGi}})
				h.ObservedPeriods = 308631
				h.ObservedThrottledPeriods = 425
				h.ObservedPeakCPUMilli = 200
				return h
			}(),
			wantAction: RuntimeSizingHold,
		},
		{
			name: "a sustained throttle ratio raises cpu",
			history: func() RuntimeUsageHistory {
				h := usageHistory(quietWindow, quietSamples, RuntimeUsage{CPU: RuntimeCPUUsage{QuotaCores: 2}, Memory: RuntimeMemoryUsage{LimitBytes: 2 * testGi}})
				h.ObservedPeriods = 100000
				h.ObservedThrottledPeriods = 12000
				h.ObservedPeakCPUMilli = 1900
				return h
			}(),
			wantAction:    RuntimeSizingRaise,
			wantSuggested: "3",
		},
		{
			// erun/build's real shape: twelve cores, not one throttled period,
			// and a busiest interval well under half the quota.
			name: "an unthrottled quota well above the busiest interval lowers cpu",
			history: func() RuntimeUsageHistory {
				h := usageHistory(quietWindow, quietSamples, RuntimeUsage{CPU: RuntimeCPUUsage{QuotaCores: 12}, Memory: RuntimeMemoryUsage{LimitBytes: 23 * testGi}})
				h.ObservedPeriods = 376556
				h.ObservedThrottledPeriods = 0
				h.ObservedPeakCPUMilli = 4567
				return h
			}(),
			wantAction:    RuntimeSizingLower,
			wantSuggested: "10",
		},
		{
			name: "too few scheduling periods recommends nothing",
			history: func() RuntimeUsageHistory {
				h := usageHistory(quietWindow, quietSamples, RuntimeUsage{CPU: RuntimeCPUUsage{QuotaCores: 1}, Memory: RuntimeMemoryUsage{LimitBytes: 2 * testGi}})
				h.ObservedPeriods = 500
				h.ObservedPeakCPUMilli = 100
				return h
			}(),
			wantAction: RuntimeSizingUnknown,
		},
		{
			name: "no quota is reported as unmeasurable",
			history: usageHistory(quietWindow, quietSamples, RuntimeUsage{
				Memory: RuntimeMemoryUsage{LimitBytes: 2 * testGi},
				CPU:    RuntimeCPUUsage{Unavailable: "cpu.max reports no quota (unlimited or not readable); utilisation needs a quota to measure against"},
			}),
			wantAction: RuntimeSizingUnknown,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recommendation, ok := RecommendRuntimeSizing(RuntimeSizingParams{History: testCase.history})
			if !ok {
				t.Fatal("expected a recommendation")
			}
			verdict := verdictFor(t, recommendation, "cpu")
			if verdict.Action != testCase.wantAction {
				t.Fatalf("action = %q, want %q (reason: %s)", verdict.Action, testCase.wantAction, verdict.Reason)
			}
			if verdict.Suggested != testCase.wantSuggested {
				t.Fatalf("suggested = %q, want %q", verdict.Suggested, testCase.wantSuggested)
			}
		})
	}
}

// TestRecommendRuntimeSizingNeverShrinksBelowHeadroom is the asymmetry gate. A
// recommender tested only on comfortable inputs is untested: the failure that
// matters is a shrink that lands close enough to the observed peak to start
// killing agents, so no input may produce one.
func TestRecommendRuntimeSizingNeverShrinksBelowHeadroom(t *testing.T) {
	limits := []int64{512 * testMi, 2 * testGi, 8704 * testMi, 23552 * testMi, 64 * testGi}
	peakFractions := []float64{0.001, 0.01, 0.09, 0.17, 0.33, 0.4999, 0.5, 0.7, 0.89}
	windows := []time.Duration{0, time.Hour, 23 * time.Hour, 24 * time.Hour, 200 * time.Hour}
	sampleCounts := []int{1, 119, 120, 5000}

	for _, limit := range limits {
		for _, fraction := range peakFractions {
			for _, window := range windows {
				for _, samples := range sampleCounts {
					peak := int64(float64(limit) * fraction)
					history := usageHistory(window, samples, RuntimeUsage{Memory: RuntimeMemoryUsage{LimitBytes: limit}, CPU: RuntimeCPUUsage{QuotaCores: 4}})
					history.ObservedPeakMemoryBytes = peak
					recommendation, ok := RecommendRuntimeSizing(RuntimeSizingParams{History: history})
					if !ok {
						t.Fatal("expected a recommendation")
					}
					verdict := verdictFor(t, recommendation, "memory")
					if verdict.Action != RuntimeSizingLower {
						continue
					}
					suggestedMi, err := ParseKubernetesMemoryToMi(verdict.Suggested)
					if err != nil {
						t.Fatalf("unparseable suggestion %q: %v", verdict.Suggested, err)
					}
					floor := int64(float64(peak) * runtimeSizingMemoryHeadroom)
					if suggestedMi*testMi < floor {
						t.Fatalf("limit=%d peak=%d: suggested %s is below %.0fx the observed peak", limit, peak, verdict.Suggested, runtimeSizingMemoryHeadroom)
					}
					if suggestedMi*testMi >= limit {
						t.Fatalf("limit=%d peak=%d: shrink to %s is not smaller than the current limit", limit, peak, verdict.Suggested)
					}
				}
			}
		}
	}
}

// TestRecommendRuntimeSizingAnyOOMKillRaises is the other half of the gate: a
// single kill must be enough, whatever else the history says.
func TestRecommendRuntimeSizingAnyOOMKillRaises(t *testing.T) {
	for _, window := range []time.Duration{0, time.Minute, 500 * time.Hour} {
		for _, samples := range []int{1, 240} {
			for _, peakFraction := range []float64{0.0, 0.05, 0.5, 0.99} {
				limit := 8 * testGi
				history := usageHistory(window, samples, RuntimeUsage{Memory: RuntimeMemoryUsage{LimitBytes: limit}, CPU: RuntimeCPUUsage{QuotaCores: 4}})
				history.ObservedPeakMemoryBytes = int64(float64(limit) * peakFraction)
				history.ObservedOOMKills = 1
				recommendation, ok := RecommendRuntimeSizing(RuntimeSizingParams{History: history})
				if !ok {
					t.Fatal("expected a recommendation")
				}
				verdict := verdictFor(t, recommendation, "memory")
				if verdict.Action != RuntimeSizingRaise || verdict.Confidence != RuntimeSizingConfidenceHigh {
					t.Fatalf("window=%s samples=%d peak=%.2f: got %q/%q, want raise/high", window, samples, peakFraction, verdict.Action, verdict.Confidence)
				}
			}
		}
	}
}

func TestRecommendRuntimeSizingWithoutHistoryStaysSilent(t *testing.T) {
	if _, ok := RecommendRuntimeSizing(RuntimeSizingParams{}); ok {
		t.Fatal("an environment erun has never observed must get silence, not a guess")
	}
}

// TestAppendRuntimeUsageSampleRetainsPeakAcrossARestart is the retention
// contract. memory.peak resets when the container restarts, so a history that
// trusted the live counter would forget the pre-restart high-water — the exact
// failure that would under-size an environment.
func TestAppendRuntimeUsageSampleRetainsPeakAcrossARestart(t *testing.T) {
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	history := RuntimeUsageHistory{}
	history = AppendRuntimeUsageSample(history, RuntimeUsage{
		Memory: RuntimeMemoryUsage{LimitBytes: 8 * testGi, PeakBytes: 6 * testGi, OOMKills: 1},
		CPU:    RuntimeCPUUsage{UsageUsec: 1_000_000, Periods: 5000, ThrottledPeriods: 10},
	}, base)
	// A restart: every cumulative counter starts over, and the new peak is
	// lower than the one already observed.
	history = AppendRuntimeUsageSample(history, RuntimeUsage{
		Memory: RuntimeMemoryUsage{LimitBytes: 8 * testGi, PeakBytes: 1 * testGi},
		CPU:    RuntimeCPUUsage{UsageUsec: 100, Periods: 3},
	}, base.Add(time.Hour))

	if history.Restarts != 1 {
		t.Fatalf("restarts = %d, want 1", history.Restarts)
	}
	if history.ObservedPeakMemoryBytes != 6*testGi {
		t.Fatalf("observed peak = %d, want the pre-restart %d", history.ObservedPeakMemoryBytes, 6*testGi)
	}
	if history.ObservedOOMKills != 1 {
		t.Fatalf("oom kills = %d, want the pre-restart kill retained", history.ObservedOOMKills)
	}
	if history.ObservedPeriods != 5003 {
		t.Fatalf("periods = %d, want both lifetimes accumulated", history.ObservedPeriods)
	}

	// And the recommender must size from the retained peak, not the live one:
	// 6Gi of 8Gi is within the raise margin, while the post-restart 1Gi would
	// look like a shrink candidate.
	recommendation, ok := RecommendRuntimeSizing(RuntimeSizingParams{History: history})
	if !ok {
		t.Fatal("expected a recommendation")
	}
	if verdict := verdictFor(t, recommendation, "memory"); verdict.Action != RuntimeSizingRaise {
		t.Fatalf("memory action = %q, want raise from the retained peak (reason: %s)", verdict.Action, verdict.Reason)
	}
}

// TestAppendRuntimeUsageSampleBoundsTheWindow pins that a long-running
// environment cannot grow the history without bound, and — the point of the
// bound coexisting with the aggregates — that the high-water survives the
// eviction of the sample that recorded it. Here the peak is set in the very
// first sample, the container then restarts and runs quietly for long enough to
// push that sample out of the window entirely.
func TestAppendRuntimeUsageSampleBoundsTheWindow(t *testing.T) {
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	history := AppendRuntimeUsageSample(RuntimeUsageHistory{}, RuntimeUsage{
		Memory: RuntimeMemoryUsage{LimitBytes: 8 * testGi, PeakBytes: 7 * testGi},
		CPU:    RuntimeCPUUsage{UsageUsec: 900_000_000, Periods: 90000},
	}, base)
	for i := 1; i < runtimeUsageHistorySampleCap*3; i++ {
		history = AppendRuntimeUsageSample(history, RuntimeUsage{
			Memory: RuntimeMemoryUsage{LimitBytes: 8 * testGi, PeakBytes: 1 * testGi},
			CPU:    RuntimeCPUUsage{UsageUsec: int64(i) * 1_000_000, Periods: int64(i) * 30},
		}, base.Add(time.Duration(i)*30*time.Second))
	}
	if len(history.Samples) != runtimeUsageHistorySampleCap {
		t.Fatalf("samples = %d, want the cap %d", len(history.Samples), runtimeUsageHistorySampleCap)
	}
	if history.ObservedPeakMemoryBytes != 7*testGi {
		t.Fatalf("observed peak = %d, want the evicted sample's %d", history.ObservedPeakMemoryBytes, 7*testGi)
	}
	if history.Restarts != 1 {
		t.Fatalf("restarts = %d, want the single counter reset", history.Restarts)
	}
	if !history.FirstObservedAt.Equal(base) {
		t.Fatalf("first observed = %s, want the window anchor %s", history.FirstObservedAt, base)
	}
	if window := history.ObservedWindow(); window < 5*time.Hour {
		t.Fatalf("observed window = %s, want the full span the evicted samples covered", window)
	}
}

func TestReadLocalRuntimeUsageReadsCgroupV2(t *testing.T) {
	root := t.TempDir()
	writeCgroupFixture(t, root, map[string]string{
		"cpu.max":        "1200000 100000\n",
		"cpu.stat":       "usage_usec 27551234478\nnr_periods 376556\nnr_throttled 0\nthrottled_usec 0\n",
		"memory.max":     "24696061952\n",
		"memory.current": "4773695488\n",
		"memory.peak":    "12742377472\n",
		"memory.events":  "low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\n",
	})
	usage := ReadLocalRuntimeUsage(root)
	if got := runtimeQuotaMilli(usage.CPU.QuotaCores); got != 12000 {
		t.Fatalf("cpu quota = %d milli, want 12000", got)
	}
	if usage.Memory.LimitBytes != 24696061952 || usage.Memory.PeakBytes != 12742377472 {
		t.Fatalf("memory = %+v", usage.Memory)
	}
	if usage.CPU.Periods != 376556 || usage.CPU.ThrottledPeriods != 0 {
		t.Fatalf("cpu = %+v", usage.CPU)
	}
	if !usage.HasCounters() {
		t.Fatal("a full cgroup v2 read must count as counters")
	}
}

// TestReadLocalRuntimeUsageTreatsUnlimitedAsUnavailable pins the distinction
// that matters to every consumer: "no limit configured" is not "a limit of
// zero".
func TestReadLocalRuntimeUsageTreatsUnlimitedAsUnavailable(t *testing.T) {
	root := t.TempDir()
	writeCgroupFixture(t, root, map[string]string{
		"cpu.max":        "max 100000\n",
		"memory.max":     "max\n",
		"memory.current": "1024\n",
	})
	usage := ReadLocalRuntimeUsage(root)
	if usage.Memory.LimitBytes != 0 || usage.CPU.QuotaCores != 0 {
		t.Fatalf("an unlimited cgroup must report no limit, got %+v", usage)
	}
	if !usage.Memory.Unlimited {
		t.Fatal("memory.max=max must set Unlimited, not just leave the limit at zero")
	}
	if usage.CPU.Unavailable == "" {
		t.Fatal("an unreadable cpu quota must be named unavailable")
	}
}

// TestReadLocalRuntimeUsageNeverFailsOnAbsentCounters covers cgroup v1 and any
// host without the files. The reader runs on the monitor's tick, so it must
// degrade rather than break the loop.
func TestReadLocalRuntimeUsageNeverFailsOnAbsentCounters(t *testing.T) {
	usage := ReadLocalRuntimeUsage(t.TempDir() + "/missing")
	if usage.HasCounters() {
		t.Fatal("a host with no cgroup v2 files must report no counters")
	}
	if usage.Memory.Unavailable == "" || usage.CPU.Unavailable == "" {
		t.Fatalf("every field must name its own unavailability, got %+v", usage)
	}
}

func writeCgroupFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}
