package routes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type stubRoleRepository struct {
	roles     []model.Role
	listErr   error
	createErr error
	gotName   string
	gotPerms  []apirepository.RolePermissionInput
	created   model.Role

	forUser    []model.Role
	forUserErr error
	gotUserID  string

	grantErr   error
	granted    model.UserRole
	gotGrant   struct{ userID, roleID, tenantID string }
	grantCalls int

	revokeErr   error
	gotRevoke   struct{ userID, roleID, tenantID string }
	revokeCalls int
}

func (r *stubRoleRepository) List(_ context.Context) ([]model.Role, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.roles, nil
}

func (r *stubRoleRepository) Create(_ context.Context, name string, permissions []apirepository.RolePermissionInput) (model.Role, error) {
	r.gotName = name
	r.gotPerms = permissions
	if r.createErr != nil {
		return model.Role{}, r.createErr
	}
	return r.created, nil
}

func (r *stubRoleRepository) ForUser(_ context.Context, userID string) ([]model.Role, error) {
	r.gotUserID = userID
	if r.forUserErr != nil {
		return nil, r.forUserErr
	}
	return r.forUser, nil
}

func (r *stubRoleRepository) Grant(_ context.Context, userID string, roleID string, tenantID string) (model.UserRole, error) {
	r.grantCalls++
	r.gotGrant = struct{ userID, roleID, tenantID string }{userID, roleID, tenantID}
	if r.grantErr != nil {
		return model.UserRole{}, r.grantErr
	}
	return r.granted, nil
}

func (r *stubRoleRepository) Revoke(_ context.Context, userID string, roleID string, tenantID string) error {
	r.revokeCalls++
	r.gotRevoke = struct{ userID, roleID, tenantID string }{userID, roleID, tenantID}
	return r.revokeErr
}

