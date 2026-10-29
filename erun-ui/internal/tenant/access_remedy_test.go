package tenant

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The non-covering role comes first, so matching by position would pick the wrong one.
const accessRemedyRolesJSON = `[{"roleId":"role-audit","name":"Auditor","permissions":[{"apiMethod":"GET","apiPath":"/v1/audit-events"}]},{"roleId":"role-reviewer","name":"Reviewer","permissions":[{"apiMethodPattern":"^GET$","apiPathPattern":"^/v1/reviews/[^/]+$"}]}]`

const reviewReadDeniedCapabilities = `[{"method":"GET","path":"/v1/whoami"},{"method":"GET","path":"/v1/audit-events"},{"method":"GET","path":"/v1/roles"}]`

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

// Asserts the command itself, not just that the denial names the missing read.
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
	want := "erun platform user grant-role --user-id user-1 --role-id role-author"
	if capability.AccessRemedy.Command != want {
		t.Fatalf("expected the copyable command %q, got %q", want, capability.AccessRemedy.Command)
	}
	if capability.AccessRemedy.RoleName != "Author" {
		t.Fatalf("expected the role that carries the write, got %q", capability.AccessRemedy.RoleName)
	}
}
