package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

type stubEnrolledUserLister struct {
	users     []model.User
	err       error
	gotFilter repository.UserFilter
}

func (s *stubEnrolledUserLister) List(_ context.Context, filter repository.UserFilter) ([]model.User, error) {
	s.gotFilter = filter
	return s.users, s.err
}

type stubIdentityAdminClient struct {
	createdOrgName    string
	createOrgErr      error
	users             []zitadel.User
	listErr           error
	gotListOrgID      string
	deactivateErr     error
	reactivateErr     error
	gotDeactivateID   string
	gotDeactivateOrg  string
	gotReactivateID   string
	gotReactivateOrg  string
	settings          zitadel.OrgSettings
	getSettingsErr    error
	updateSettingsErr error
	gotUpdateParams   zitadel.UpdateOrgSettingsParams
	smtpStatus        zitadel.SMTPStatus
	getSMTPErr        error
	updateSMTPErr     error
	gotSMTPParams     zitadel.SetSMTPConfigParams
}

func (s *stubIdentityAdminClient) ListUsers(_ context.Context, orgID string) ([]zitadel.User, error) {
	s.gotListOrgID = orgID
	return s.users, s.listErr
}

func (s *stubIdentityAdminClient) DeactivateUser(_ context.Context, orgID string, userID string) error {
	s.gotDeactivateOrg = orgID
	s.gotDeactivateID = userID
	return s.deactivateErr
}

func (s *stubIdentityAdminClient) ReactivateUser(_ context.Context, orgID string, userID string) error {
	s.gotReactivateOrg = orgID
	s.gotReactivateID = userID
	return s.reactivateErr
}

func (s *stubIdentityAdminClient) GetOrgSettings(context.Context) (zitadel.OrgSettings, error) {
	return s.settings, s.getSettingsErr
}

func (s *stubIdentityAdminClient) UpdateOrgSettings(_ context.Context, params zitadel.UpdateOrgSettingsParams) (zitadel.OrgSettings, error) {
	s.gotUpdateParams = params
	return s.settings, s.updateSettingsErr
}

func (s *stubIdentityAdminClient) CreateOrg(_ context.Context, name string) (zitadel.Org, error) {
	s.createdOrgName = name
	if s.createOrgErr != nil {
		return zitadel.Org{}, s.createOrgErr
	}
	return zitadel.Org{ID: "org-1", Name: name}, nil
}

func (s *stubIdentityAdminClient) GetSMTPStatus(context.Context) (zitadel.SMTPStatus, error) {
	return s.smtpStatus, s.getSMTPErr
}

func (s *stubIdentityAdminClient) UpdateSMTPConfig(_ context.Context, params zitadel.SetSMTPConfigParams) (zitadel.SMTPStatus, error) {
	s.gotSMTPParams = params
	return s.smtpStatus, s.updateSMTPErr
}

type stubIdentityEnroller struct {
	result    service.EnrollIdentityResult
	err       error
	gotParams service.EnrollIdentityParams
}

func (s *stubIdentityEnroller) Enroll(_ context.Context, params service.EnrollIdentityParams) (service.EnrollIdentityResult, error) {
	s.gotParams = params
	return s.result, s.err
}

func identityRequest(method, path, body, tenantType string) *http.Request {
	var reader *bytes.Buffer
	if body != "" {
		reader = bytes.NewBufferString(body)
	} else {
		reader = bytes.NewBufferString("")
	}
	req := httptest.NewRequest(method, path, reader)
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:       "tenant-ops",
		TenantType:     tenantType,
		ErunUserID:     "caller-user",
		ExternalIssuer: "https://auth.example.com",
	}))
	return req
}

