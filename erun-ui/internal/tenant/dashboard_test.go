package tenant

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
)

// nil capabilities means the platform reports none.
func tenantDashboardAPI(t *testing.T, capabilities string, forbidden map[string]bool, requests *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		*requests = append(*requests, req.URL.Path)
		if forbidden[req.URL.Path] {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		tenantDashboardAPIResponse(w, req, capabilities)
	}))
}

var tenantDashboardAPIFixtures = map[string]string{
	"/v1/reviews":                 `[{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"READY"}]`,
	"/v1/reviews/merge-queue":     `[{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"READY"}]`,
	"/v1/reviews/review-1/builds": `[{"buildId":"build-1","tenantId":"tenant-1","reviewId":"review-1","successful":true,"commitId":"abc","version":"1.2.3"}]`,
	"/v1/builds":                  `{"builds":[]}`,
	"/v1/pipeline":                `[{"issueKey":"sophium/erun#2683","items":[{"issueKey":"sophium/erun#2683","issueRef":"sophium/erun#2683","issueRefSource":"DECLARED","rung":"PLANNED","job":{"jobId":"job-1","jobType":"triage","issueRef":"sophium/erun#2683","summary":"plan the pipeline view","status":"PLANNED","actorKind":"orchestrator","actorId":"erun/ideas","startedAt":"2026-01-01T00:00:00Z"}}]}]`,
	"/v1/audit-events":            `{"events":[{"type":"API","externalUserId":"subject-1","apiMethod":"GET","apiPath":"/v1/audit-events","createdAt":"2026-01-01T00:00:00Z"}]}`,
	"/v1/users":                   `[]`,
	"/v1/contexts":                `[{"contextId":"context-1","tenantId":"tenant-1","name":"prod","provider":"aws","status":"running"}]`,
	"/v1/environments":            `[{"environmentId":"env-1","tenantId":"tenant-1","name":"prod","type":"runtime","status":"running"}]`,
	"/v1/invite-requests":         `[]`,
	"/v1/roles":                   `[{"roleId":"role-audit","name":"Auditor","permissions":[{"apiMethod":"GET","apiPath":"/v1/audit-events"}]},{"roleId":"role-reviewer","name":"Reviewer","permissions":[{"apiMethodPattern":"^GET$","apiPathPattern":"^/v1/reviews/[^/]+$"}]}]`,
}

func tenantDashboardAPIResponse(w http.ResponseWriter, req *http.Request, capabilities string) {
	if req.URL.Path == "/v1/whoami" {
		_, _ = w.Write([]byte(`{"tenantId":"tenant-1","userId":"user-1","username":"reader","roles":["Auditor"],"issuer":"https://sts.aws.example","subject":"subject-1","capabilities":` + capabilities + `}`))
		return
	}
	body, ok := tenantDashboardAPIFixtures[req.URL.Path]
	if !ok {
		http.NotFound(w, req)
		return
	}
	_, _ = w.Write([]byte(body))
}

// testERunPlatformAliasApp uses a real temp-dir secret store so the real
// refresh_token grant path mints the token.
func testERunPlatformAliasApp(t *testing.T, apiURL string) *Service {
	t.Helper()
	return testERunPlatformAliasAppWithTenants(t, apiURL, map[string]model.TenantConfig{"frs": {Name: "frs"}})
}

func testERunPlatformAliasAppWithTenants(t *testing.T, apiURL string, tenants map[string]model.TenantConfig) *Service {
	t.Helper()
	secrets := secretstore.NewFileCloudSecretStore(t.TempDir())
	refreshRef := "erun/refresh/" + testERunAlias
	if err := secrets.SaveCloudSecret(refreshRef, "refresh-1"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	jwt := testUIJWTWithSubject(testERunIssuer, testERunSubject)
	rootConfig := config.ERunConfig{CloudProviders: []model.CloudProviderConfig{{
		Alias:    testERunAlias,
		Provider: config.CloudProviderERun,
		Username: "erun",
		ERun:     &model.ERunProviderConfig{APIURL: apiURL, ClientID: "test-client", RefreshTokenRef: refreshRef},
	}}}
	return newTestService(t, Deps{
		Store: stubStore{config: &rootConfig, tenants: tenants},
		Cloud: cloud.CloudDependencies{
			CloudSecretStore: secrets,
			FetchOIDCDiscovery: func(core.Context, string) (cloud.OIDCDiscovery, error) {
				return cloud.OIDCDiscovery{TokenEndpoint: "https://auth.erun.example/token"}, nil
			},
			RefreshERunTokens: func(core.Context, cloud.OIDCDiscovery, string, string) (cloud.ERunTokens, error) {
				return cloud.ERunTokens{AccessToken: jwt, ExpiresIn: time.Hour}, nil
			},
		},
	})
}

const (
	testERunAlias   = "erun+api.test.example@erun"
	testERunIssuer  = "https://auth.erun.example"
	testERunSubject = "user-subject-1"
)

func testAWSAliasApp(t *testing.T) *Service {
	t.Helper()
	jwt := testUIJWT("https://sts.aws.example")
	rootConfig := config.ERunConfig{CloudProviders: []model.CloudProviderConfig{{
		Alias: "team-cloud", Provider: config.CloudProviderAWS, Profile: "team",
	}}}
	return newTestService(t, Deps{
		Store: stubStore{config: &rootConfig, tenants: map[string]model.TenantConfig{"frs": {Name: "frs"}}},
		Cloud: cloud.CloudDependencies{
			RunAWSBearerToken: func(core.Context, string, string) (string, error) { return jwt, nil },
			CheckAWSStatus: func(_ core.Context, provider model.CloudProviderConfig) model.CloudProviderStatus {
				return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusActive}
			},
		},
	})
}

