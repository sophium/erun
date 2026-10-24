package podruntime

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

const (
	testGiB        = int64(1024 * 1024 * 1024)
	testMemoryWarn = 6 * testGiB
)

// memoryReadingForPercent derives the reading from raw counters, using the smallest byte
// count that reaches percent, so the crossing itself is tested.
func memoryReadingForPercent(currentPercent, peakPercent float64, limit int64, oomKills int64) config.RuntimeUsage {
	bytesFor := func(percent float64) int64 {
		return int64(math.Ceil(float64(limit) * percent / 100))
	}
	values := map[string]string{
		"cgroup_type":    config.CgroupV2FSType,
		"memory_max":     strconv.FormatInt(limit, 10),
		"memory_peak":    strconv.FormatInt(bytesFor(peakPercent), 10),
		"memory_current": strconv.FormatInt(bytesFor(currentPercent), 10),
		"cpu_max":        "400000 100000",
	}
	if oomKills > 0 {
		values["memory_oom_kill"] = strconv.FormatInt(oomKills, 10)
	} else {
		values["memory_oom_kill"] = "0"
	}
	usage := config.RuntimeUsage{Tenant: "erun", Environment: "code3", Memory: config.RuntimeMemoryUsageFromValues(values)}
	usage.Warnings = runtimeUsageWarnings(usage)
	return usage
}

func saturatingReading(current, limit int64) config.RuntimeUsage {
	return config.RuntimeUsage{
		Tenant:      "erun",
		Environment: "code3",
		Memory: config.RuntimeMemoryUsage{
			CurrentBytes:     current,
			PeakBytes:        limit,
			PeakObserved:     true,
			LimitBytes:       limit,
			PercentOfLimit:   100 * float64(current) / float64(limit),
			OOMKillsObserved: true,
		},
		CPU: config.RuntimeCPUUsage{QuotaCores: 4, Periods: 20000, ThrottledPeriods: 0},
	}
}

func memoryVerdict(t *testing.T, recommendation deploy.RuntimeSizingRecommendation) deploy.RuntimeSizingVerdict {
	t.Helper()
	for _, verdict := range recommendation.Verdicts {
		if verdict.Resource == "memory" {
			return verdict
		}
	}
	t.Fatalf("recommendation carries no memory verdict: %+v", recommendation.Verdicts)
	return deploy.RuntimeSizingVerdict{}
}

// The history is empty on purpose: a host with no retained history is the case at risk.
func TestSaturationWarningAlwaysCarriesItsRecommendation(t *testing.T) {
	limit := 6 * testGiB

	for _, percent := range []float64{deploy.RuntimeUsageMemoryWarnPercent, 86, 88, 90, 95, 100} {
		usage := memoryReadingForPercent(percent, percent, limit, 0)
		if len(usage.Warnings) == 0 {
			t.Fatalf("%.0f%% of the limit fired no warning; the fixture is wrong, not the code", percent)
		}

		recommendation, ok := deploy.RecommendRuntimeSizing(deploy.RuntimeSizingParams{Live: &usage})
		if !ok {
			t.Fatalf("%.0f%% of the limit fired a warning but produced no recommendation at all", percent)
		}
		verdict := memoryVerdict(t, recommendation)
		if verdict.Action != deploy.RuntimeSizingRaise {
			t.Errorf("%.0f%% of the limit warned but the recommendation says %q (%s); a saturated environment must be told what would fix it",
				percent, verdict.Action, verdict.Reason)
		}
		if strings.TrimSpace(verdict.Suggested) == "" {
			t.Errorf("%.0f%% of the limit warned but the raise suggests no size", percent)
		}
		if verdict.Confidence != deploy.RuntimeSizingConfidenceHigh {
			t.Errorf("%.0f%% of the limit: a raise stands on evidence of harm and must be high confidence, got %q",
				percent, verdict.Confidence)
		}
		if strings.TrimSpace(verdict.Reason) == "" {
			t.Errorf("%.0f%% of the limit: raise carries no reason", percent)
		}
	}
}

