package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// tenantDashboardAPI serves the dashboard's reads, refusing every path in
// forbidden and reporting the capability set whoami should answer with.
// capabilities nil means the platform reports none at all.
func tenantDashboardAPI(t *testing.T, capabilities string, forbidden map[string]bool, requests *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		*requests = append(*requests, req.URL.Path)
		if forbidden[req.URL.Path] {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/v1/whoami":
			_, _ = w.Write([]byte(`{"tenantId":"tenant-1","userId":"user-1","username":"reader","roles":["Auditor"],"issuer":"https://sts.aws.example","subject":"subject-1","capabilities":` + capabilities + `}`))
		case "/v1/reviews", "/v1/reviews/merge-queue":
			_, _ = w.Write([]byte(`[{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"READY"}]`))
		case "/v1/reviews/review-1/builds":
			_, _ = w.Write([]byte(`[{"buildId":"build-1","tenantId":"tenant-1","reviewId":"review-1","successful":true,"commitId":"abc","version":"1.2.3"}]`))
		case "/v1/audit-events":
			_, _ = w.Write([]byte(`{"events":[{"type":"API","externalUserId":"subject-1","apiMethod":"GET","apiPath":"/v1/audit-events","createdAt":"2026-01-01T00:00:00Z"}]}`))
		default:
			http.NotFound(w, req)
		}
	}))
}

func tenantDashboardApp(t *testing.T) *App {
	t.Helper()
	jwt := testUIJWT("https://sts.aws.example")
	rootConfig := eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{{
		Alias: "team-cloud", Provider: eruncommon.CloudProviderAWS, Profile: "team",
	}}}
	return NewApp(erunUIDeps{
		store: stubUIStore{config: &rootConfig},
		cloudDeps: eruncommon.CloudDependencies{
			RunAWSBearerToken: func(eruncommon.Context, string, string) (string, error) { return jwt, nil },
			CheckAWSStatus: func(_ eruncommon.Context, provider eruncommon.CloudProviderConfig) eruncommon.CloudProviderStatus {
				return eruncommon.CloudProviderStatus{CloudProviderConfig: provider, Status: eruncommon.CloudTokenStatusActive}
			},
		},
	})
}

func loadTenantDashboardFrom(t *testing.T, app *App, apiURL string) uiTenantDashboard {
	t.Helper()
	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{
		Tenant: "frs", APIURL: apiURL, CloudProviderAlias: "team-cloud",
	})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	return dashboard
}

func panelFor(t *testing.T, dashboard uiTenantDashboard, tab string) uiTenantDashboardPanel {
	t.Helper()
	for _, panel := range dashboard.Panels {
		if panel.Tab == tab {
			return panel
		}
	}
	t.Fatalf("no %q panel in %+v", tab, dashboard.Panels)
	return uiTenantDashboardPanel{}
}

// TestTenantDashboardPanelsResolveIndependently is the operator-visible half of
// the bug: one forbidden read used to abort the whole load, so every panel went
// blank — including the ones the caller may read.
func TestTenantDashboardPanelsResolveIndependently(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "null", map[string]bool{"/v1/reviews": true}, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t), server.URL)

	if dashboard.APIError != "" {
		t.Fatalf("one refused panel must not fail the whole dashboard: %q", dashboard.APIError)
	}
	if len(dashboard.AuditEvents) != 1 {
		t.Fatalf("expected the audit panel the caller may read to still resolve, got %+v", dashboard.AuditEvents)
	}
	if len(dashboard.MergeQueue) != 1 {
		t.Fatalf("expected the merge queue panel to still resolve, got %+v", dashboard.MergeQueue)
	}
	if builds := panelFor(t, dashboard, tenantDashboardTabBuilds); !strings.Contains(builds.Error, "403") {
		t.Fatalf("expected the builds panel to carry its own failure, got %+v", builds)
	}
	if audit := panelFor(t, dashboard, tenantDashboardTabAudit); audit.Error != "" || audit.Restricted != "" {
		t.Fatalf("expected a clean audit panel, got %+v", audit)
	}
}