func tenantDashboardApp(t *testing.T, apiURL string) *Service {
	t.Helper()
	return testERunPlatformAliasApp(t, apiURL)
}

func loadTenantDashboardFrom(t *testing.T, app *Service) Dashboard {
	t.Helper()
	dashboard, err := app.LoadTenantDashboard(DashboardInput{Tenant: "frs"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	return dashboard
}

func panelFor(t *testing.T, dashboard Dashboard, tab string) DashboardPanel {
	t.Helper()
	for _, panel := range dashboard.Panels {
		if panel.Tab == tab {
			return panel
		}
	}
	t.Fatalf("no %q panel in %+v", tab, dashboard.Panels)
	return DashboardPanel{}
}

func TestTenantDashboardPanelsResolveIndependently(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "null", map[string]bool{"/v1/reviews": true}, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

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

func TestTenantDashboardSkipsReadsTheCallerMayNotMake(t *testing.T) {
	var requests []string
	capabilities := `[{"method":"GET","path":"/v1/whoami"},{"method":"GET","path":"/v1/audit-events"}]`
	server := tenantDashboardAPI(t, capabilities, nil, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	if got := strings.Join(requests, ","); got != "/v1/whoami,/v1/audit-events,/v1/invite-requests/mine,/v1/config" {
		t.Fatalf("expected only the reads the caller may make (plus the identity-scoped invite-request/config reads, which need no capability), got %q", got)
	}
	queue := panelFor(t, dashboard, tenantDashboardTabQueue)
	if queue.Restricted != ReadMergeQueue {
		t.Fatalf("expected the merge queue panel to name the missing read, got %+v", queue)
	}
	builds := panelFor(t, dashboard, tenantDashboardTabBuilds)
	if builds.Restricted != ReadReviews {
		t.Fatalf("expected the builds panel to name the missing read, got %+v", builds)
	}
	pipeline := panelFor(t, dashboard, tenantDashboardTabPipeline)
	if pipeline.Restricted != tenantDashboardReadPipeline {
		t.Fatalf("expected the pipeline panel to name the missing read, got %+v", pipeline)
	}
	if audit := panelFor(t, dashboard, tenantDashboardTabAudit); audit.Restricted != "" {
		t.Fatalf("expected the audit panel to be readable, got %+v", audit)
	}
	if len(dashboard.AuditEvents) != 1 {
		t.Fatalf("expected the readable panel to still load, got %+v", dashboard.AuditEvents)
	}
}

// An empty capability set is an answer: every panel is restricted.
func TestTenantDashboardRestrictsEveryPanelForAPermissionlessCaller(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "[]", nil, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	if got := strings.Join(requests, ","); got != "/v1/whoami,/v1/invite-requests/mine,/v1/config" {
		t.Fatalf("expected no read beyond identity (plus the identity-scoped invite-request/config reads, which need no capability), got %q", got)
	}
	for _, tab := range []string{tenantDashboardTabQueue, tenantDashboardTabPipeline, tenantDashboardTabBuilds, tenantDashboardTabAudit} {
		if panel := panelFor(t, dashboard, tab); panel.Restricted == "" {
			t.Fatalf("expected the %s panel to be reported as restricted, got %+v", tab, panel)
		}
	}
}

func TestTenantDashboardAttemptsEveryReadWhenCapabilitiesAreUnknown(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "null", nil, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	want := "/v1/whoami,/v1/users,/v1/reviews,/v1/reviews/merge-queue,/v1/pipeline,/v1/reviews/review-1/builds,/v1/builds,/v1/reviews/review-1/comments,/v1/reviews,/v1/reviews,/v1/audit-events,/v1/contexts,/v1/environments,/v1/invite-requests,/v1/invite-requests/mine,/v1/config"
	if got := strings.Join(requests, ","); got != want {
		t.Fatalf("expected every read to be attempted, got %q, want %q", got, want)
	}
	for _, panel := range dashboard.Panels {
		if panel.Restricted != "" {
			t.Fatalf("expected no panel to be hidden on an unknown capability set, got %+v", panel)
		}
	}
	if dashboard.MineReviewCount == nil || *dashboard.MineReviewCount != 1 {
		t.Fatalf("expected the Mine filter count to be computed, got %+v", dashboard.MineReviewCount)
	}
	if dashboard.WaitingOnMeReviewCount == nil || *dashboard.WaitingOnMeReviewCount != 1 {
		t.Fatalf("expected the Waiting-on-me filter count to be computed, got %+v", dashboard.WaitingOnMeReviewCount)
	}
}

func TestTenantDashboardResolvesReviewAuthorUsernames(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, req.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/v1/whoami":
			_, _ = w.Write([]byte(`{"tenantId":"tenant-1","userId":"user-1","username":"reader","capabilities":null}`))
		case "/v1/users":
			_, _ = w.Write([]byte(`[{"userId":"user-2","tenantId":"tenant-1","username":"pat"}]`))
		case "/v1/reviews", "/v1/reviews/merge-queue":
			_, _ = w.Write([]byte(`[{"reviewId":"review-1","tenantId":"tenant-1","authorUserId":"user-2","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"READY"}]`))
		case "/v1/reviews/review-1/builds", "/v1/reviews/review-1/comments":
			_, _ = w.Write([]byte(`[]`))
		case "/v1/audit-events":
			_, _ = w.Write([]byte(`{"events":[]}`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	if len(dashboard.Reviews) != 1 || dashboard.Reviews[0].AuthorUsername != "pat" {
		t.Fatalf("expected the review's author to resolve to its username, got %+v", dashboard.Reviews)
	}
}

func TestTenantDashboardExplainsARefusedIdentityRead(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "null", map[string]bool{"/v1/whoami": true}, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

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

func TestTenantDashboardUsersPanelListsTheTenantRoster(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, req.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/v1/whoami":
			_, _ = w.Write([]byte(`{"tenantId":"tenant-1","userId":"user-1","username":"reader","roles":["Auditor"],"capabilities":null}`))
		case "/v1/users":
			_, _ = w.Write([]byte(`[{"userId":"user-1","tenantId":"tenant-1","username":"reader"},{"userId":"user-2","tenantId":"tenant-1","username":"pat"},{"userId":"user-3","tenantId":"tenant-1","username":"sam"}]`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	if panel := panelFor(t, dashboard, tenantDashboardTabUsers); panel.Restricted != "" || panel.Error != "" {
		t.Fatalf("expected a readable users panel, got %+v", panel)
	}
	assertTenantDashboardRoster(t, dashboard)
	if got := strings.Count(strings.Join(requests, ","), "/v1/users"); got != 1 {
		t.Fatalf("expected exactly one roster read for the whole dashboard load, got %d in %q", got, requests)
	}
}

func assertTenantDashboardRoster(t *testing.T, dashboard Dashboard) {
	t.Helper()
	if len(dashboard.Users) != 3 {
		t.Fatalf("expected the tenant's three users, got %d: %+v", len(dashboard.Users), dashboard.Users)
	}
	for i, want := range []string{"reader", "pat", "sam"} {
		if dashboard.Users[i].Username != want {
			t.Fatalf("expected roster row %d to be %q, got %+v", i, want, dashboard.Users[i])
		}
	}
	// The roster has no roles; only the caller's row carries whoami's.
	if len(dashboard.Users[0].Roles) != 1 || dashboard.Users[0].Roles[0] != "Auditor" {
		t.Fatalf("expected the caller's own row to carry whoami's roles, got %+v", dashboard.Users[0])
	}
	if len(dashboard.Users[1].Roles) != 0 {
		t.Fatalf("expected a row the roster read reports no roles for to stay unknown, got %+v", dashboard.Users[1])
	}
}

func TestTenantDashboardNamesAnUnreadableRoster(t *testing.T) {
	var requests []string
	capabilities := `[{"method":"GET","path":"/v1/whoami"},{"method":"GET","path":"/v1/audit-events"}]`
	server := tenantDashboardAPI(t, capabilities, nil, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	if panel := panelFor(t, dashboard, tenantDashboardTabUsers); panel.Restricted != tenantDashboardReadUsers {
		t.Fatalf("expected the users panel to name the missing read %q, got %+v", tenantDashboardReadUsers, panel)
	}
	if len(dashboard.Users) != 0 {
		t.Fatalf("expected no roster to be claimed when the read is not permitted, got %+v", dashboard.Users)
	}
	if strings.Contains(strings.Join(requests, ","), "/v1/users") {
		t.Fatalf("expected the unpermitted roster read not to be attempted, got %q", requests)
	}
}

func TestTenantDashboardReportsAFailedRosterRead(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "null", map[string]bool{"/v1/users": true}, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	panel := panelFor(t, dashboard, tenantDashboardTabUsers)
	if panel.Error == "" || panel.Restricted != "" {
		t.Fatalf("expected the users panel to carry its own read failure, got %+v", panel)
	}
	if len(dashboard.Users) != 0 {
		t.Fatalf("expected no roster to be claimed when the read failed, got %+v", dashboard.Users)
	}
}
