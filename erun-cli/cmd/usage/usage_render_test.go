package usage

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/podruntime"
)

const (
	renderMiB = int64(1024 * 1024)
	renderGiB = 1024 * renderMiB
)

// code3Reading is the reading this renderer exists for: `erun/code3` pinned
// against its 6144Mi limit, peak at the ceiling, no OOM kill recorded yet. The
// warnings fire; before this change nothing followed them.
func code3Reading() config.RuntimeUsage {
	const limit = int64(6144) * renderMiB
	usage := config.RuntimeUsage{
		Tenant:      "erun",
		Environment: "code3",
		Memory: config.RuntimeMemoryUsage{
			CurrentBytes:     limit * 98 / 100,
			PeakBytes:        limit,
			PeakObserved:     true,
			LimitBytes:       limit,
			PercentOfLimit:   98.05,
			OOMKillsObserved: true,
		},
		CPU: config.RuntimeCPUUsage{
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

// TestUsageRenderingOmitsSizingWhenThereIsNone keeps the honest-silence case:
// a reading and a history that support no recommendation at all render exactly
// the output they always did, rather than an empty-headed section.
func TestUsageRenderingOmitsSizingWhenThereIsNone(t *testing.T) {
	var out bytes.Buffer
	if err := shared.WriteUsageResult(core.Context{Stdout: &out, Stderr: &out}, podruntime.RuntimeUsageReport{
		RuntimeUsage: config.RuntimeUsage{CPU: config.RuntimeCPUUsage{QuotaCores: 4, UtilizationPercent: 1, IntervalSeconds: 1}},
	}); err != nil {
		t.Fatalf("writeUsageResult: %v", err)
	}
	if strings.Contains(out.String(), "Sizing") {
		t.Errorf("a report with no recommendation rendered a sizing section:\n%s", out.String())
	}
}

// TestUsageReportStatesAnUnreadableReservationRatherThanZero covers the case a
// silently-empty reading would get wrong: a pod spec that could not be read
// must say so, because "no reservation figures" and "reserves nothing" are
// different answers and only one of them is true here.
func TestUsageReportStatesAnUnreadableReservationRatherThanZero(t *testing.T) {
	usage := code3Reading()
	usage.Requests = &config.RuntimeUsageRequests{Unavailable: "kubectl get pods: connection refused"}
	var out bytes.Buffer
	if err := shared.WriteUsageResult(core.Context{Stdout: &out, Stderr: &out}, podruntime.RuntimeUsageReport{RuntimeUsage: usage}); err != nil {
		t.Fatalf("writeUsageResult: %v", err)
	}
	if !strings.Contains(out.String(), "Requests: unavailable (kubectl get pods: connection refused)") {
		t.Errorf("expected the unreadable reservation to be stated, got:\n%s", out.String())
	}
}
