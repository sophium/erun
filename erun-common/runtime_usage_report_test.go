package eruncommon

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

const (
	testGiB        = int64(1024 * 1024 * 1024)
	testMemoryWarn = 6 * testGiB
)

// memoryReadingForPercent builds a memory reading the way the reader does, from
// the raw counters, rather than assembling one field by field: a fixture that
// states percentOfLimit itself can disagree with the bytes under it, and the
// whole property here lives on the boundary those two meet at. current is the
// smallest whole byte count that reaches percent, so the crossing itself is
// what gets tested.
func memoryReadingForPercent(currentPercent, peakPercent float64, limit int64, oomKills int64) RuntimeUsage {
	bytesFor := func(percent float64) int64 {
		return int64(math.Ceil(float64(limit) * percent / 100))
	}
	values := map[string]string{
		"cgroup_type":    cgroupV2FSType,
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
	usage := RuntimeUsage{Tenant: "erun", Environment: "code3", Memory: runtimeMemoryUsageFromValues(values)}
	usage.Warnings = runtimeUsageWarnings(usage)
	return usage
}

// saturatingReading is the reading this contract exists for: an environment
// sustained against its memory ceiling, peak at the limit, no OOM kill recorded
// yet. It is the case where the warning fired and the recommendation had
// nothing to say.
func saturatingReading(current, limit int64) RuntimeUsage {
	return RuntimeUsage{
		Tenant:      "erun",
		Environment: "code3",
		Memory: RuntimeMemoryUsage{
			CurrentBytes:     current,
			PeakBytes:        limit,
			PeakObserved:     true,
			LimitBytes:       limit,
			PercentOfLimit:   100 * float64(current) / float64(limit),
			OOMKillsObserved: true,
		},
		CPU: RuntimeCPUUsage{QuotaCores: 4, Periods: 20000, ThrottledPeriods: 0},
	}
}

func memoryVerdict(t *testing.T, recommendation RuntimeSizingRecommendation) RuntimeSizingVerdict {
	t.Helper()
	for _, verdict := range recommendation.Verdicts {
		if verdict.Resource == "memory" {
			return verdict
		}
	}
	t.Fatalf("recommendation carries no memory verdict: %+v", recommendation.Verdicts)
	return RuntimeSizingVerdict{}
}

// TestSaturationWarningAlwaysCarriesItsRecommendation is the property this
// change exists to keep: an environment told it is saturated is never left
// without the sizing advice that answers it. Every reading that fires a memory
// warning -- current usage, or a peak that came within a hair of the limit --
// must come back with a raise, from evidence no longer than the single reading
// itself. A host that has never retained history is the case that regressed,
// so the history here is empty on purpose.
func TestSaturationWarningAlwaysCarriesItsRecommendation(t *testing.T) {
	limit := int64(6 * testGiB)

	// The first figure is the threshold itself, crossed by the smallest byte
	// count that crosses it. That is the boundary the alarm and the advisory
	// have to agree on, and the one an integer byte count can fall off.
	for _, percent := range []float64{RuntimeUsageMemoryWarnPercent, 86, 88, 90, 95, 100} {
		usage := memoryReadingForPercent(percent, percent, limit, 0)
		if len(usage.Warnings) == 0 {
			t.Fatalf("%.0f%% of the limit fired no warning; the fixture is wrong, not the code", percent)
		}

		recommendation, ok := RecommendRuntimeSizing(RuntimeSizingParams{Live: &usage})
		if !ok {
			t.Fatalf("%.0f%% of the limit fired a warning but produced no recommendation at all", percent)
		}
		verdict := memoryVerdict(t, recommendation)
		if verdict.Action != RuntimeSizingRaise {
			t.Errorf("%.0f%% of the limit warned but the recommendation says %q (%s); a saturated environment must be told what would fix it",
				percent, verdict.Action, verdict.Reason)
		}
		if strings.TrimSpace(verdict.Suggested) == "" {
			t.Errorf("%.0f%% of the limit warned but the raise suggests no size", percent)
		}
		if verdict.Confidence != RuntimeSizingConfidenceHigh {
			t.Errorf("%.0f%% of the limit: a raise stands on evidence of harm and must be high confidence, got %q",
				percent, verdict.Confidence)
		}
		if strings.TrimSpace(verdict.Reason) == "" {
			t.Errorf("%.0f%% of the limit: raise carries no reason", percent)
		}
	}
}

// TestPeakOnlyWarningAlsoCarriesARaise covers the reading that has already
// fallen back down: current usage is idle, memory.peak is at the ceiling. The
// peak warning fires, so the recommendation has to speak too.
func TestPeakOnlyWarningAlsoCarriesARaise(t *testing.T) {
	usage := saturatingReading(200*1024*1024, testMemoryWarn)
	warnings := runtimeUsageWarnings(usage)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "memory.peak reached") {
		t.Fatalf("expected exactly the peak warning, got %v", warnings)
	}

	recommendation, ok := RecommendRuntimeSizing(RuntimeSizingParams{Live: &usage})
	if !ok {
		t.Fatal("the peak warning fired with no recommendation beside it")
	}
	verdict := memoryVerdict(t, recommendation)
	if verdict.Action != RuntimeSizingRaise {
		t.Errorf("peak at the ceiling warned but the recommendation says %q (%s)", verdict.Action, verdict.Reason)
	}
	if !strings.Contains(verdict.Reason, "100%") {
		t.Errorf("the raise must quote the same peak the warning did, got %q", verdict.Reason)
	}
}

