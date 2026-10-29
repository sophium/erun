package tenant

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// The bearer and username hint come from the erun-type alias, never the primary alias.
func TestLoadTenantDashboardResolvesThePlatformThroughTheERunAlias(t *testing.T) {
	var requests []string
	server := httptest.NewServer(erunPlatformDashboardHandler(t, &requests, "frs"))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	dashboard, err := app.LoadTenantDashboard(DashboardInput{Tenant: "frs"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	assertERunPlatformDashboard(t, dashboard, requests)
}

// A local tenant reaches a platform tenant only through its alias, so the two
// names can differ; the fixture's "frs" vs "erun" is the disagreement.
func TestLoadTenantDashboardNamesThePlatformTenantItsRowsBelongTo(t *testing.T) {
	var requests []string
	server := httptest.NewServer(erunPlatformDashboardHandler(t, &requests, "erun"))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	dashboard, err := app.LoadTenantDashboard(DashboardInput{Tenant: "frs"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	if dashboard.User == nil {
		t.Fatal("expected the dashboard to carry the caller's own identity")
	}
	if dashboard.User.TenantName != "erun" {
		t.Fatalf("expected the dashboard to name the platform tenant its rows belong to, got %q", dashboard.User.TenantName)
	}
}

// Nothing in config links the local and platform tenant namespaces, so whoami
// must be the last request the platform sees when the names differ.
func TestLoadTenantDashboardRefusesAPlatformTenantThatIsNotTheLocalOne(t *testing.T) {
	var requests []string
	server := httptest.NewServer(erunPlatformDashboardHandler(t, &requests, "erun"))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	dashboard, err := app.LoadTenantDashboard(DashboardInput{Tenant: "frs"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	if dashboard.PlatformState != tenantPlatformStateTenantMismatch {
		t.Fatalf("expected the dashboard to refuse a platform tenant that is not the local one, got platformState %q", dashboard.PlatformState)
	}
	user := dashboard.User
	if user == nil {
		t.Fatal("expected the dashboard to carry the caller's own identity")
	}
	if user.TenantName != "erun" {
		t.Fatalf("expected the caller row to name the platform tenant, got %q", user.TenantName)
	}
	if !reflect.DeepEqual(requests, []string{"/v1/whoami"}) {
		t.Fatalf("expected the identity read to be the last read behind a mismatched platform tenant, got %v", requests)
	}
	assertTenantDashboardRowsAreEmpty(t, &dashboard)
}

func assertTenantDashboardRowsAreEmpty(t *testing.T, dashboard *Dashboard) {
	t.Helper()
	for _, rows := range []struct {
		name string
		len  int
	}{
		{"reviews", len(dashboard.Reviews)},
		{"merge queue", len(dashboard.MergeQueue)},
		{"users", len(dashboard.Users)},
		{"audit events", len(dashboard.AuditEvents)},
		{"panels", len(dashboard.Panels)},
	} {
		if rows.len != 0 {
			t.Fatalf("expected no %s behind a mismatched platform tenant, got %d", rows.name, rows.len)
		}
	}
}

// With no name on either side there is nothing to compare, so nothing is refused.
func TestTenantDashboardTenantMismatchOnlyNamesATenantThatDoesNotCorrespond(t *testing.T) {
	for _, tc := range []struct {
		name               string
		localTenant        string
		platformTenantName string
		want               string
	}{
		{name: "a nameless platform leaves nothing to compare", localTenant: "frs", platformTenantName: "", want: ""},
		{name: "an unnamed local tenant is never refused", localTenant: "", platformTenantName: "erun", want: ""},
		{name: "the same name corresponds", localTenant: "frs", platformTenantName: "frs", want: ""},
		{name: "the same name in another case still corresponds", localTenant: "frs", platformTenantName: "FRS", want: ""},
		{name: "surrounding whitespace does not make a mismatch", localTenant: " frs ", platformTenantName: "frs", want: ""},
		{name: "a different tenant is named", localTenant: "frs", platformTenantName: "erun", want: "erun"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tenantDashboardTenantMismatch(tc.localTenant, tc.platformTenantName); got != tc.want {
				t.Fatalf("tenantDashboardTenantMismatch(%q, %q) = %q, want %q", tc.localTenant, tc.platformTenantName, got, tc.want)
			}
		})
	}
}

// erunPlatformDashboardFixtures is keyed by path; a switch tripped the cyclomatic-complexity lint.
func erunPlatformDashboardFixtures(platformTenantName string) map[string]string {
	return map[string]string{
		"/v1/whoami":                  `{"tenantId":"tenant-1","tenantName":"` + platformTenantName + `","userId":"user-1","username":"Rihards.Freimanis","roles":["ReadAll","WriteAll"],"issuer":"` + testERunIssuer + `","subject":"` + testERunSubject + `"}`,
		"/v1/reviews":                 `[{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"READY"}]`,
		"/v1/reviews/merge-queue":     `[{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"READY"}]`,
		"/v1/reviews/review-1/builds": `[{"buildId":"build-1","tenantId":"tenant-1","reviewId":"review-1","successful":true,"commitId":"abc","version":"1.2.3"}]`,
		"/v1/builds":                  `{"builds":[]}`,
		// One group: a planned job with no branch, and a review linked via its branch name.
		"/v1/pipeline": `[{"issueKey":"sophium/erun#2683","items":[` +
			`{"issueKey":"sophium/erun#2683","issueRef":"sophium/erun#2683","issueRefSource":"DECLARED","rung":"PLANNED",` +
			`"job":{"jobId":"job-1","jobType":"triage","issueRef":"sophium/erun#2683","summary":"plan the pipeline view","status":"PLANNED","actorKind":"orchestrator","actorId":"erun/ideas","startedAt":"2026-01-01T00:00:00Z"}},` +
			`{"issueKey":"sophium/erun#2683","issueRef":"2683","issueRefSource":"INFERRED","rung":"REVIEW_OPEN",` +
			`"review":{"reviewId":"review-1","repository":"github.com/sophium/erun","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"OPEN"}}` +
			`]}]`,
		"/v1/audit-events": `{"events":[{"auditEventId":"event-1","tenantId":"tenant-1","erunUserId":"user-1","externalUserId":"subject-1","externalIssuerId":"` + testERunIssuer + `","type":"API","apiMethod":"GET","apiPath":"/v1/reviews","createdAt":"2026-01-01T00:00:00Z"}]}`,
		"/v1/users":        `[]`,
	}
}

func erunPlatformDashboardHandler(t *testing.T, requests *[]string, platformTenantName string) http.HandlerFunc {
	t.Helper()
	jwt := testUIJWTWithSubject(testERunIssuer, testERunSubject)
	fixtures := erunPlatformDashboardFixtures(platformTenantName)

	return func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer "+jwt {
			t.Fatalf("unexpected authorization header: %q", req.Header.Get("Authorization"))
		}
		if req.Header.Get("X-ERun-Username") != "erun" {
			t.Fatalf("unexpected username hint: %q", req.Header.Get("X-ERun-Username"))
		}
		*requests = append(*requests, req.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		body, ok := fixtures[req.URL.Path]
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write([]byte(body))
	}
}

func assertERunPlatformDashboard(t *testing.T, dashboard Dashboard, requests []string) {
	t.Helper()

	assertERunPlatformDashboardUser(t, dashboard)
	if len(dashboard.MergeQueue) != 1 || len(dashboard.Builds) != 1 || dashboard.Builds[0].ReviewName != "Review 1" {
		t.Fatalf("unexpected dashboard: %+v", dashboard)
	}
	if dashboard.PlatformAlias != testERunAlias {
		t.Fatalf("expected the resolved platform alias to be reported, got %q", dashboard.PlatformAlias)
	}
	assertERunPlatformDashboardAuditEvents(t, dashboard.AuditEvents)
	assertERunPlatformDashboardPipeline(t, dashboard.Pipeline)
	want := "/v1/whoami,/v1/users,/v1/reviews,/v1/reviews/merge-queue,/v1/pipeline,/v1/reviews/review-1/builds,/v1/builds,/v1/reviews/review-1/comments,/v1/reviews,/v1/reviews,/v1/audit-events,/v1/contexts,/v1/environments,/v1/invite-requests,/v1/invite-requests/mine,/v1/config"
	if strings.Join(requests, ",") != want {
		t.Fatalf("unexpected API requests: %+v, want %q", requests, want)
	}
}

// The platform tenant name is asserted even when it matches; the header always renders it.
func assertERunPlatformDashboardUser(t *testing.T, dashboard Dashboard) {
	t.Helper()
	if dashboard.User == nil || dashboard.User.Username != "Rihards.Freimanis" || len(dashboard.User.Roles) != 2 {
		t.Fatalf("unexpected dashboard user: %+v", dashboard.User)
	}
	if dashboard.User.TenantName != "frs" {
		t.Fatalf("expected the dashboard to name the platform tenant, got %q", dashboard.User.TenantName)
	}
}

func assertERunPlatformDashboardPipeline(t *testing.T, issues []PipelineIssue) {
	t.Helper()
	if len(issues) != 1 || issues[0].IssueKey != "sophium/erun#2683" || len(issues[0].Items) != 2 {
		t.Fatalf("unexpected pipeline: %+v", issues)
	}
	assertERunPlatformDashboardPlannedJob(t, issues[0].Items[0])
	assertERunPlatformDashboardOpenReview(t, issues[0].Items[1])
}

func assertERunPlatformDashboardPlannedJob(t *testing.T, planned PipelineItem) {
	t.Helper()
	if planned.Job == nil || planned.Review != nil {
		t.Fatalf("expected the planned item to be a job and only a job, got %+v", planned)
	}
	if planned.Rung != "PLANNED" || planned.Job.JobType != "triage" || planned.IssueRefSource != "DECLARED" {
		t.Fatalf("unexpected planned item: %+v", planned)
	}
	if planned.Job.EndedAt != "" {
		t.Fatalf("a planned job must carry no endedAt, got %q", planned.Job.EndedAt)
	}
}

func assertERunPlatformDashboardOpenReview(t *testing.T, open PipelineItem) {
	t.Helper()
	if open.Review == nil || open.Job != nil {
		t.Fatalf("expected the review item to be a review and only a review, got %+v", open)
	}
	if open.IssueRefSource != "INFERRED" {
		t.Fatalf("expected the branch-derived link to arrive as INFERRED, got %q", open.IssueRefSource)
	}
}

func assertERunPlatformDashboardAuditEvents(t *testing.T, events []DashboardAudit) {
	t.Helper()
	if len(events) != 1 || events[0].Actor != "subject-1" || events[0].Action != "GET /v1/reviews" {
		t.Fatalf("unexpected audit events: %+v", events)
	}
}

func TestLoadTenantDashboardReturnsAPILogWhenIdentityIsNotEnrolled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		notEnrolledDashboardAPIResponse(t, w, req.URL.Path)
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	app.d.LoadAPILog = func(_ context.Context, input DashboardInput) (string, error) {
		if input.MCPURL != "http://127.0.0.1:17000/mcp" || input.KubernetesContext != "" {
			t.Fatalf("unexpected log input: %+v", input)
		}
		return "auth rejected token", nil
	}

	dashboard, err := app.LoadTenantDashboard(DashboardInput{
		Tenant: "frs",
		MCPURL: "http://127.0.0.1:17000/mcp",
	})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	if dashboard.PlatformState != tenantPlatformStateNotEnrolled {
		t.Fatalf("expected the not-enrolled state, got %q (message %q)", dashboard.PlatformState, dashboard.APIError)
	}
	if !strings.Contains(dashboard.APIError, "not enrolled") {
		t.Fatalf("unexpected dashboard message: %q", dashboard.APIError)
	}
	if dashboard.APILog != "auth rejected token" {
		t.Fatalf("unexpected dashboard: %+v", dashboard)
	}
	if dashboard.MyInviteRequest != nil {
		t.Fatalf("expected no invite request yet, got %+v", dashboard.MyInviteRequest)
	}
	if dashboard.InviteRequestRateLimitWindowSeconds != 60 {
		t.Fatalf("expected the platform's current submission window to be reported, got %d", dashboard.InviteRequestRateLimitWindowSeconds)
	}
}

// The underlying error stays verbatim; operators need the original transport text.
func TestLoadTenantDashboardAPILogErrorNamesTheReadThatFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		notEnrolledDashboardAPIResponse(t, w, req.URL.Path)
	}))
	defer server.Close()

	const rawError = "exit status 1: kubectl stub: no cluster in the Playwright harness"
	app := testERunPlatformAliasApp(t, server.URL)
	app.d.LoadAPILog = func(_ context.Context, _ DashboardInput) (string, error) {
		return "", errors.New(rawError)
	}

	dashboard, err := app.LoadTenantDashboard(DashboardInput{
		Tenant: "frs",
		MCPURL: "http://127.0.0.1:17000/mcp",
	})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	if !strings.Contains(dashboard.APILogError, "Could not read this environment's API log") {
		t.Fatalf("API log error does not name the read that failed: %q", dashboard.APILogError)
	}
	if !strings.Contains(dashboard.APILogError, rawError) {
		t.Fatalf("API log error dropped the underlying error text: %q", dashboard.APILogError)
	}
	if dashboard.APILog != "" {
		t.Fatalf("expected no API log alongside the error, got %q", dashboard.APILog)
	}
}

