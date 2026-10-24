package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestLoadTenantDashboardResolvesThePlatformThroughTheERunAlias is the "green"
// half of the erun-vs-AWS seam: the dashboard's bearer and username
// hint come from the resolved erun-type platform alias, never from whichever
// alias happens to be the tenant's primary one. See
// TestLoadTenantDashboardNeverUsesAnAWSAliasForThePlatform for the "red"
// half — proving an AWS-only configuration cannot reach this path at all.
func TestLoadTenantDashboardResolvesThePlatformThroughTheERunAlias(t *testing.T) {
	var requests []string
	server := httptest.NewServer(erunPlatformDashboardHandler(t, &requests, "frs"))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{Tenant: "frs"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	assertERunPlatformDashboard(t, dashboard, requests)
}

// TestLoadTenantDashboardNamesThePlatformTenantItsRowsBelongTo: a local tenant
// is bound to a platform tenant only by whichever alias its credential reaches,
// so the rows every panel renders can belong to a tenant that is not the one
// the dashboard was opened from — an `frs` dashboard listing `erun`'s reviews
// and queue. The surface must name the platform tenant whoami reports.
//
// The fixture's local tenant is "frs" (testERunPlatformAliasApp) and the
// platform answers "erun", which is the disagreement, not the agreement:
// assertERunPlatformDashboard covers the case where the two names match.
func TestLoadTenantDashboardNamesThePlatformTenantItsRowsBelongTo(t *testing.T) {
	var requests []string
	server := httptest.NewServer(erunPlatformDashboardHandler(t, &requests, "erun"))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{Tenant: "frs"})
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

// TestLoadTenantDashboardRefusesAPlatformTenantThatIsNotTheLocalOne: a local
// tenant must not present another platform tenant's rows as its own. Attaching an erun-type alias to a local tenant's
// cloudprovideraliases gives that tenant's dashboard a route to the platform,
// resolved through the alias — so a tenant named `frs` renders `erun`'s
// reviews, queue, users and audit under its own name, behind the write
// controls (create a review, advance a merge queue, approve an invitation)
// that act on them. Nothing in either config links the two namespaces; the
// platform's own name for the tenant behind the bearer is the only thing that
// can say whether the rows are this local tenant's.
//
// The fixture's local tenant is "frs" (testERunPlatformAliasApp) and the
// platform answers "erun", so the two do not correspond. This drives the real
// path — the alias resolution, the real refresh-token bearer mint, and a real
// HTTP platform — and pins that the dashboard stops at the identity read:
// whoami is the last request the platform sees, so no panel ever renders the
// other tenant's rows.
// TestLoadTenantDashboardResolvesThePlatformThroughTheERunAlias is the
// agreeing control: the same path with the platform answering "frs" loads
// every panel exactly as it did before.
func TestLoadTenantDashboardRefusesAPlatformTenantThatIsNotTheLocalOne(t *testing.T) {
	var requests []string
	server := httptest.NewServer(erunPlatformDashboardHandler(t, &requests, "erun"))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{Tenant: "frs"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	if dashboard.PlatformState != tenantPlatformStateTenantMismatch {
		t.Fatalf("expected the dashboard to refuse a platform tenant that is not the local one, got platformState %q", dashboard.PlatformState)
	}
	// The platform's own name is still carried, so the header can say whose
	// rows these are rather than only that they are not this tenant's.
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

// assertTenantDashboardRowsAreEmpty holds the mismatched-platform dashboard to
// "none of the other tenant's rows", panel by panel: each is a different read
// against the same wrong tenant, so one of them left populated is the whole
// defect back again.
func assertTenantDashboardRowsAreEmpty(t *testing.T, dashboard *uiTenantDashboard) {
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

// TestTenantDashboardTenantMismatchOnlyNamesATenantThatDoesNotCorrespond pins
// the two halves of the correspondence check that are not "the names differ":
// a platform that reports no name leaves nothing to compare, so its dashboard
// is read without the check, and an unnamed local
// tenant is never refused on a comparison that was never made.
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

// erunPlatformDashboardFixtures is erunPlatformDashboardHandler's fixture
// body for every path, keyed by path rather than a switch — a switch here
// once tripped golangci-lint's cyclomatic-complexity cap.
// platformTenantName is the name the fixture's whoami reports for the tenant
// behind the bearer. It is the caller's choice rather than a constant because
// the local tenant a dashboard is opened from and the platform tenant its
// credential reaches are separate namespaces; a caller stages the state it
// means to exercise by naming whether they agree.
func erunPlatformDashboardFixtures(platformTenantName string) map[string]string {
	return map[string]string{
		"/v1/whoami":                  `{"tenantId":"tenant-1","tenantName":"` + platformTenantName + `","userId":"user-1","username":"Rihards.Freimanis","roles":["ReadAll","WriteAll"],"issuer":"` + testERunIssuer + `","subject":"` + testERunSubject + `"}`,
		"/v1/reviews":                 `[{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"READY"}]`,
		"/v1/reviews/merge-queue":     `[{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"READY"}]`,
		"/v1/reviews/review-1/builds": `[{"buildId":"build-1","tenantId":"tenant-1","reviewId":"review-1","successful":true,"commitId":"abc","version":"1.2.3"}]`,
		"/v1/builds":                  `{"builds":[]}`,
		// The pipeline fixture carries both halves of the union in one view:
		// a planned job with no branch and no review behind it, and a review
		// whose only issue link was parsed out of its branch name.
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

func assertERunPlatformDashboard(t *testing.T, dashboard uiTenantDashboard, requests []string) {
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

// assertERunPlatformDashboardUser pins the caller's own identity row, which is
// the one row whoami answers for. Its platform tenant name is asserted even
// where it agrees with the local tenant's: the header line is rendered
// whenever the platform reported one, so its absence is never the signal that
// the two names matched.
func assertERunPlatformDashboardUser(t *testing.T, dashboard uiTenantDashboard) {
	t.Helper()
	if dashboard.User == nil || dashboard.User.Username != "Rihards.Freimanis" || len(dashboard.User.Roles) != 2 {
		t.Fatalf("unexpected dashboard user: %+v", dashboard.User)
	}
	if dashboard.User.TenantName != "frs" {
		t.Fatalf("expected the dashboard to name the platform tenant, got %q", dashboard.User.TenantName)
	}
}

// assertERunPlatformDashboardPipeline pins what the Pipeline tab is for: the
// planned job and the review arrive in one group keyed on the same issue, the
// job is a job (a record with no branch) and the review is a review, and the
// provenance of every link survives the read.
func assertERunPlatformDashboardPipeline(t *testing.T, issues []uiPipelineIssue) {
	t.Helper()
	if len(issues) != 1 || issues[0].IssueKey != "sophium/erun#2683" || len(issues[0].Items) != 2 {
		t.Fatalf("unexpected pipeline: %+v", issues)
	}
	assertERunPlatformDashboardPlannedJob(t, issues[0].Items[0])
	assertERunPlatformDashboardOpenReview(t, issues[0].Items[1])
}

func assertERunPlatformDashboardPlannedJob(t *testing.T, planned uiPipelineItem) {
	t.Helper()
	if planned.Job == nil || planned.Review != nil {
		t.Fatalf("expected the planned item to be a job and only a job, got %+v", planned)
	}
	if planned.Rung != "PLANNED" || planned.Job.JobType != "triage" || planned.IssueRefSource != "DECLARED" {
		t.Fatalf("unexpected planned item: %+v", planned)
	}
	// A planned job is a backlog entry: it carries no endedAt, and a reader
	// must not see one invented for it.
	if planned.Job.EndedAt != "" {
		t.Fatalf("a planned job must carry no endedAt, got %q", planned.Job.EndedAt)
	}
}

func assertERunPlatformDashboardOpenReview(t *testing.T, open uiPipelineItem) {
	t.Helper()
	if open.Review == nil || open.Job != nil {
		t.Fatalf("expected the review item to be a review and only a review, got %+v", open)
	}
	if open.IssueRefSource != "INFERRED" {
		t.Fatalf("expected the branch-derived link to arrive as INFERRED, got %q", open.IssueRefSource)
	}
}

func assertERunPlatformDashboardAuditEvents(t *testing.T, events []uiTenantDashboardAudit) {
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
	app.deps.loadAPILog = func(_ context.Context, input uiTenantDashboardInput) (string, error) {
		if input.MCPURL != "http://127.0.0.1:17000/mcp" || input.KubernetesContext != "" {
			t.Fatalf("unexpected log input: %+v", input)
		}
		return "auth rejected token", nil
	}

	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{
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

// The API log is one tab of nine, so a failed read must name the read it could
// not make before the raw error — the same shape the Runtime panels and
// Manage's jobs panel use. The underlying error stays verbatim: operators need
// the original transport text, not a paraphrase of it.
func TestLoadTenantDashboardAPILogErrorNamesTheReadThatFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		notEnrolledDashboardAPIResponse(t, w, req.URL.Path)
	}))
	defer server.Close()

	const rawError = "exit status 1: kubectl stub: no cluster in the Playwright harness"
	app := testERunPlatformAliasApp(t, server.URL)
	app.deps.loadAPILog = func(_ context.Context, _ uiTenantDashboardInput) (string, error) {
		return "", errors.New(rawError)
	}

	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{
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

// notEnrolledDashboardAPIResponse serves TestLoadTenantDashboardReturnsAPILogWhenIdentityIsNotEnrolled's
// fixture: whoami always 401s, and the identity-scoped invite-request/config
// reads it still makes with the same bearer both answer normally.
func notEnrolledDashboardAPIResponse(t *testing.T, w http.ResponseWriter, path string) {
	t.Helper()
	switch path {
	case "/v1/whoami":
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case "/v1/invite-requests/mine":
		// Not-enrolled is exactly the identity "request an invitation"
		// serves — the dashboard still checks this caller's own request
		// status using the same bearer, even though whoami itself 401s.
		http.Error(w, "no invite request found", http.StatusNotFound)
	case "/v1/config":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tenant":{},"inviteRequestRateLimitWindowSeconds":60}`))
	default:
		t.Fatalf("unexpected request path: %s", path)
	}
}

// TestLoadTenantDashboardDoesNotMisreportTenantUnresolvedOrResolutionFailedAsNotEnrolled
// a 401 whoami response can mean three different things per its
// {code, message} envelope (erun-backend-api's auth.go), and "not enrolled"
// is false advice for two of them. TENANT_UNRESOLVED and RESOLUTION_FAILED must
// fall through to the generic whole-dashboard error instead of asserting an
// enrollment answer that is not true.
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
			dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{Tenant: "frs"})
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

// TestLoadTenantDashboardReportsMyInviteRequestErrorRatherThanNilOnFault
// guards against the failure a round trip other than "not found" must not
// silently collapse into "never submitted one": a caller with an already
// pending or approved request would be shown "Request an invitation" again.
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
	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{Tenant: "frs"})
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

// TestLoadTenantDashboardNeverUsesAnAWSAliasForThePlatform is the "red" half
// of the erun-vs-AWS seam: on a machine configured exactly like the
// operator's own (only an AWS cloud alias, no erun platform alias at all),
// the dashboard must report not-connected rather than signing the platform
// read with the AWS identity. Before this fix, LoadTenantDashboard took its
// apiUrl/cloudProviderAlias straight from the frontend, which happily handed
// it the tenant's AWS-typed primary alias and an environment's own loopback
// port-forward — this test fails against that shape because there is no
// APIURL/CloudProviderAlias field left to even construct it with, and it
// would otherwise reach an AWS-signed 401 rather than this local, no-network
// classification.
func TestLoadTenantDashboardNeverUsesAnAWSAliasForThePlatform(t *testing.T) {
	app := testAWSAliasApp(t)
	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{Tenant: "frs"})
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

func TestLoadAPILogPrefersKubernetesLogs(t *testing.T) {
	t.Setenv("PATH", fakeKubectl(t, func(args []string) (string, int) {
		got := strings.Join(args, "\n")
		// The erun-backend-api chart's Deployment/Service is <tenant>-api
		// (open.APIDeploymentName), not the runtime's <tenant>-devops
		// Deployment: the erun-backend-api container never exists there.
		want := "--context\ncluster-dev\n--namespace\nfrs-prod\nlogs\n-l\napp=frs-api\n--prefix\n-c\nerun-backend-api\n--tail\n400"
		if got != want {
			t.Fatalf("unexpected kubectl args:\n%s", got)
		}
		return "api log from pod\n", 0
	})+string(os.PathListSeparator)+os.Getenv("PATH"))

	log, err := loadAPILog(context.Background(), uiTenantDashboardInput{
		Tenant:            "frs",
		Environment:       "prod",
		MCPURL:            "http://127.0.0.1:17000/mcp",
		KubernetesContext: "cluster-dev",
	})
	if err != nil {
		t.Fatalf("loadAPILog failed: %v", err)
	}
	if log != "api log from pod" {
		t.Fatalf("unexpected log: %q", log)
	}
}

func fakeKubectl(t *testing.T, handler func([]string) (string, int)) string {
	t.Helper()
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "kubectl.args")
	outputPath := filepath.Join(dir, "kubectl.output")
	exitPath := filepath.Join(dir, "kubectl.exit")
	writeFakeKubectlStub(t, dir, argsPath, outputPath, exitPath)
	output, code := handler([]string{"--context", "cluster-dev", "--namespace", "frs-prod", "logs", "-l", "app=frs-api", "--prefix", "-c", "erun-backend-api", "--tail", "400"})
	if err := os.WriteFile(outputPath, []byte(output), 0o644); err != nil {
		t.Fatalf("write fake kubectl output failed: %v", err)
	}
	if err := os.WriteFile(exitPath, []byte(fmt.Sprintf("%d", code)), 0o644); err != nil {
		t.Fatalf("write fake kubectl exit failed: %v", err)
	}
	t.Cleanup(func() {
		data, err := os.ReadFile(argsPath)
		if err != nil {
			t.Fatalf("read fake kubectl args failed: %v", err)
		}
		// The Windows stub records CRLF-terminated lines; normalize so the
		// recorded args match what production passed.
		normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
		handler(strings.Split(strings.TrimSpace(normalized), "\n"))
	})
	return dir
}

// writeFakeKubectlStub drops a `kubectl` on PATH that records its args, replays a
// captured stdout, and exits with a captured code. On Windows it must be a real
// .cmd (exec.LookPath only honors PATHEXT extensions, and a #!/bin/sh script is
// not runnable), with CRLF line endings or cmd.exe mis-parses the label loop.
func writeFakeKubectlStub(t *testing.T, dir, argsPath, outputPath, exitPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		script := "@echo off\r\n" +
			"type nul > \"" + argsPath + "\"\r\n" +
			":loop\r\n" +
			"if \"%~1\"==\"\" goto after\r\n" +
			">> \"" + argsPath + "\" echo(%~1\r\n" +
			"shift\r\n" +
			"goto loop\r\n" +
			":after\r\n" +
			"type \"" + outputPath + "\"\r\n" +
			"set /p code=< \"" + exitPath + "\"\r\n" +
			"exit /b %code%\r\n"
		if err := os.WriteFile(filepath.Join(dir, "kubectl.cmd"), []byte(script), 0o755); err != nil {
			t.Fatalf("write fake kubectl failed: %v", err)
		}
		return
	}
	script := `#!/bin/sh
printf '%s\n' "$@" >"` + argsPath + `"
cat "` + outputPath + `"
exit "$(cat "` + exitPath + `")"
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kubectl failed: %v", err)
	}
}
