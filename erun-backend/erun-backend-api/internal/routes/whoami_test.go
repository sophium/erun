package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type stubWhoamiUserRepository struct {
	user  model.User
	roles []string
}

func (r stubWhoamiUserRepository) Get(context.Context, string) (model.User, error) {
	return r.user, nil
}

func (r stubWhoamiUserRepository) RoleNames(context.Context, string) ([]string, error) {
	return r.roles, nil
}

type stubWhoamiTenantRepository struct {
	tenant model.Tenant
}

func (r stubWhoamiTenantRepository) Current(context.Context) (model.Tenant, error) {
	return r.tenant, nil
}

// TestWhoamiTenantNameMatchesTenantRecordNotUsername locks erun#2083: an
// operator read whoami's leading username field as if it were the tenant's
// name, because whoami carried no tenant name field at all and its
// plain-text rendering put the username exactly where a tenant name would
// visually be expected -- the two fields happened to read "erun" and "frs"
// for the same tenant id, which looked like a naming disagreement between
// whoami and tenant list. There is only one tenants.name column (tenant.go's
// model.Tenant.Name); whoami must report that same value, off the same
// TenantRepository.Current call tenant list uses, never the caller's
// username.
func TestWhoamiTenantNameMatchesTenantRecordNotUsername(t *testing.T) {
	userRepo := stubWhoamiUserRepository{user: model.User{Username: "erun"}}
	tenantRepo := stubWhoamiTenantRepository{tenant: model.Tenant{TenantID: "tenant-1", Name: "frs"}}
	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:       "tenant-1",
		ErunUserID:     "user-1",
		ExternalIssuer: "https://issuer.example",
		ExternalUserID: "external-user-1",
	}))
	rec := httptest.NewRecorder()

	WhoamiRoutes{users: userRepo, tenants: tenantRepo}.handleWhoami(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var response whoamiResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.TenantName != "frs" {
		t.Fatalf("whoami must report the tenant's real name %q (the same name tenant list reports for this tenant id), got %q", "frs", response.TenantName)
	}
	if response.TenantName == response.Username {
		t.Fatalf("whoami's tenant name must never equal the caller's username (%q); an operator confusing the two is exactly the bug this locks", response.Username)
	}
}

func TestWhoamiReturnsUsernameAndRoles(t *testing.T) {
	repo := stubWhoamiUserRepository{
		user: model.User{Username: "Rihards.Freimanis"},
		roles: []string{
			"ReadAll",
			"WriteAll",
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:       "tenant-1",
		ErunUserID:     "user-1",
		ExternalIssuer: "https://issuer.example",
		ExternalUserID: "external-user-1",
	}))
	rec := httptest.NewRecorder()

	WhoamiRoutes{users: repo}.handleWhoami(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var response whoamiResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Username != "Rihards.Freimanis" {
		t.Fatalf("unexpected username: %q", response.Username)
	}
	if len(response.Roles) != 2 || response.Roles[0] != "ReadAll" || response.Roles[1] != "WriteAll" {
		t.Fatalf("unexpected roles: %+v", response.Roles)
	}
}