// notEnrolledDashboardAPIResponse 401s whoami while invite-request/config reads answer normally.
func notEnrolledDashboardAPIResponse(t *testing.T, w http.ResponseWriter, path string) {
	t.Helper()
	switch path {
	case "/v1/whoami":
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case "/v1/invite-requests/mine":
		http.Error(w, "no invite request found", http.StatusNotFound)
	case "/v1/config":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tenant":{},"inviteRequestRateLimitWindowSeconds":60}`))
	default:
		t.Fatalf("unexpected request path: %s", path)
	}
}

// TENANT_UNRESOLVED and RESOLUTION_FAILED 401s must not be reported as not enrolled.
func TestLoadTenantDashboardDoesNotMisreportTenantUnresolvedOrResolutionFailedAsNotEnrolled(t *testing.T) {
	for _, code := range []string{"TENANT_UNRESOLVED", "RESOLUTION_FAILED"} {
		t.Run(code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/v1/whoami":
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"code":"` + code + `","message":"platform-provided detail"}`))
				case "/v1/invite-requests/mine":
					http.Error(w, "no invite request found", http.StatusNotFound)
				case "/v1/config":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"tenant":{},"inviteRequestRateLimitWindowSeconds":60}`))
				default:
					t.Fatalf("unexpected request path: %s", req.URL.Path)
				}
			}))
			defer server.Close()

			app := testERunPlatformAliasApp(t, server.URL)
			dashboard, err := app.LoadTenantDashboard(DashboardInput{Tenant: "frs"})
			if err != nil {
				t.Fatalf("LoadTenantDashboard failed: %v", err)
			}
			if dashboard.PlatformState == tenantPlatformStateNotEnrolled {
				t.Fatalf("%s must not be reported as not-enrolled, got state %q message %q", code, dashboard.PlatformState, dashboard.APIError)
			}
			if dashboard.PlatformState != "" {
				t.Fatalf("expected the generic whole-dashboard error state, got %q", dashboard.PlatformState)
			}
			if !strings.Contains(dashboard.APIError, "platform-provided detail") {
				t.Fatalf("expected the platform's own detail in the message, got %q", dashboard.APIError)
			}
		})
	}
}

