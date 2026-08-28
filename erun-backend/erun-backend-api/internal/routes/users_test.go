package routes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type stubUserEnrollmentRepository struct {
	createCalls int
	createErr   error
	gotCreate   repository.CreateUserParams

	listCalls int
	listErr   error
	gotFilter repository.UserFilter
	users     []model.User
}

func (r *stubUserEnrollmentRepository) Create(_ context.Context, params repository.CreateUserParams) (model.User, error) {
	r.createCalls++
	r.gotCreate = params
	if r.createErr != nil {
		return model.User{}, r.createErr
	}
	tenantID := params.TenantID
	if tenantID == "" {
		tenantID = "caller-tenant"
	}
	return model.User{UserID: "user-new", TenantID: tenantID, Username: params.Username}, nil
}

func (r *stubUserEnrollmentRepository) List(_ context.Context, filter repository.UserFilter) ([]model.User, error) {
	r.listCalls++
	r.gotFilter = filter
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.users, nil
}

func postUsers(t *testing.T, users *stubUserEnrollmentRepository, tenantType, tenantID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewBufferString(body))
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   tenantID,
		TenantType: tenantType,
		ErunUserID: "caller-user",
	}))
	rec := httptest.NewRecorder()
	UserRoutes{users: users}.createUser(rec, req)
	return rec
}

func getUsers(t *testing.T, users *stubUserEnrollmentRepository, tenantType, tenantID, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/users"+query, nil)
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   tenantID,
		TenantType: tenantType,
		ErunUserID: "caller-user",
	}))
	rec := httptest.NewRecorder()
	UserRoutes{users: users}.listUsers(rec, req)
	return rec
}

func TestCreateUserEnrollsInOwnTenantByDefault(t *testing.T) {
	users := &stubUserEnrollmentRepository{}
	rec := postUsers(t, users, string(model.TenantTypeCompany), "tenant-a", `{"username":"alice"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if users.createCalls != 1 || users.gotCreate.TenantID != "" || users.gotCreate.Username != "alice" {
		t.Fatalf("Create called %d times params=%+v, want 1 call with no tenant override", users.createCalls, users.gotCreate)
	}
}

func TestCreateUserForbidsCrossTenantWithoutOperations(t *testing.T) {
	users := &stubUserEnrollmentRepository{}
	rec := postUsers(t, users, string(model.TenantTypeCompany), "tenant-a", `{"username":"alice","tenantId":"tenant-b"}`)
	if rec.Code != http.StatusForbidden || users.createCalls != 0 {
		t.Fatalf("status=%d createCalls=%d, want 403 / 0 calls", rec.Code, users.createCalls)
	}
}

func TestCreateUserAllowsOperationsCrossTenantOverride(t *testing.T) {
	users := &stubUserEnrollmentRepository{}
	rec := postUsers(t, users, string(model.TenantTypeOperations), "ops-tenant", `{"username":"alice","tenantId":"tenant-b","issuer":"https://issuer.example","subject":"sub-1"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if users.createCalls != 1 || users.gotCreate.TenantID != "tenant-b" || users.gotCreate.Issuer != "https://issuer.example" || users.gotCreate.Subject != "sub-1" {
		t.Fatalf("Create called %d times params=%+v, want 1 call targeting tenant-b with the external identity", users.createCalls, users.gotCreate)
	}
}

func TestCreateUserRejectsEmptyUsername(t *testing.T) {
	users := &stubUserEnrollmentRepository{}
	rec := postUsers(t, users, string(model.TenantTypeCompany), "tenant-a", `{"username":"  "}`)
	if rec.Code != http.StatusBadRequest || users.createCalls != 0 {
		t.Fatalf("status=%d createCalls=%d, want 400 / 0 calls", rec.Code, users.createCalls)
	}
}

func TestCreateUserMapsConflictToStatusConflict(t *testing.T) {
	users := &stubUserEnrollmentRepository{createErr: repository.ErrConflict}
	rec := postUsers(t, users, string(model.TenantTypeCompany), "tenant-a", `{"username":"alice"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestCreateUserPassesThroughExplicitRoleIDs(t *testing.T) {
	users := &stubUserEnrollmentRepository{}
	rec := postUsers(t, users, string(model.TenantTypeCompany), "tenant-a", `{"username":"alice","roleIds":["role-1","role-2"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if len(users.gotCreate.RoleIDs) != 2 || users.gotCreate.RoleIDs[0] != "role-1" || users.gotCreate.RoleIDs[1] != "role-2" {
		t.Fatalf("gotCreate.RoleIDs = %+v, want [role-1 role-2]", users.gotCreate.RoleIDs)
	}
}

func TestCreateUserMapsUnknownRoleToStatusNotFound(t *testing.T) {
	users := &stubUserEnrollmentRepository{createErr: repository.ErrNotFound}
	rec := postUsers(t, users, string(model.TenantTypeCompany), "tenant-a", `{"username":"alice","roleIds":["missing-role"]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestListUsersScopesToOwnTenantByDefault(t *testing.T) {
	users := &stubUserEnrollmentRepository{users: []model.User{{UserID: "u1", TenantID: "tenant-a", Username: "alice"}}}
	rec := getUsers(t, users, string(model.TenantTypeCompany), "tenant-a", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if users.listCalls != 1 || users.gotFilter.TenantID != "tenant-a" {
		t.Fatalf("List called %d times filter=%+v, want 1 call scoped to tenant-a", users.listCalls, users.gotFilter)
	}
}

func TestListUsersForbidsCrossTenantWithoutOperations(t *testing.T) {
	users := &stubUserEnrollmentRepository{}
	rec := getUsers(t, users, string(model.TenantTypeCompany), "tenant-a", "?tenantId=tenant-b")
	if rec.Code != http.StatusForbidden || users.listCalls != 0 {
		t.Fatalf("status=%d listCalls=%d, want 403 / 0 calls", rec.Code, users.listCalls)
	}
}

func TestListUsersAllowsOperationsCrossTenantOverride(t *testing.T) {
	users := &stubUserEnrollmentRepository{}
	rec := getUsers(t, users, string(model.TenantTypeOperations), "ops-tenant", "?tenantId=tenant-b")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if users.listCalls != 1 || users.gotFilter.TenantID != "tenant-b" {
		t.Fatalf("List called %d times filter=%+v, want 1 call scoped to tenant-b", users.listCalls, users.gotFilter)
	}
}