// TestOOMKillWarningCarriesARaise covers the third way the memory alarm fires.
func TestOOMKillWarningCarriesARaise(t *testing.T) {
	usage := saturatingReading(1024*1024*1024, testMemoryWarn)
	usage.Memory.OOMKills = 2
	usage.Memory.PeakBytes = 1024 * 1024 * 1024
	if len(runtimeUsageWarnings(usage)) == 0 {
		t.Fatal("a recorded OOM kill fired no warning")
	}

	recommendation, ok := RecommendRuntimeSizing(RuntimeSizingParams{Live: &usage})
	if !ok {
		t.Fatal("an OOM kill warned with no recommendation beside it")
	}
	verdict := memoryVerdict(t, recommendation)
	if verdict.Action != RuntimeSizingRaise || !strings.Contains(verdict.Reason, "2 oom kill(s)") {
		t.Errorf("expected a raise naming the kills, got %q (%s)", verdict.Action, verdict.Reason)
	}
}

// TestRetainedHistoryAndLiveReadingCannotDisagree pins the "never a second
// engine" half of the property. A recommendation scored from retained history
// plus the same reading handed in as Live must reach one verdict, and a live
// reading that is worse than everything retained must win -- otherwise the
// history-based answer `erun list` shows and the reading-based answer
// `erun usage` shows could name different sizes for one environment.
func TestRetainedHistoryAndLiveReadingCannotDisagree(t *testing.T) {
	limit := int64(6 * testGiB)
	history := RuntimeUsageHistory{
		Samples:                 []RuntimeUsage{saturatingReading(1024*1024*1024, limit)},
		ObservedPeakMemoryBytes: 1024 * 1024 * 1024,
	}
	// The retained history alone says the environment is nowhere near its
	// limit, and has not been watched long enough to shrink.
	fromHistory, ok := RecommendRuntimeSizing(RuntimeSizingParams{History: history})
	if !ok {
		t.Fatal("a retained sample produced no recommendation")
	}
	if got := memoryVerdict(t, fromHistory).Action; got != RuntimeSizingUnknown {
		t.Fatalf("expected the retained history to say insufficient-evidence, got %q", got)
	}

	// The live reading shows the environment pinned at the ceiling. One
	// recommendation, scored over both, must not miss it.
	live := saturatingReading(limit*99/100, limit)
	fromBoth, ok := RecommendRuntimeSizing(RuntimeSizingParams{History: history, Live: &live})
	if !ok {
		t.Fatal("the live reading produced no recommendation")
	}
	verdict := memoryVerdict(t, fromBoth)
	if verdict.Action != RuntimeSizingRaise {
		t.Fatalf("a reading at 99%% of the limit was scored as %q (%s)", verdict.Action, verdict.Reason)
	}

	raise, ok := runtimeMemoryRaiseVerdict(limit, live.Memory.PeakBytes, 0, NamespaceResourceQuota{})
	if !ok {
		t.Fatal("the raise helper disagrees with the recommendation it is the only source of")
	}
	if raise.Suggested != verdict.Suggested || raise.Reason != verdict.Reason {
		t.Errorf("two verdicts from one reading: %q (%s) vs %q (%s)",
			verdict.Suggested, verdict.Reason, raise.Suggested, raise.Reason)
	}
}

// TestLiveReadingIsTheSizeScoredAgainst guards the stale-limit case: history
// retained under an older, smaller runtimepod must not size the environment
// against a limit it no longer runs under.
func TestLiveReadingIsTheSizeScoredAgainst(t *testing.T) {
	oldLimit := int64(3 * testGiB)
	history := RuntimeUsageHistory{
		Samples:                 []RuntimeUsage{saturatingReading(1024*1024*1024, oldLimit)},
		ObservedPeakMemoryBytes: 1024 * 1024 * 1024,
	}
	live := saturatingReading(1024*1024*1024, testMemoryWarn)

	recommendation, ok := RecommendRuntimeSizing(RuntimeSizingParams{History: history, Live: &live})
	if !ok {
		t.Fatal("no recommendation")
	}
	if recommendation.Evidence.MemoryLimitBytes != testMemoryWarn {
		t.Errorf("scored against %d, want the live limit %d", recommendation.Evidence.MemoryLimitBytes, testMemoryWarn)
	}
	if got := memoryVerdict(t, recommendation).Current; got != formatBytesAsMi(testMemoryWarn) {
		t.Errorf("current size reported as %q, want %q", got, formatBytesAsMi(testMemoryWarn))
	}
}

// TestNoEvidenceIsStillSilence keeps the one case that must not gain a
// recommendation: an environment erun has never observed gets silence, not a
// guess. The threshold alignment must not turn "nothing known" into advice.
func TestNoEvidenceIsStillSilence(t *testing.T) {
	if _, ok := RecommendRuntimeSizing(RuntimeSizingParams{}); ok {
		t.Fatal("an environment with no reading and no history produced a recommendation")
	}
}