// TestTenantDashboardSkipsReadsTheCallerMayNotMake is the "never
// enabled-then-403" half: a panel whose capability is absent is reported as
// restricted and its API read is never attempted.
func TestTenantDashboardSkipsReadsTheCallerMayNotMake(t *testing.T) {
	var requests []string
	capabilities := `[{"method":"GET","path":"/v1/whoami"},{"method":"GET","path":"/v1/audit-events"}]`
	server := tenantDashboardAPI(t, capabilities, nil, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t), server.URL)

	if got := strings.Join(requests, ","); got != "/v1/whoami,/v1/audit-events" {
		t.Fatalf("expected only the reads the caller may make, got %q", got)
	}
	queue := panelFor(t, dashboard, tenantDashboardTabQueue)
	if queue.Restricted != tenantDashboardReadMergeQueue {
		t.Fatalf("expected the merge queue panel to name the missing read, got %+v", queue)
	}
	builds := panelFor(t, dashboard, tenantDashboardTabBuilds)
	if builds.Restricted != tenantDashboardReadReviews {
		t.Fatalf("expected the builds panel to name the missing read, got %+v", builds)
	}
	if audit := panelFor(t, dashboard, tenantDashboardTabAudit); audit.Restricted != "" {
		t.Fatalf("expected the audit panel to be readable, got %+v", audit)
	}
	if len(dashboard.AuditEvents) != 1 {
		t.Fatalf("expected the readable panel to still load, got %+v", dashboard.AuditEvents)
	}
}

// TestTenantDashboardRestrictsEveryPanelForAPermissionlessCaller pins the
// difference between an empty capability set and an absent one: empty is an
// answer, and every panel is restricted rather than silently blank.
func TestTenantDashboardRestrictsEveryPanelForAPermissionlessCaller(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "[]", nil, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t), server.URL)

	if got := strings.Join(requests, ","); got != "/v1/whoami" {
		t.Fatalf("expected no read beyond identity, got %q", got)
	}
	for _, tab := range []string{tenantDashboardTabQueue, tenantDashboardTabBuilds, tenantDashboardTabAudit} {
		if panel := panelFor(t, dashboard, tab); panel.Restricted == "" {
			t.Fatalf("expected the %s panel to be reported as restricted, got %+v", tab, panel)
		}
	}
}

// TestTenantDashboardAttemptsEveryReadWhenCapabilitiesAreUnknown keeps an
// unknown capability set from reading as an empty one: a platform that cannot
// report capabilities must not leave every panel hidden.
func TestTenantDashboardAttemptsEveryReadWhenCapabilitiesAreUnknown(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "null", nil, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t), server.URL)

	if got := strings.Join(requests, ","); got != "/v1/whoami,/v1/reviews,/v1/reviews/merge-queue,/v1/reviews/review-1/builds,/v1/audit-events" {
		t.Fatalf("expected every read to be attempted, got %q", got)
	}
	for _, panel := range dashboard.Panels {
		if panel.Restricted != "" {
			t.Fatalf("expected no panel to be hidden on an unknown capability set, got %+v", panel)
		}
	}
}

// TestTenantDashboardExplainsARefusedIdentityRead is what a caller with no
// permissions at all sees. The identity read is authorized like every other
// route, so it is refused outright — and the dashboard has to say what that
// means rather than repeat the status line.
func TestTenantDashboardExplainsARefusedIdentityRead(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "null", map[string]bool{"/v1/whoami": true}, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t), server.URL)

	if !strings.Contains(dashboard.APIError, "do not have access to this tenant's dashboard") {
		t.Fatalf("expected the refusal to be explained in the operator's terms, got %q", dashboard.APIError)
	}
	if strings.Contains(dashboard.APIError, "403") {
		t.Fatalf("expected no raw status line in the operator-facing message, got %q", dashboard.APIError)
	}
	if len(dashboard.Panels) != 0 {
		t.Fatalf("expected no panels to be claimed when identity could not be read, got %+v", dashboard.Panels)
	}
}
