package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
		tenantDashboardAPIResponse(w, req, capabilities)
	}))
}

// tenantDashboardAPIFixtures is tenantDashboardAPIResponse's fixture body for
// every path except /v1/whoami (handled separately, since its body carries
// the caller-supplied capabilities), keyed by path rather than a switch — a
// switch here once tripped golangci-lint's cyclomatic-complexity cap the
// moment a gate-run case joined the rest.
var tenantDashboardAPIFixtures = map[string]string{
	"/v1/reviews":                 `[{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"READY"}]`,
	"/v1/reviews/merge-queue":     `[{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"READY"}]`,
	"/v1/reviews/review-1/builds": `[{"buildId":"build-1","tenantId":"tenant-1","reviewId":"review-1","successful":true,"commitId":"abc","version":"1.2.3"}]`,
	"/v1/builds":                  `{"builds":[]}`,
	"/v1/gate-runs":               `[{"gateRunId":"gate-1","tenantId":"tenant-1","sourceBranch":"feature","targetBranch":"main","sourceCommit":"abc","mergeCommit":"def","status":"PASSED","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]`,
	"/v1/audit-events":            `{"events":[{"type":"API","externalUserId":"subject-1","apiMethod":"GET","apiPath":"/v1/audit-events","createdAt":"2026-01-01T00:00:00Z"}]}`,
	"/v1/users":                   `[]`,
	"/v1/contexts":                `[{"contextId":"context-1","tenantId":"tenant-1","name":"prod","provider":"aws","status":"running"}]`,
	"/v1/environments":            `[{"environmentId":"env-1","tenantId":"tenant-1","name":"prod","type":"runtime","status":"running"}]`,
	"/v1/invite-requests":         `[]`,
}

// tenantDashboardAPIResponse is tenantDashboardAPI's fixture body for every
// path its forbidden/default handling did not already short-circuit — split
// out to keep tenantDashboardAPI itself under the module's complexity cap.
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

// testERunPlatformAliasApp builds an App with one signed-in erun-type cloud
// alias whose ERun.APIURL is apiURL, backed by a real (temp-dir) secret store
// so CloudProviderBearerToken runs its real refresh_token grant path — the
// same path erun platform`/`erun cloud login` drives — down to a crafted
// access token carrying issuer/subject claims a test can assert on.
func testERunPlatformAliasApp(t *testing.T, apiURL string) *App {
	t.Helper()
	return testERunPlatformAliasAppWithTenants(t, apiURL, map[string]eruncommon.TenantConfig{"frs": {Name: "frs"}})
}

// testERunPlatformAliasAppWithTenants is testERunPlatformAliasApp with an
// explicit tenants map, for tests exercising a tenant-level config override
// (e.g. TenantConfig.APIURL) that the default single-tenant fixture cannot
// carry.
func testERunPlatformAliasAppWithTenants(t *testing.T, apiURL string, tenants map[string]eruncommon.TenantConfig) *App {
	t.Helper()
	secrets := eruncommon.NewFileCloudSecretStore(t.TempDir())
	refreshRef := "erun/refresh/" + testERunAlias
	if err := secrets.SaveCloudSecret(refreshRef, "refresh-1"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	jwt := testUIJWTWithSubject(testERunIssuer, testERunSubject)
	rootConfig := eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{{
		Alias:    testERunAlias,
		Provider: eruncommon.CloudProviderERun,
		Username: "erun",
		ERun:     &eruncommon.ERunProviderConfig{APIURL: apiURL, ClientID: "test-client", RefreshTokenRef: refreshRef},
	}}}
	return NewApp(erunUIDeps{
		store: stubUIStore{config: &rootConfig, tenants: tenants},
		cloudDeps: eruncommon.CloudDependencies{
			CloudSecretStore: secrets,
			FetchOIDCDiscovery: func(eruncommon.Context, string) (eruncommon.OIDCDiscovery, error) {
				return eruncommon.OIDCDiscovery{TokenEndpoint: "https://auth.erun.example/token"}, nil
			},
			RefreshERunTokens: func(eruncommon.Context, eruncommon.OIDCDiscovery, string, string) (eruncommon.ERunTokens, error) {
				return eruncommon.ERunTokens{AccessToken: jwt, ExpiresIn: time.Hour}, nil
			},
		},
	})
}

