package podruntime

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config/runtimeresources"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeusage"
)

const (
	testGiB        = int64(1024 * 1024 * 1024)
	testMemoryWarn = 6 * testGiB
)

func saturatingReading(current, limit int64) model.RuntimeUsage {
	return model.RuntimeUsage{
		Tenant:      "erun",
		Environment: "code3",
		Memory: model.RuntimeMemoryUsage{
			CurrentBytes:     current,
			PeakBytes:        limit,
			PeakObserved:     true,
			LimitBytes:       limit,
			PercentOfLimit:   100 * float64(current) / float64(limit),
			OOMKillsObserved: true,
		},
		CPU: model.RuntimeCPUUsage{QuotaCores: 4, Periods: 20000, ThrottledPeriods: 0},
	}
}

func memoryVerdict(t *testing.T, recommendation model.RuntimeSizingRecommendation) model.RuntimeSizingVerdict {
	t.Helper()
	for _, verdict := range recommendation.Verdicts {
		if verdict.Resource == "memory" {
			return verdict
		}
	}
	t.Fatalf("recommendation carries no memory verdict: %+v", recommendation.Verdicts)
	return model.RuntimeSizingVerdict{}
}

func TestRetainedHistoryAndLiveReadingCannotDisagree(t *testing.T) {
	limit := 6 * testGiB
	history := runtimeusage.RuntimeUsageHistory{
		Samples:                 []model.RuntimeUsage{saturatingReading(1024*1024*1024, limit)},
		ObservedPeakMemoryBytes: 1024 * 1024 * 1024,
	}
	fromHistory, ok := runtimeusage.RecommendRuntimeSizing(runtimeusage.RuntimeSizingParams{History: history})
	if !ok {
		t.Fatal("a retained sample produced no recommendation")
	}
	if got := memoryVerdict(t, fromHistory).Action; got != model.RuntimeSizingUnknown {
		t.Fatalf("expected the retained history to say insufficient-evidence, got %q", got)
	}

	live := saturatingReading(limit*99/100, limit)
	fromBoth, ok := runtimeusage.RecommendRuntimeSizing(runtimeusage.RuntimeSizingParams{History: history, Live: &live})
	if !ok {
		t.Fatal("the live reading produced no recommendation")
	}
	verdict := memoryVerdict(t, fromBoth)
	if verdict.Action != model.RuntimeSizingRaise {
		t.Fatalf("a reading at 99%% of the limit was scored as %q (%s)", verdict.Action, verdict.Reason)
	}

	raise, ok := runtimeusage.RuntimeMemoryRaiseVerdict(limit, live.Memory.PeakBytes, 0, model.NamespaceResourceQuota{})
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
	history := runtimeusage.RuntimeUsageHistory{
		Samples:                 []model.RuntimeUsage{saturatingReading(1024*1024*1024, oldLimit)},
		ObservedPeakMemoryBytes: 1024 * 1024 * 1024,
	}
	live := saturatingReading(1024*1024*1024, testMemoryWarn)

	recommendation, ok := runtimeusage.RecommendRuntimeSizing(runtimeusage.RuntimeSizingParams{History: history, Live: &live})
	if !ok {
		t.Fatal("no recommendation")
	}
	if recommendation.Evidence.MemoryLimitBytes != testMemoryWarn {
		t.Errorf("scored against %d, want the live limit %d", recommendation.Evidence.MemoryLimitBytes, testMemoryWarn)
	}
	if got := memoryVerdict(t, recommendation).Current; got != runtimeresources.FormatBytesAsMi(testMemoryWarn) {
		t.Errorf("current size reported as %q, want %q", got, runtimeresources.FormatBytesAsMi(testMemoryWarn))
	}
}