func TestListUsersForbidsNonOperationsTenant(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	routes := IdentityRoutes{admin: admin}
	rec := httptest.NewRecorder()
	routes.listUsers(rec, identityRequest(http.MethodGet, "/v1/identity/users", "", string(model.TenantTypeCompany)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestListUsersReturnsAdminResult(t *testing.T) {
	admin := &stubIdentityAdminClient{users: []zitadel.User{{ID: "u1", Username: "alice"}}}
	routes := IdentityRoutes{admin: admin, erunUsers: &stubEnrolledUserLister{}}
	rec := httptest.NewRecorder()
	routes.listUsers(rec, identityRequest(http.MethodGet, "/v1/identity/users", "", string(model.TenantTypeOperations)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestListUsersDefaultsToNoOrgOverride(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	routes := IdentityRoutes{admin: admin, erunUsers: &stubEnrolledUserLister{}}
	rec := httptest.NewRecorder()
	routes.listUsers(rec, identityRequest(http.MethodGet, "/v1/identity/users", "", string(model.TenantTypeOperations)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if admin.gotListOrgID != "" {
		t.Fatalf("gotListOrgID = %q, want empty when ?orgId= is not given", admin.gotListOrgID)
	}
}

// An identity in another org is invisible unless that org is named on the read.
func TestListUsersThreadsOrgIDQueryParam(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	routes := IdentityRoutes{admin: admin, erunUsers: &stubEnrolledUserLister{}}
	rec := httptest.NewRecorder()
	routes.listUsers(rec, identityRequest(http.MethodGet, "/v1/identity/users?orgId=org-tenant-x", "", string(model.TenantTypeOperations)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if admin.gotListOrgID != "org-tenant-x" {
		t.Fatalf("gotListOrgID = %q, want org-tenant-x", admin.gotListOrgID)
	}
}

func TestListUsersDefaultsMembershipJoinToCallersOwnTenant(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	erunUsers := &stubEnrolledUserLister{}
	routes := IdentityRoutes{admin: admin, erunUsers: erunUsers}
	rec := httptest.NewRecorder()
	routes.listUsers(rec, identityRequest(http.MethodGet, "/v1/identity/users", "", string(model.TenantTypeOperations)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if erunUsers.gotFilter.TenantID != "tenant-ops" {
		t.Fatalf("gotFilter.TenantID = %q, want the caller's own tenant-ops", erunUsers.gotFilter.TenantID)
	}
}

// The join must target the administered tenant, or members of other orgs render as IdP-only.
func TestListUsersThreadsTenantIDQueryParamForMembershipJoin(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	erunUsers := &stubEnrolledUserLister{}
	routes := IdentityRoutes{admin: admin, erunUsers: erunUsers}
	rec := httptest.NewRecorder()
	routes.listUsers(rec, identityRequest(http.MethodGet, "/v1/identity/users?orgId=org-tenant-x&tenantId=tenant-x", "", string(model.TenantTypeOperations)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if erunUsers.gotFilter.TenantID != "tenant-x" {
		t.Fatalf("gotFilter.TenantID = %q, want tenant-x", erunUsers.gotFilter.TenantID)
	}
}

// Also proves the machine-account signal survives the merge.
func TestListUsersDistinguishesEnrolledFromIdPOnly(t *testing.T) {
	admin := &stubIdentityAdminClient{users: []zitadel.User{
		{ID: "sub-alice", Username: "alice", Email: "alice@example.com"},
		{ID: "sub-stranger", Username: "stranger", Email: "stranger@example.com"},
		{ID: "sub-svc", Username: "admin-sa", IsMachine: true},
	}}
	erunUsers := &stubEnrolledUserLister{users: []model.User{
		{UserID: "erun-alice", Username: "alice", ExternalUserID: "sub-alice"},
	}}
	routes := IdentityRoutes{admin: admin, erunUsers: erunUsers}
	rec := httptest.NewRecorder()
	routes.listUsers(rec, identityRequest(http.MethodGet, "/v1/identity/users", "", string(model.TenantTypeOperations)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var views []identityUserView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(views) != 3 {
		t.Fatalf("got %d views, want 3", len(views))
	}
	if !views[0].Enrolled || views[0].ErunUserID != "erun-alice" {
		t.Fatalf("views[0] (alice) = %+v, want Enrolled with erun-alice", views[0])
	}
	if views[1].Enrolled || views[1].ErunUserID != "" {
		t.Fatalf("views[1] (stranger) = %+v, want not enrolled", views[1])
	}
	if !views[2].IsMachine || views[2].Enrolled {
		t.Fatalf("views[2] (admin-sa) = %+v, want a machine account, not enrolled", views[2])
	}
}

// `id` is the OIDC subject, the one value joining both directories.
func TestListUsersReportsErunUsernameAlongsideIdPUsername(t *testing.T) {
	admin := &stubIdentityAdminClient{users: []zitadel.User{
		{ID: "sub-1", Username: "zadmin@frs.auth.example.com", Email: "admin@example.com"},
	}}
	erunUsers := &stubEnrolledUserLister{users: []model.User{
		{UserID: "erun-1", Username: "erun", ExternalUserID: "sub-1"},
	}}
	routes := IdentityRoutes{admin: admin, erunUsers: erunUsers}
	rec := httptest.NewRecorder()
	routes.listUsers(rec, identityRequest(http.MethodGet, "/v1/identity/users", "", string(model.TenantTypeOperations)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var views []identityUserView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("got %d views, want 1", len(views))
	}
	if views[0].Username != "zadmin@frs.auth.example.com" {
		t.Fatalf("views[0].Username = %q, want the IdP username unchanged", views[0].Username)
	}
	if views[0].ErunUsername != "erun" {
		t.Fatalf("views[0].ErunUsername = %q, want the enrolled erun user's own username", views[0].ErunUsername)
	}
	if views[0].ID != "sub-1" {
		t.Fatalf("views[0].ID = %q, want the OIDC subject reported so a client can join whoami's own subject against it", views[0].ID)
	}
}

func TestCreateUserRejectsMissingFields(t *testing.T) {
	enroller := &stubIdentityEnroller{}
	routes := IdentityRoutes{enroller: enroller}
	rec := httptest.NewRecorder()
	routes.createUser(rec, identityRequest(http.MethodPost, "/v1/identity/users", `{"username":"alice"}`, string(model.TenantTypeOperations)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a missing email", rec.Code)
	}
}

func TestCreateUserEnrollsAndPassesCallerIssuer(t *testing.T) {
	enroller := &stubIdentityEnroller{result: service.EnrollIdentityResult{
		IdPUser:  zitadel.User{ID: "idp-1", Username: "alice"},
		ErunUser: model.User{UserID: "erun-1", Username: "alice"},
	}}
	routes := IdentityRoutes{enroller: enroller}
	rec := httptest.NewRecorder()
	routes.createUser(rec, identityRequest(http.MethodPost, "/v1/identity/users", `{"username":"alice","email":"alice@example.com"}`, string(model.TenantTypeOperations)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if enroller.gotParams.Issuer != "https://auth.example.com" {
		t.Fatalf("Issuer = %q, want the caller's own external issuer", enroller.gotParams.Issuer)
	}
}

func TestCreateUserReports201WithErrorOnMappingFailure(t *testing.T) {
	enroller := &stubIdentityEnroller{
		result: service.EnrollIdentityResult{IdPUser: zitadel.User{ID: "idp-2"}},
		err:    service.ErrIdentityMappingFailed,
	}
	routes := IdentityRoutes{enroller: enroller}
	rec := httptest.NewRecorder()
	routes.createUser(rec, identityRequest(http.MethodPost, "/v1/identity/users", `{"username":"bob","email":"bob@example.com"}`, string(model.TenantTypeOperations)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (the IdP half landed) with the mapping failure reported in the body; body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("idp-2")) {
		t.Fatalf("body = %s, want it to name the orphaned idp user", rec.Body.String())
	}
}

// The error is wrapped as the service wraps it.
func TestCreateUserReportsATakenLoginNameAsUsernameTaken(t *testing.T) {
	enroller := &stubIdentityEnroller{err: fmt.Errorf("create identity provider user: %w",
		&zitadel.UsernameTakenError{Username: "login-client"})}
	routes := IdentityRoutes{enroller: enroller}
	rec := httptest.NewRecorder()
	routes.createUser(rec, identityRequest(http.MethodPost, "/v1/identity/users", `{"username":"login-client","email":"ops@frs.example"}`, string(model.TenantTypeOperations)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"USERNAME_TAKEN"`) {
		t.Fatalf("body = %q, want the USERNAME_TAKEN machine code", body)
	}
	if !strings.Contains(body, "login-client") {
		t.Fatalf("body = %q, want the offending login name named in the message", body)
	}
}

func TestCreateUserForwardsAnUnrelatedConflictFromTheIdP(t *testing.T) {
	enroller := &stubIdentityEnroller{err: &zitadel.APIError{
		StatusCode: http.StatusConflict,
		Body:       `{"code":6,"message":"Errors.User.EmailAlreadyExists"}`,
	}}
	routes := IdentityRoutes{enroller: enroller}
	rec := httptest.NewRecorder()
	routes.createUser(rec, identityRequest(http.MethodPost, "/v1/identity/users", `{"username":"alice","email":"a@example.com"}`, string(model.TenantTypeOperations)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want the instance's own 409 forwarded; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "USERNAME_TAKEN") {
		t.Fatalf("body = %q, must not relabel an unrelated conflict as a taken login name", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Errors.User.EmailAlreadyExists") {
		t.Fatalf("body = %q, want the instance's own message forwarded unchanged", rec.Body.String())
	}
}

func TestDeactivateAndReactivateUsePathValue(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	routes := IdentityRoutes{admin: admin}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/identity/users/{external_id}/deactivate", routes.deactivateUser)
	mux.HandleFunc("POST /v1/identity/users/{external_id}/reactivate", routes.reactivateUser)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, identityRequest(http.MethodPost, "/v1/identity/users/idp-9/deactivate", "", string(model.TenantTypeOperations)))
	if rec.Code != http.StatusNoContent || admin.gotDeactivateID != "idp-9" {
		t.Fatalf("status=%d gotDeactivateID=%q, want 204 and idp-9", rec.Code, admin.gotDeactivateID)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, identityRequest(http.MethodPost, "/v1/identity/users/idp-9/reactivate", "", string(model.TenantTypeOperations)))
	if rec.Code != http.StatusNoContent || admin.gotReactivateID != "idp-9" {
		t.Fatalf("status=%d gotReactivateID=%q, want 204 and idp-9", rec.Code, admin.gotReactivateID)
	}
}

func TestDeactivateAndReactivateThreadOrgIDQueryParam(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	routes := IdentityRoutes{admin: admin}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/identity/users/{external_id}/deactivate", routes.deactivateUser)
	mux.HandleFunc("POST /v1/identity/users/{external_id}/reactivate", routes.reactivateUser)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, identityRequest(http.MethodPost, "/v1/identity/users/idp-9/deactivate?orgId=org-tenant-x", "", string(model.TenantTypeOperations)))
	if rec.Code != http.StatusNoContent || admin.gotDeactivateOrg != "org-tenant-x" {
		t.Fatalf("status=%d gotDeactivateOrg=%q, want 204 and org-tenant-x", rec.Code, admin.gotDeactivateOrg)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, identityRequest(http.MethodPost, "/v1/identity/users/idp-9/reactivate?orgId=org-tenant-x", "", string(model.TenantTypeOperations)))
	if rec.Code != http.StatusNoContent || admin.gotReactivateOrg != "org-tenant-x" {
		t.Fatalf("status=%d gotReactivateOrg=%q, want 204 and org-tenant-x", rec.Code, admin.gotReactivateOrg)
	}
}

func TestDeactivateForwardsZitadelAPIError(t *testing.T) {
	admin := &stubIdentityAdminClient{deactivateErr: &zitadel.APIError{StatusCode: http.StatusNotFound, Body: "User with state initial can only be deleted not deactivated"}}
	routes := IdentityRoutes{admin: admin}
	rec := httptest.NewRecorder()
	routes.deactivateUser(rec, identityRequest(http.MethodPost, "/v1/identity/users/idp-1/deactivate", "", string(model.TenantTypeOperations)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want the forwarded Zitadel status", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("state initial")) {
		t.Fatalf("body = %s, want the Zitadel message forwarded", rec.Body.String())
	}
}