const (
	testERunAlias   = "erun+api.test.example@erun"
	testERunIssuer  = "https://auth.erun.example"
	testERunSubject = "user-subject-1"
)

// testAWSAliasApp builds an App whose only configured cloud alias is an AWS
// one — the exact shape the operator's own machine had: a tenant's primary
// cloud alias is AWS-typed, and no erun platform alias is configured at all.
// Used to prove the platform read no longer reaches for it.
func testAWSAliasApp(t *testing.T) *App {
	t.Helper()
	jwt := testUIJWT("https://sts.aws.example")
	rootConfig := eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{{
		Alias: "team-cloud", Provider: eruncommon.CloudProviderAWS, Profile: "team",
	}}}
	return NewApp(erunUIDeps{
		store: stubUIStore{config: &rootConfig, tenants: map[string]eruncommon.TenantConfig{"frs": {Name: "frs"}}},
		cloudDeps: eruncommon.CloudDependencies{
			RunAWSBearerToken: func(eruncommon.Context, string, string) (string, error) { return jwt, nil },
			CheckAWSStatus: func(_ eruncommon.Context, provider eruncommon.CloudProviderConfig) eruncommon.CloudProviderStatus {
				return eruncommon.CloudProviderStatus{CloudProviderConfig: provider, Status: eruncommon.CloudTokenStatusActive}
			},
		},
	})
}

func tenantDashboardApp(t *testing.T, apiURL string) *App {
	t.Helper()
	return testERunPlatformAliasApp(t, apiURL)
}

func loadTenantDashboardFrom(t *testing.T, app *App) uiTenantDashboard {
	t.Helper()
	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{Tenant: "frs"})
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
	if len(dashboard.GateRuns) != 1 {
		t.Fatalf("expected the gate runs panel to still resolve, got %+v", dashboard.GateRuns)
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

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	if got := strings.Join(requests, ","); got != "/v1/whoami,/v1/audit-events,/v1/invite-requests/mine,/v1/config" {
		t.Fatalf("expected only the reads the caller may make (plus the identity-scoped invite-request/config reads, which need no capability), got %q", got)
	}
	queue := panelFor(t, dashboard, tenantDashboardTabQueue)
	if queue.Restricted != tenantDashboardReadMergeQueue {
		t.Fatalf("expected the merge queue panel to name the missing read, got %+v", queue)
	}
	gates := panelFor(t, dashboard, tenantDashboardTabGates)
	if gates.Restricted != tenantDashboardReadGateRuns {
		t.Fatalf("expected the gates panel to name the missing read, got %+v", gates)
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

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	if got := strings.Join(requests, ","); got != "/v1/whoami,/v1/invite-requests/mine,/v1/config" {
		t.Fatalf("expected no read beyond identity (plus the identity-scoped invite-request/config reads, which need no capability), got %q", got)
	}
	for _, tab := range []string{tenantDashboardTabQueue, tenantDashboardTabGates, tenantDashboardTabBuilds, tenantDashboardTabAudit} {
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

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	want := "/v1/whoami,/v1/users,/v1/reviews,/v1/reviews/merge-queue,/v1/gate-runs,/v1/reviews/review-1/builds,/v1/builds,/v1/reviews/review-1/comments,/v1/reviews,/v1/reviews,/v1/audit-events,/v1/contexts,/v1/environments,/v1/invite-requests,/v1/invite-requests/mine,/v1/config"
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

// TestTenantDashboardResolvesReviewAuthorUsernames is the "u2 in the UI" bug
// fixed for #1378: a review's raw authorUserId is enriched with the tenant
// user directory's display name whenever the caller can read /v1/users.
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

// TestTenantDashboardExplainsARefusedIdentityRead is what a caller with no
// permissions at all sees. The identity read is authorized like every other
// route, so it is refused outright — and the dashboard has to say what that
// means rather than repeat the status line.
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
