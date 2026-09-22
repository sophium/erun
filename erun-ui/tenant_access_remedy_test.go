package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// accessRemedyRolesJSON is the tenant's role list, ordered so the role that
// does NOT cover the review read comes first: a remedy matched by position
// rather than by permission would then name the wrong role, and the tests
// below would see it.
const accessRemedyRolesJSON = `[{"roleId":"role-audit","name":"Auditor","permissions":[{"apiMethod":"GET","apiPath":"/v1/audit-events"}]},{"roleId":"role-reviewer","name":"Reviewer","permissions":[{"apiMethodPattern":"^GET$","apiPathPattern":"^/v1/reviews/[^/]+$"}]}]`

// reviewReadDeniedCapabilities is whoami's answer for a caller enrolled in the
// tenant but refused the review read — the caller whose denial has to carry a
// next step.
const reviewReadDeniedCapabilities = `[{"method":"GET","path":"/v1/whoami"},{"method":"GET","path":"/v1/audit-events"},{"method":"GET","path":"/v1/roles"}]`

// reviewReadDeniedAPI serves the identity read and the role list a denied
// review-detail load needs. Anything else is a 404: the review read is refused
// before it is attempted, so a test that reached another path would be testing
// a different caller.
func reviewReadDeniedAPI(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/v1/whoami":
			_, _ = w.Write([]byte(`{"tenantId":"tenant-1","userId":"user-1","username":"reader","capabilities":` + reviewReadDeniedCapabilities + `}`))
		case "/v1/roles":
			_, _ = w.Write([]byte(accessRemedyRolesJSON))
		default:
			http.NotFound(w, req)
		}
	}))
}

// TestReviewDetailDenialHandsOverTheGrantCommand is the contract this change
// exists for: a caller refused a review is given the exact command that grants
// them a role carrying the read, with their own user id already in it. An
// assertion that the denial merely names the missing read — the old behaviour
// — would pass on the version that stopped short, which is why this one reads
// the command itself.
func TestReviewDetailDenialHandsOverTheGrantCommand(t *testing.T) {
	server := reviewReadDeniedAPI(t)
	defer server.Close()

	detail := loadReviewDetailFrom(t, tenantDashboardApp(t, server.URL))

	if detail.Restricted != tenantDashboardReadReview {
		t.Fatalf("expected the review read to be the restriction, got %q", detail.Restricted)
	}
	remedy, ok := detail.AccessRemedies[detail.Restricted]
	if !ok {
		t.Fatalf("expected the denial to carry a next step, got %+v", detail.AccessRemedies)
	}
	want := "erun platform user grant-role --user-id user-1 --role-id role-reviewer"
	if remedy.Command != want {
		t.Fatalf("expected the copyable command %q, got %q", want, remedy.Command)
	}
	if remedy.RoleName != "Reviewer" {
		t.Fatalf("expected the role named in the platform's own vocabulary, got %q", remedy.RoleName)
	}
}

// TestRestrictedDashboardPanelHandsOverTheGrantCommand covers the same
// contract on the dashboard's tabs, where the missing read is a different one
// (audit events) and so the remedy has to resolve a different role.
func TestRestrictedDashboardPanelHandsOverTheGrantCommand(t *testing.T) {
	requests := []string{}
	server := tenantDashboardAPI(t, `[{"method":"GET","path":"/v1/whoami"},{"method":"GET","path":"/v1/roles"}]`, nil, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	panel := panelFor(t, dashboard, tenantDashboardTabAudit)
	if panel.Restricted != tenantDashboardReadAuditEvents {
		t.Fatalf("expected the audit read to be the restriction, got %q", panel.Restricted)
	}
	remedy, ok := dashboard.AccessRemedies[panel.Restricted]
	if !ok {
		t.Fatalf("expected the restricted panel to carry a next step, got %+v", dashboard.AccessRemedies)
	}
	if !strings.Contains(remedy.Command, "--user-id user-1") {
		t.Fatalf("expected the caller's own user id in %q", remedy.Command)
	}
	if !strings.Contains(remedy.Command, "--role-id role-audit") {
		t.Fatalf("expected the role that covers the audit read, got %q", remedy.Command)
	}
}

// TestRestrictedPanelWithoutACoveringRoleOffersNoCommand locks down the other
// half: when nothing in the tenant grants the missing read, the denial keeps
// its plain sentence rather than handing over a command that would grant a
// role while leaving the caller exactly as refused as before.
func TestRestrictedPanelWithoutACoveringRoleOffersNoCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/v1/whoami":
			_, _ = w.Write([]byte(`{"tenantId":"tenant-1","userId":"user-1","capabilities":[{"method":"GET","path":"/v1/whoami"},{"method":"GET","path":"/v1/roles"}]}`))
		case "/v1/roles":
			_, _ = w.Write([]byte(`[{"roleId":"role-reviewer","name":"Reviewer","permissions":[{"apiMethodPattern":"^GET$","apiPathPattern":"^/v1/reviews/[^/]+$"}]}]`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	panel := panelFor(t, dashboard, tenantDashboardTabAudit)
	if _, ok := dashboard.AccessRemedies[panel.Restricted]; ok {
		t.Fatalf("expected no command when no role covers the read, got %+v", dashboard.AccessRemedies)
	}
}

// TestReviewCreateCapabilityHandsOverTheGrantCommand covers the issue's named
// sibling: the notice that says "You do not have access to create reviews"
// names the capability and nothing else, leaving the operator to work out how
// it is granted. It has to carry the command too.
func TestReviewCreateCapabilityHandsOverTheGrantCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/v1/whoami":
			_, _ = w.Write([]byte(`{"tenantId":"tenant-1","userId":"user-1","username":"reader","capabilities":[{"method":"GET","path":"/v1/whoami"},{"method":"GET","path":"/v1/roles"}]}`))
		case "/v1/roles":
			_, _ = w.Write([]byte(`[{"roleId":"role-reviewer","name":"Reviewer","permissions":[{"apiMethod":"GET","apiPath":"/v1/reviews"}]},{"roleId":"role-author","name":"Author","permissions":[{"apiMethod":"POST","apiPath":"/v1/reviews"}]}]`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	capability, err := tenantDashboardApp(t, server.URL).TenantReviewCreateCapability("frs")
	if err != nil {
		t.Fatalf("TenantReviewCreateCapability failed: %v", err)
	}
	if capability.CanCreate || capability.Restricted == "" {
		t.Fatalf("expected a restricted capability naming why, got %+v", capability)
	}
	if capability.AccessRemedy == nil {
		t.Fatal("expected the notice to carry a next step")
	}
	// The role is resolved by the method it grants: the role that only reads
	// reviews must not be offered as the one that lets the caller open one.
	want := "erun platform user grant-role --user-id user-1 --role-id role-author"
	if capability.AccessRemedy.Command != want {
		t.Fatalf("expected the copyable command %q, got %q", want, capability.AccessRemedy.Command)
	}
	if capability.AccessRemedy.RoleName != "Author" {
		t.Fatalf("expected the role that carries the write, got %q", capability.AccessRemedy.RoleName)
	}
}