func TestPeakOnlyWarningAlsoCarriesARaise(t *testing.T) {
	usage := saturatingReading(200*1024*1024, testMemoryWarn)
	warnings := runtimeUsageWarnings(usage)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "memory.peak reached") {
		t.Fatalf("expected exactly the peak warning, got %v", warnings)
	}

	recommendation, ok := deploy.RecommendRuntimeSizing(deploy.RuntimeSizingParams{Live: &usage})
	if !ok {
		t.Fatal("the peak warning fired with no recommendation beside it")
	}
	verdict := memoryVerdict(t, recommendation)
	if verdict.Action != deploy.RuntimeSizingRaise {
		t.Errorf("peak at the ceiling warned but the recommendation says %q (%s)", verdict.Action, verdict.Reason)
	}
	if !strings.Contains(verdict.Reason, "100%") {
		t.Errorf("the raise must quote the same peak the warning did, got %q", verdict.Reason)
	}
}

func TestRetainedHistoryAndLiveReadingCannotDisagree(t *testing.T) {
	limit := 6 * testGiB
	history := deploy.RuntimeUsageHistory{
		Samples:                 []config.RuntimeUsage{saturatingReading(1024*1024*1024, limit)},
		ObservedPeakMemoryBytes: 1024 * 1024 * 1024,
	}
	fromHistory, ok := deploy.RecommendRuntimeSizing(deploy.RuntimeSizingParams{History: history})
	if !ok {
		t.Fatal("a retained sample produced no recommendation")
	}
	if got := memoryVerdict(t, fromHistory).Action; got != deploy.RuntimeSizingUnknown {
		t.Fatalf("expected the retained history to say insufficient-evidence, got %q", got)
	}

	live := saturatingReading(limit*99/100, limit)
	fromBoth, ok := deploy.RecommendRuntimeSizing(deploy.RuntimeSizingParams{History: history, Live: &live})
	if !ok {
		t.Fatal("the live reading produced no recommendation")
	}
	verdict := memoryVerdict(t, fromBoth)
	if verdict.Action != deploy.RuntimeSizingRaise {
		t.Fatalf("a reading at 99%% of the limit was scored as %q (%s)", verdict.Action, verdict.Reason)
	}

	raise, ok := deploy.RuntimeMemoryRaiseVerdict(limit, live.Memory.PeakBytes, 0, config.NamespaceResourceQuota{})
	if !ok {
		t.Fatal("the raise helper disagrees with the recommendation it is the only source of")
	}
	if raise.Suggested != verdict.Suggested || raise.Reason != verdict.Reason {
		t.Errorf("two verdicts from one reading: %q (%s) vs %q (%s)",
			verdict.Suggested, verdict.Reason, raise.Suggested, raise.Reason)
	}
}

func TestLiveReadingIsTheSizeScoredAgainst(t *testing.T) {
	oldLimit := 3 * testGiB
	history := deploy.RuntimeUsageHistory{
		Samples:                 []config.RuntimeUsage{saturatingReading(1024*1024*1024, oldLimit)},
		ObservedPeakMemoryBytes: 1024 * 1024 * 1024,
	}
	live := saturatingReading(1024*1024*1024, testMemoryWarn)

	recommendation, ok := deploy.RecommendRuntimeSizing(deploy.RuntimeSizingParams{History: history, Live: &live})
	if !ok {
		t.Fatal("no recommendation")
	}
	if recommendation.Evidence.MemoryLimitBytes != testMemoryWarn {
		t.Errorf("scored against %d, want the live limit %d", recommendation.Evidence.MemoryLimitBytes, testMemoryWarn)
	}
	if got := memoryVerdict(t, recommendation).Current; got != config.FormatBytesAsMi(testMemoryWarn) {
		t.Errorf("current size reported as %q, want %q", got, config.FormatBytesAsMi(testMemoryWarn))
	}
}

func TestNoEvidenceIsStillSilence(t *testing.T) {
	if _, ok := deploy.RecommendRuntimeSizing(deploy.RuntimeSizingParams{}); ok {
		t.Fatal("an environment with no reading and no history produced a recommendation")
	}
}
