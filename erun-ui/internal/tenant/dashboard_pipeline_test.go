package tenant

import (
	"strings"
	"testing"
)

func TestTenantDashboardPipelineReportsAFailedReadRatherThanAnEmptyPipeline(t *testing.T) {
	var requests []string
	// Unknown capabilities, so the read is attempted and fails itself.
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
	if len(dashboard.MergeQueue) != 1 || len(dashboard.AuditEvents) != 1 {
		t.Fatalf("expected the other panels to still resolve, got queue=%+v audit=%+v", dashboard.MergeQueue, dashboard.AuditEvents)
	}
}
