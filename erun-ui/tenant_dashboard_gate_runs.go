package main

import (
	"context"

	eruncommon "github.com/sophium/erun/erun-common"
)

// loadTenantDashboardGateRuns is the Gates tab's own panel: what is being
// gated right now, and what recent gates decided — the desktop
// counterpart to `erun gate list`. Degrades independently like every other
// panel here: a caller who cannot read GET /v1/gate-runs gets a named
// restriction, and a read that fails carries its own error, neither of
// which blanks a neighbouring panel.
func loadTenantDashboardGateRuns(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard) {
	panel := uiTenantDashboardPanel{Tab: tenantDashboardTabGates}
	if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadGateRuns); restricted != "" {
		panel.Restricted = restricted
		dashboard.Panels = append(dashboard.Panels, panel)
		return
	}
	runs, err := client.ListGateRuns(ctx, eruncommon.PlatformGateRunFilter{})
	if err != nil {
		panel.Error = tenantDashboardReadError(tenantDashboardReadGateRuns, err)
	} else {
		dashboard.GateRuns = tenantDashboardGateRuns(runs)
	}
	dashboard.Panels = append(dashboard.Panels, panel)
}

func tenantDashboardGateRuns(runs []eruncommon.PlatformGateRun) []uiGateRun {
	result := make([]uiGateRun, 0, len(runs))
	for _, run := range runs {
		result = append(result, uiGateRun{
			GateRunID:    run.GateRunID,
			SourceBranch: run.SourceBranch,
			TargetBranch: run.TargetBranch,
			SourceCommit: run.SourceCommit,
			MergeCommit:  run.MergeCommit,
			ReviewID:     run.ReviewID,
			ReviewName:   run.ReviewName,
			Status:       run.Status,
			FailingStep:  run.FailingStep,
			LogRef:       run.LogRef,
			CreatedAt:    tenantDashboardTime(run.CreatedAt),
			UpdatedAt:    tenantDashboardTime(run.UpdatedAt),
		})
	}
	return result
}
