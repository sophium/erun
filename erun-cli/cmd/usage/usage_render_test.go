package usage

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sophium/erun/cmd/shared/usageview"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

const (
	renderMiB = int64(1024 * 1024)
	renderGiB = 1024 * renderMiB
)

// code3Reading is a memory-saturated reading with warnings and no OOM kill.
func code3Reading() model.RuntimeUsage {
	const limit = int64(6144) * renderMiB
	usage := model.RuntimeUsage{
		Tenant:      "erun",
		Environment: "code3",
		Memory: model.RuntimeMemoryUsage{
			CurrentBytes:     limit * 98 / 100,
			PeakBytes:        limit,
			PeakObserved:     true,
			LimitBytes:       limit,
			PercentOfLimit:   98.05,
			OOMKillsObserved: true,
		},
		CPU: model.RuntimeCPUUsage{
			QuotaCores:         4,
			UtilizationPercent: 88.7,
			IntervalSeconds:    1,
			Periods:            20000,
		},
	}
	usage.Warnings = []string{
		"memory is at 98% of its 6144Mi limit (warns at 85%)",
		"memory.peak reached 100% of the limit (warns at 95%) -- this environment came close to an OOM kill",
	}
	return usage
}

func TestUsageReportStatesAnUnreadableReservationRatherThanZero(t *testing.T) {
	usage := code3Reading()
	usage.Requests = &model.RuntimeUsageRequests{Unavailable: "kubectl get pods: connection refused"}
	var out bytes.Buffer
	if err := usageview.WriteUsageResult(core.Context{Stdout: &out, Stderr: &out}, model.RuntimeUsageReport{RuntimeUsage: usage}); err != nil {
		t.Fatalf("writeUsageResult: %v", err)
	}
	if !strings.Contains(out.String(), "Requests: unavailable (kubectl get pods: connection refused)") {
		t.Errorf("expected the unreadable reservation to be stated, got:\n%s", out.String())
	}
}
