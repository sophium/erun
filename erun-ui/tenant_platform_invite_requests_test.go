package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubmitTenantInviteRequestSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/invite-requests" || req.Method != http.MethodPost {
			t.Fatalf("method=%s path=%s", req.Method, req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"inviteRequestId":"req-1","kind":"JOIN_TENANT","tenantName":"acme","status":"PENDING"}`))
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	outcome, err := app.SubmitTenantInviteRequest(uiSubmitInviteRequestInput{
		Tenant: "frs", Kind: "JOIN_TENANT", TenantName: "acme",
	})
	if err != nil {
		t.Fatalf("SubmitTenantInviteRequest: %v", err)
	}
	if outcome.RateLimited != nil {
		t.Fatalf("expected no rate limit, got %+v", outcome.RateLimited)
	}
	if outcome.Request == nil || outcome.Request.InviteRequestID != "req-1" || outcome.Request.Status != "PENDING" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
}

func TestSubmitTenantInviteRequestRejectsInvalidKind(t *testing.T) {
	app := testERunPlatformAliasApp(t, "https://api.example.test")
	if _, err := app.SubmitTenantInviteRequest(uiSubmitInviteRequestInput{
		Tenant: "frs", Kind: "SOMETHING_ELSE", TenantName: "acme",
	}); err == nil {
		t.Fatal("expected an error for an invalid kind")
	}
}

func TestSubmitTenantInviteRequestRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Retry-After", "30")
		http.Error(w, "too many requests, try again later", http.StatusTooManyRequests)
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	outcome, err := app.SubmitTenantInviteRequest(uiSubmitInviteRequestInput{
		Tenant: "frs", Kind: "JOIN_TENANT", TenantName: "acme",
	})
	if err != nil {
		t.Fatalf("SubmitTenantInviteRequest: %v", err)
	}
	if outcome.Request != nil {
		t.Fatalf("expected no request on a rate-limited outcome, got %+v", outcome.Request)
	}
	if outcome.RateLimited == nil || outcome.RateLimited.RetryAfterSeconds != 30 {
		t.Fatalf("expected a rate-limited outcome naming 30s, got %+v", outcome.RateLimited)
	}
}

// TestSubmitTenantInviteRequestRateLimitedWithoutRetryAfterUsesConfiguredWindow
// covers a 429 with no usable Retry-After (missing entirely here; the RFC
// 9110 HTTP-date form hits the same statusErr.RetryAfter() ok=false path).
// Reporting 0 seconds would read as "try again now" and hide that the
// caller was rate limited at all, so this must fall back to a real,
// non-zero wait rather than discarding the ok bool.
func TestSubmitTenantInviteRequestRateLimitedWithoutRetryAfterUsesConfiguredWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v1/invite-requests":
			http.Error(w, "too many requests, try again later", http.StatusTooManyRequests)
		case "/v1/config":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tenant":{},"inviteRequestRateLimitWindowSeconds":45}`))
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
		}
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	outcome, err := app.SubmitTenantInviteRequest(uiSubmitInviteRequestInput{
		Tenant: "frs", Kind: "JOIN_TENANT", TenantName: "acme",
	})
	if err != nil {
		t.Fatalf("SubmitTenantInviteRequest: %v", err)
	}
	if outcome.RateLimited == nil || outcome.RateLimited.RetryAfterSeconds != 45 {
		t.Fatalf("expected the configured 45s window as the fallback, got %+v", outcome.RateLimited)
	}
}

// TestSubmitTenantInviteRequestRateLimitedWithoutRetryAfterOrConfigUsesDefault
// covers the case where even the configured-window fallback read fails: the
// caller must still see a sane non-zero wait, never 0.
func TestSubmitTenantInviteRequestRateLimitedWithoutRetryAfterOrConfigUsesDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v1/invite-requests":
			http.Error(w, "too many requests, try again later", http.StatusTooManyRequests)
		case "/v1/config":
			http.Error(w, "internal error", http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
		}
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	outcome, err := app.SubmitTenantInviteRequest(uiSubmitInviteRequestInput{
		Tenant: "frs", Kind: "JOIN_TENANT", TenantName: "acme",
	})
	if err != nil {
		t.Fatalf("SubmitTenantInviteRequest: %v", err)
	}
	if outcome.RateLimited == nil || outcome.RateLimited.RetryAfterSeconds != defaultInviteRequestRetryAfterSeconds {
		t.Fatalf("expected the hardcoded default fallback, got %+v", outcome.RateLimited)
	}
}

func TestGetMyTenantInviteRequestNoneYet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "no invite request found", http.StatusNotFound)
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	request, err := app.GetMyTenantInviteRequest(uiTenantInput{Tenant: "frs"})
	if err != nil {
		t.Fatalf("GetMyTenantInviteRequest: %v", err)
	}
	if request != nil {
		t.Fatalf("expected nil when the caller never submitted one, got %+v", request)
	}
}

func TestGetMyTenantInviteRequestFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"inviteRequestId":"req-1","status":"DECLINED","declineReason":"tenant already exists"}`))
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	request, err := app.GetMyTenantInviteRequest(uiTenantInput{Tenant: "frs"})
	if err != nil {
		t.Fatalf("GetMyTenantInviteRequest: %v", err)
	}
	if request == nil || request.Status != "DECLINED" || request.DeclineReason != "tenant already exists" {
		t.Fatalf("unexpected request: %+v", request)
	}
}

func TestDeclineTenantInviteRequestRequiresReason(t *testing.T) {
	app := testERunPlatformAliasApp(t, "https://api.example.test")
	if _, err := app.DeclineTenantInviteRequest(uiDeclineInviteRequestInput{
		Tenant: "frs", InviteRequestID: "req-1", Reason: "   ",
	}); err == nil {
		t.Fatal("expected an error for an empty decline reason")
	}
}

func TestDeclineTenantInviteRequestSendsReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/invite-requests/req-1/decline" {
			t.Fatalf("path = %s", req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"inviteRequestId":"req-1","status":"DECLINED","declineReason":"not now"}`))
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	request, err := app.DeclineTenantInviteRequest(uiDeclineInviteRequestInput{
		Tenant: "frs", InviteRequestID: "req-1", Reason: "not now",
	})
	if err != nil {
		t.Fatalf("DeclineTenantInviteRequest: %v", err)
	}
	if request.Status != "DECLINED" || request.DeclineReason != "not now" {
		t.Fatalf("unexpected request: %+v", request)
	}
}

func TestApproveTenantInviteRequestConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "invite request has already been decided", http.StatusConflict)
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	if _, err := app.ApproveTenantInviteRequest(uiDecideInviteRequestInput{Tenant: "frs", InviteRequestID: "req-1"}); err == nil {
		t.Fatal("expected an error when the request was already decided")
	}
}

func TestListTenantPlatformEnrollmentStatusesNotConnectedIsLocalOnly(t *testing.T) {
	app := testAWSAliasApp(t)
	statuses := app.ListTenantPlatformEnrollmentStatuses(uiListTenantPlatformEnrollmentStatusesInput{Tenants: []string{"frs"}})
	if len(statuses) != 1 || statuses[0].State != tenantEnrollmentLocalOnly {
		t.Fatalf("expected local-only for a tenant with no erun platform alias, got %+v", statuses)
	}
}

func TestListTenantPlatformEnrollmentStatusesEnrolled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tenantId":"tenant-1","userId":"user-1"}`))
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	statuses := app.ListTenantPlatformEnrollmentStatuses(uiListTenantPlatformEnrollmentStatusesInput{Tenants: []string{"frs"}})
	if len(statuses) != 1 || statuses[0].State != tenantEnrollmentEnrolled {
		t.Fatalf("expected enrolled once whoami succeeds, got %+v", statuses)
	}
}

func TestListTenantPlatformEnrollmentStatusesUnknownOnWhoamiFault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	statuses := app.ListTenantPlatformEnrollmentStatuses(uiListTenantPlatformEnrollmentStatusesInput{Tenants: []string{"frs"}})
	if len(statuses) != 1 || statuses[0].State != tenantEnrollmentUnknown {
		t.Fatalf("expected unknown when whoami fails for a reason other than unauthorized/forbidden, got %+v", statuses)
	}
}

func TestListTenantPlatformEnrollmentStatusesUnknownOnMyInviteRequestFault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v1/whoami":
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case "/v1/invite-requests/mine":
			http.Error(w, "internal error", http.StatusInternalServerError)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	statuses := app.ListTenantPlatformEnrollmentStatuses(uiListTenantPlatformEnrollmentStatusesInput{Tenants: []string{"frs"}})
	if len(statuses) != 1 || statuses[0].State != tenantEnrollmentUnknown {
		t.Fatalf("expected unknown when MyInviteRequest fails for a reason other than not-found, got %+v", statuses)
	}
}

func TestListTenantPlatformEnrollmentStatusesLocalOnlyWhenNeverRequested(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v1/whoami":
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case "/v1/invite-requests/mine":
			http.Error(w, "no invite request found", http.StatusNotFound)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	app := testERunPlatformAliasApp(t, server.URL)
	statuses := app.ListTenantPlatformEnrollmentStatuses(uiListTenantPlatformEnrollmentStatusesInput{Tenants: []string{"frs"}})
	if len(statuses) != 1 || statuses[0].State != tenantEnrollmentLocalOnly {
		t.Fatalf("expected local-only when the identity is verified but never requested, got %+v", statuses)
	}
}

func TestListTenantPlatformEnrollmentStatusesPendingAndDeclined(t *testing.T) {
	cases := []struct {
		name       string
		mineBody   string
		wantState  string
		wantReason string
	}{
		{"pending", `{"status":"PENDING"}`, tenantEnrollmentPending, ""},
		{"declined", `{"status":"DECLINED","declineReason":"tenant name taken"}`, tenantEnrollmentDeclined, "tenant name taken"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/v1/whoami":
					http.Error(w, "unauthorized", http.StatusUnauthorized)
				case "/v1/invite-requests/mine":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.mineBody))
				default:
					http.NotFound(w, req)
				}
			}))
			defer server.Close()

			app := testERunPlatformAliasApp(t, server.URL)
			statuses := app.ListTenantPlatformEnrollmentStatuses(uiListTenantPlatformEnrollmentStatusesInput{Tenants: []string{"frs"}})
			if len(statuses) != 1 || statuses[0].State != tc.wantState || statuses[0].DeclineReason != tc.wantReason {
				t.Fatalf("unexpected statuses: %+v", statuses)
			}
		})
	}
}
