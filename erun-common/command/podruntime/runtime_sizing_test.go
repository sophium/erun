package podruntime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config/runtimeresources"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy/runtimeusage"
)

const (
	testMi = int64(1024 * 1024)
	testGi = 1024 * testMi
)

func usageHistory(window time.Duration, samples int, latest model.RuntimeUsage) runtimeusage.RuntimeUsageHistory {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	history := runtimeusage.RuntimeUsageHistory{FirstObservedAt: start, LastObservedAt: start.Add(window)}
	for i := 0; i < samples; i++ {
		history.Samples = append(history.Samples, latest)
	}
	if samples == 0 {
		history.Samples = append(history.Samples, latest)
	}
	return history
}

func verdictFor(t *testing.T, recommendation model.RuntimeSizingRecommendation, resource string) model.RuntimeSizingVerdict {
	t.Helper()
	for _, verdict := range recommendation.Verdicts {
		if verdict.Resource == resource {
			return verdict
		}
	}
	t.Fatalf("no %s verdict in %+v", resource, recommendation.Verdicts)
	return model.RuntimeSizingVerdict{}
}

type memoryDirectionCase struct {
	name           string
	history        runtimeusage.RuntimeUsageHistory
	ceiling        model.NamespaceResourceQuota
	wantAction     model.RuntimeSizingAction
	wantConfidence model.RuntimeSizingConfidence
	wantSuggested  string
}

func runMemoryDirectionCases(t *testing.T, cases []memoryDirectionCase) {
	t.Helper()
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recommendation, ok := runtimeusage.RecommendRuntimeSizing(runtimeusage.RuntimeSizingParams{History: testCase.history, Ceiling: testCase.ceiling})
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

func TestRecommendRuntimeSizingShrinksOrWithholdsMemory(t *testing.T) {
	quietWindow := 26 * time.Hour
	quietSamples := 200

	runMemoryDirectionCases(t, []memoryDirectionCase{
		{
			name: "a long window with too few samples recommends nothing",
			history: func() runtimeusage.RuntimeUsageHistory {
				h := usageHistory(quietWindow, 4, model.RuntimeUsage{Memory: model.RuntimeMemoryUsage{LimitBytes: 10 * testGi}})
				h.ObservedPeakMemoryBytes = 1 * testGi
				return h
			}(),
			wantAction: model.RuntimeSizingUnknown,
		},
		{
			name: "no limit is reported as unmeasurable, not as zero",
			history: usageHistory(quietWindow, quietSamples, model.RuntimeUsage{
				CPU:    model.RuntimeCPUUsage{QuotaCores: 1},
				Memory: model.RuntimeMemoryUsage{Unavailable: "memory.max was not readable"},
			}),
			wantAction: model.RuntimeSizingUnknown,
		},
		{
			name: "a peak just under the raise margin holds rather than shrinking",
			history: func() runtimeusage.RuntimeUsageHistory {
				h := usageHistory(quietWindow, quietSamples, model.RuntimeUsage{Memory: model.RuntimeMemoryUsage{LimitBytes: 10 * testGi}})
				h.ObservedPeakMemoryBytes = 8 * testGi
				return h
			}(),
			wantAction: model.RuntimeSizingHold,
		},
	})
}

func TestRecommendRuntimeSizingCPUDirections(t *testing.T) {
	quietWindow := 26 * time.Hour
	quietSamples := 200

	cases := []struct {
		name          string
		history       runtimeusage.RuntimeUsageHistory
		wantAction    model.RuntimeSizingAction
		wantSuggested string
	}{
		{
			name: "too few scheduling periods recommends nothing",
			history: func() runtimeusage.RuntimeUsageHistory {
				h := usageHistory(quietWindow, quietSamples, model.RuntimeUsage{CPU: model.RuntimeCPUUsage{QuotaCores: 1}, Memory: model.RuntimeMemoryUsage{LimitBytes: 2 * testGi}})
				h.ObservedPeriods = 500
				h.ObservedPeakCPUMilli = 100
				return h
			}(),
			wantAction: model.RuntimeSizingUnknown,
		},
		{
			name: "no quota is reported as unmeasurable",
			history: usageHistory(quietWindow, quietSamples, model.RuntimeUsage{
				Memory: model.RuntimeMemoryUsage{LimitBytes: 2 * testGi},
				CPU:    model.RuntimeCPUUsage{Unavailable: "cpu.max reports no quota (unlimited or not readable); utilisation needs a quota to measure against"},
			}),
			wantAction: model.RuntimeSizingUnknown,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recommendation, ok := runtimeusage.RecommendRuntimeSizing(runtimeusage.RuntimeSizingParams{History: testCase.history})
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

// The peak is set in the first sample, which is then pushed out of the window.
func TestAppendRuntimeUsageSampleBoundsTheWindow(t *testing.T) {
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	history := AppendRuntimeUsageSample(runtimeusage.RuntimeUsageHistory{}, model.RuntimeUsage{
		Memory: model.RuntimeMemoryUsage{LimitBytes: 8 * testGi, PeakBytes: 7 * testGi},
		CPU:    model.RuntimeCPUUsage{UsageUsec: 900_000_000, Periods: 90000},
	}, base)
	for i := 1; i < runtimeUsageHistorySampleCap*3; i++ {
		history = AppendRuntimeUsageSample(history, model.RuntimeUsage{
			Memory: model.RuntimeMemoryUsage{LimitBytes: 8 * testGi, PeakBytes: 1 * testGi},
			CPU:    model.RuntimeCPUUsage{UsageUsec: int64(i) * 1_000_000, Periods: int64(i) * 30},
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

func TestReadLocalRuntimeUsageTreatsUnlimitedAsUnavailable(t *testing.T) {
	root := t.TempDir()
	writeCgroupFixture(t, root, map[string]string{
		"cpu.max":        "max 100000\n",
		"memory.max":     "max\n",
		"memory.current": "1024\n",
	})
	usage := runtimeresources.ReadLocalRuntimeUsage(root)
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

func writeCgroupFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}
