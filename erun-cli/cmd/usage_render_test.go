package cmd

import (
	"bytes"
	"strings"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

const (
	renderMiB = int64(1024 * 1024)
	renderGiB = 1024 * renderMiB
)

// code3Reading is the reading this renderer exists for: `erun/code3` pinned
// against its 6144Mi limit, peak at the ceiling, no OOM kill recorded yet. The
// warnings fire; before this change nothing followed them.
func code3Reading() common.RuntimeUsage {
	const limit = int64(6144) * renderMiB
	usage := common.RuntimeUsage{
		Tenant:      "erun",
		Environment: "code3",
		Memory: common.RuntimeMemoryUsage{
			CurrentBytes:     limit * 98 / 100,
			PeakBytes:        limit,
			PeakObserved:     true,
			LimitBytes:       limit,
			PercentOfLimit:   98.05,
			OOMKillsObserved: true,
		},
		CPU: common.RuntimeCPUUsage{
			QuotaCores:         4,
			UtilizationPercent: 88.7,
			IntervalSeconds:    1,
			Periods:            20000,
		},
	}
	// Verbatim from the issue's own output. That the reader derives exactly
	// these from this reading is asserted where the derivation lives, in
	// erun-common; this test is about what the CLI does with them.
	usage.Warnings = []string{
		"memory is at 98% of its 6144Mi limit (warns at 85%)",
		"memory.peak reached 100% of the limit (warns at 95%) -- this environment came close to an OOM kill",
	}
	return usage
}

// TestUsageReportRendersTheSizingRecommendationBesideTheWarnings is the
// operator-visible half of the fix: an environment reported as saturated must
// be told, in the same output, the size that would fix it and the evidence
// that size rests on. Both halves are asserted together, because the defect
// was never a missing recommendation in isolation -- it was a warning with
// nothing after it.
func TestUsageReportRendersTheSizingRecommendationBesideTheWarnings(t *testing.T) {
	usage := code3Reading()
	if len(usage.Warnings) == 0 {
		t.Fatal("the fixture fires no warning; it is not the reading this test exists for")
	}

	report := common.RuntimeUsageReport{RuntimeUsage: usage}
	recommendation, ok := common.RecommendRuntimeSizing(common.RuntimeSizingParams{Live: &usage})
	if !ok {
		t.Fatal("a reading at 98% of its limit produced no recommendation")
	}
	report.Sizing = &recommendation

	var out bytes.Buffer
	if err := writeUsageResult(common.Context{Stdout: &out, Stderr: &out}, report); err != nil {
		t.Fatalf("writeUsageResult: %v", err)
	}
	rendered := out.String()
	t.Logf("erun usage:\n%s", strings.TrimRight(rendered, "\n"))

	warningAt := strings.Index(rendered, "Warnings (")
	if warningAt < 0 {
		t.Fatalf("no warning section rendered:\n%s", rendered)
	}
	sizingAt := strings.Index(rendered, "Sizing recommendation:")
	if sizingAt < 0 {
		t.Fatalf("the environment is reported as saturated with no recommendation after the warning:\n%s", rendered)
	}
	if sizingAt < warningAt {
		t.Errorf("the recommendation must follow the warning it answers, got it before:\n%s", rendered)
	}
	for _, want := range []string{"sizing: memory raise", "sizing-evidence:"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered output is missing %q:\n%s", want, rendered)
		}
	}
}

// TestUsageRenderingOmitsSizingWhenThereIsNone keeps the honest-silence case:
// a reading and a history that support no recommendation at all render exactly
// the output they always did, rather than an empty-headed section.
func TestUsageRenderingOmitsSizingWhenThereIsNone(t *testing.T) {
	var out bytes.Buffer
	if err := writeUsageResult(common.Context{Stdout: &out, Stderr: &out}, common.RuntimeUsageReport{
		RuntimeUsage: common.RuntimeUsage{CPU: common.RuntimeCPUUsage{QuotaCores: 4, UtilizationPercent: 1, IntervalSeconds: 1}},
	}); err != nil {
		t.Fatalf("writeUsageResult: %v", err)
	}
	if strings.Contains(out.String(), "Sizing") {
		t.Errorf("a report with no recommendation rendered a sizing section:\n%s", out.String())
	}
}