func TestListRolesReturnsRepositoryRoles(t *testing.T) {
	roles := &stubRoleRepository{roles: []model.Role{{RoleID: "role-1", Name: "ReadAll"}}}
	req := httptest.NewRequest(http.MethodGet, "/v1/roles", nil)
	rec := httptest.NewRecorder()
	RoleRoutes{roles: roles}.listRoles(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateRoleRejectsMissingName(t *testing.T) {
	roles := &stubRoleRepository{}
	req := httptest.NewRequest(http.MethodPost, "/v1/roles", bytes.NewBufferString(`{"permissions":[{"apiMethod":"GET","apiPath":"/v1/reviews"}]}`))
	rec := httptest.NewRecorder()
	RoleRoutes{roles: roles}.createRole(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateRoleRejectsNoPermissions(t *testing.T) {
	roles := &stubRoleRepository{}
	req := httptest.NewRequest(http.MethodPost, "/v1/roles", bytes.NewBufferString(`{"name":"Reviewer"}`))
	rec := httptest.NewRecorder()
	RoleRoutes{roles: roles}.createRole(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateRoleNarrowedToOnePathSucceeds(t *testing.T) {
	roles := &stubRoleRepository{created: model.Role{RoleID: "role-1", Name: "Reviewer"}}
	body := `{"name":"Reviewer","permissions":[{"apiMethod":"GET","apiPath":"/v1/reviews"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/roles", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	RoleRoutes{roles: roles}.createRole(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if roles.gotName != "Reviewer" || len(roles.gotPerms) != 1 || roles.gotPerms[0].APIMethod != "GET" || roles.gotPerms[0].APIPath != "/v1/reviews" {
		t.Fatalf("Create called with name=%q perms=%+v", roles.gotName, roles.gotPerms)
	}
}

func TestCreateRoleMapsInvalidInputToBadRequest(t *testing.T) {
	roles := &stubRoleRepository{createErr: apirepository.ErrInvalidInput}
	body := `{"name":"Reviewer","permissions":[{"apiMethod":"GET","apiPath":"/v1/reviews"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/roles", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	RoleRoutes{roles: roles}.createRole(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateRoleMapsConflictToStatusConflict(t *testing.T) {
	roles := &stubRoleRepository{createErr: apirepository.ErrConflict}
	body := `{"name":"ReadAll","permissions":[{"apiMethod":"GET","apiPath":"/v1/reviews"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/roles", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	RoleRoutes{roles: roles}.createRole(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

func TestListUserRolesUsesPathUserID(t *testing.T) {
	roles := &stubRoleRepository{forUser: []model.Role{{RoleID: "role-1", Name: "ReadAll"}}}
	req := httptest.NewRequest(http.MethodGet, "/v1/users/user-1/roles", nil)
	req.SetPathValue("user_id", "user-1")
	rec := httptest.NewRecorder()
	RoleRoutes{roles: roles}.listUserRoles(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if roles.gotUserID != "user-1" {
		t.Fatalf("ForUser called with userID=%q, want user-1", roles.gotUserID)
	}
}

// postGrant drives the route with the security context the authentication
// middleware always stamps, since the route now resolves the target tenant
// from it.
func postGrant(t *testing.T, roles *stubRoleRepository, tenantType, tenantID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/users/user-1/roles", bytes.NewBufferString(body))
	req.SetPathValue("user_id", "user-1")
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   tenantID,
		TenantType: tenantType,
		ErunUserID: "caller-user",
	}))
	rec := httptest.NewRecorder()
	RoleRoutes{roles: roles}.grantUserRole(rec, req)
	return rec
}

func TestGrantUserRoleRejectsMissingRoleID(t *testing.T) {
	roles := &stubRoleRepository{}
	rec := postGrant(t, roles, string(model.TenantTypeCompany), "tenant-a", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestGrantUserRoleSucceeds(t *testing.T) {
	roles := &stubRoleRepository{granted: model.UserRole{UserID: "user-1", RoleID: "role-1"}}
	rec := postGrant(t, roles, string(model.TenantTypeCompany), "tenant-a", `{"roleId":"role-1"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	// No tenant override for the ordinary case: the column default keeps the
	// grant in the caller's own tenant.
	if roles.gotGrant.userID != "user-1" || roles.gotGrant.roleID != "role-1" || roles.gotGrant.tenantID != "" {
		t.Fatalf("Grant called with %+v, want user-1/role-1 and no tenant override", roles.gotGrant)
	}
}

// TestGrantUserRoleForbidsCrossTenantWithoutOperations is the guard: naming
// another tenant must not be a way for an ordinary tenant to write into it.
func TestGrantUserRoleForbidsCrossTenantWithoutOperations(t *testing.T) {
	roles := &stubRoleRepository{}
	rec := postGrant(t, roles, string(model.TenantTypeCompany), "tenant-a", `{"roleId":"role-1","tenantId":"tenant-b"}`)
	if rec.Code != http.StatusForbidden || roles.grantCalls != 0 {
		t.Fatalf("status=%d grantCalls=%d, want 403 / 0 calls", rec.Code, roles.grantCalls)
	}
}

// TestGrantUserRoleFromOperationsTargetsNamedTenant is the recovery path: an
// operations caller elevates a user in another tenant, and the grant must be
// filed under that tenant rather than under operations.
func TestGrantUserRoleFromOperationsTargetsNamedTenant(t *testing.T) {
	roles := &stubRoleRepository{granted: model.UserRole{UserID: "user-1", RoleID: "role-1", TenantID: "tenant-b"}}
	rec := postGrant(t, roles, string(model.TenantTypeOperations), "tenant-ops", `{"roleId":"role-1","tenantId":"tenant-b"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if roles.gotGrant.tenantID != "tenant-b" {
		t.Fatalf("Grant called with %+v, want tenantID=tenant-b", roles.gotGrant)
	}
}

// TestGrantUserRoleFromOperationsOwnTenantPassesNoOverride keeps the override
// strictly for the cross-tenant case: an operations caller naming its own
// tenant still relies on the column default.
func TestGrantUserRoleFromOperationsOwnTenantPassesNoOverride(t *testing.T) {
	roles := &stubRoleRepository{granted: model.UserRole{UserID: "user-1", RoleID: "role-1"}}
	rec := postGrant(t, roles, string(model.TenantTypeOperations), "tenant-ops", `{"roleId":"role-1","tenantId":"tenant-ops"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if roles.gotGrant.tenantID != "" {
		t.Fatalf("Grant called with %+v, want no tenant override", roles.gotGrant)
	}
}

func TestGrantUserRoleMapsConflictToStatusConflict(t *testing.T) {
	roles := &stubRoleRepository{grantErr: apirepository.ErrConflict}
	rec := postGrant(t, roles, string(model.TenantTypeCompany), "tenant-a", `{"roleId":"role-1"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

func TestGrantUserRoleMapsNotFoundToStatusNotFound(t *testing.T) {
	roles := &stubRoleRepository{grantErr: apirepository.ErrNotFound}
	rec := postGrant(t, roles, string(model.TenantTypeCompany), "tenant-a", `{"roleId":"role-1"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// deleteGrant drives the revoke route with the security context the
// authentication middleware always stamps, and an optional ?tenantId=.
func deleteGrant(t *testing.T, roles *stubRoleRepository, tenantType, tenantID, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/v1/users/user-1/roles/role-1"+query, nil)
	req.SetPathValue("user_id", "user-1")
	req.SetPathValue("role_id", "role-1")
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   tenantID,
		TenantType: tenantType,
		ErunUserID: "caller-user",
	}))
	rec := httptest.NewRecorder()
	RoleRoutes{roles: roles}.revokeUserRole(rec, req)
	return rec
}

func TestRevokeUserRoleSucceeds(t *testing.T) {
	roles := &stubRoleRepository{}
	rec := deleteGrant(t, roles, string(model.TenantTypeCompany), "tenant-a", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if roles.gotRevoke.userID != "user-1" || roles.gotRevoke.roleID != "role-1" || roles.gotRevoke.tenantID != "" {
		t.Fatalf("Revoke called with %+v, want user-1/role-1 and no tenant override", roles.gotRevoke)
	}
}

// TestRevokeUserRoleForbidsCrossTenantWithoutOperations is the same guard the
// grant route has: naming another tenant must not be a way in for an ordinary
// tenant.
func TestRevokeUserRoleForbidsCrossTenantWithoutOperations(t *testing.T) {
	roles := &stubRoleRepository{}
	rec := deleteGrant(t, roles, string(model.TenantTypeCompany), "tenant-a", "?tenantId=tenant-b")
	if rec.Code != http.StatusForbidden || roles.revokeCalls != 0 {
		t.Fatalf("status=%d revokeCalls=%d, want 403 / 0 calls", rec.Code, roles.revokeCalls)
	}
}

// TestRevokeUserRoleFromOperationsTargetsNamedTenant keeps grant and revoke
// symmetrical: an operations caller that can create a cross-tenant grant must be
// able to undo it, or the fix trades one unrecoverable state for another.
func TestRevokeUserRoleFromOperationsTargetsNamedTenant(t *testing.T) {
	roles := &stubRoleRepository{}
	rec := deleteGrant(t, roles, string(model.TenantTypeOperations), "tenant-ops", "?tenantId=tenant-b")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if roles.gotRevoke.tenantID != "tenant-b" {
		t.Fatalf("Revoke called with %+v, want tenantID=tenant-b", roles.gotRevoke)
	}
}

func TestRevokeUserRoleFromOperationsOwnTenantPassesNoOverride(t *testing.T) {
	roles := &stubRoleRepository{}
	rec := deleteGrant(t, roles, string(model.TenantTypeOperations), "tenant-ops", "?tenantId=tenant-ops")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if roles.gotRevoke.tenantID != "" {
		t.Fatalf("Revoke called with %+v, want no tenant override", roles.gotRevoke)
	}
}

// TestRevokeUserRoleRefusesLastGrantCapableRole is the route-level half of the
// lockout guard: the repository's refusal must reach the caller as a 409 with
// an actionable message, not a generic 500.
func TestRevokeUserRoleRefusesLastGrantCapableRole(t *testing.T) {
	roles := &stubRoleRepository{revokeErr: apirepository.ErrLastGrantCapableRole}
	rec := deleteGrant(t, roles, string(model.TenantTypeCompany), "tenant-a", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRevokeUserRoleMapsNotFoundToStatusNotFound(t *testing.T) {
	roles := &stubRoleRepository{revokeErr: apirepository.ErrNotFound}
	rec := deleteGrant(t, roles, string(model.TenantTypeCompany), "tenant-a", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
