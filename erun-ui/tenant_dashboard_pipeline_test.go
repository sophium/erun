package main

import (
	"strings"
	"testing"
)

// TestTenantDashboardPipelineReportsAFailedReadRatherThanAnEmptyPipeline is
// the pipeline panel's own three-states check: a read that failed must render
// as a failure naming the read, never as a tenant with nothing in the
// pipeline. "Nothing is going on" is a conclusion an operator acts on by
// doing nothing, so the two must not look alike.
func TestTenantDashboardPipelineReportsAFailedReadRatherThanAnEmptyPipeline(t *testing.T) {
	var requests []string
	// An unknown capability set leaves every read attempted, so the refusal
	// here is the read's own failure rather than a panel skipped by
	// capability -- which is the state this test is about.
	server := tenantDashboardAPI(t, "null", map[string]bool{"/v1/pipeline": true}, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	if len(dashboard.Pipeline) != 0 {
		t.Fatalf("expected no pipeline rows behind a failed read, got %+v", dashboard.Pipeline)
	}
	panel := panelFor(t, dashboard, tenantDashboardTabPipeline)
	if panel.Restricted != "" {
		t.Fatalf("expected the panel to report a failure, not a restriction, got %+v", panel)
	}
	if !strings.Contains(panel.Error, tenantDashboardReadPipeline) {
		t.Fatalf("expected the failure to name the read that failed, got %q", panel.Error)
	}
	// The neighbouring panels are untouched: one failed read never blanks the
	// ones that worked.
	if len(dashboard.GateRuns) != 1 || len(dashboard.AuditEvents) != 1 {
		t.Fatalf("expected the other panels to still resolve, got gates=%+v audit=%+v", dashboard.GateRuns, dashboard.AuditEvents)
	}
}