// A failed invite-request read must not look like "never submitted one".
func TestLoadTenantDashboardReportsMyInviteRequestErrorRatherThanNilOnFault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v1/whoami":
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case "/v1/invite-requests/mine":
			http.Error(w, "internal error", http.StatusInternalServerError)
		case "/v1/config":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tenant":{},"inviteRequestRateLimitWindowSeconds":60}`))
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
		}
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	dashboard, err := app.LoadTenantDashboard(DashboardInput{Tenant: "frs"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	if dashboard.MyInviteRequest != nil {
		t.Fatalf("expected no invite request on a transport fault, got %+v", dashboard.MyInviteRequest)
	}
	if dashboard.MyInviteRequestError == "" {
		t.Fatal("expected MyInviteRequestError to be set rather than silently nil on a transport fault")
	}
}

// With only an AWS alias, the dashboard must report not-connected rather than sign
// the platform read with the AWS identity.
func TestLoadTenantDashboardNeverUsesAnAWSAliasForThePlatform(t *testing.T) {
	app := testAWSAliasApp(t)
	dashboard, err := app.LoadTenantDashboard(DashboardInput{Tenant: "frs"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	if dashboard.PlatformState != tenantPlatformStateNotConnected {
		t.Fatalf("expected not-connected with only an AWS alias configured, got %q (apiError=%q)", dashboard.PlatformState, dashboard.APIError)
	}
	if dashboard.APIURL != "" || dashboard.PlatformAlias != "" {
		t.Fatalf("expected no platform URL or alias to be reported, got %+v", dashboard)
	}
}
