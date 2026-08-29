package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

type stubEnrolledUserLister struct {
	users []model.User
	err   error
}

func (s *stubEnrolledUserLister) List(context.Context, repository.UserFilter) ([]model.User, error) {
	return s.users, s.err
}

type stubIdentityAdminClient struct {
	createdOrgName    string
	createOrgErr      error
	users             []zitadel.User
	listErr           error
	deactivateErr     error
	reactivateErr     error
	gotDeactivateID   string
	gotReactivateID   string
	settings          zitadel.OrgSettings
	getSettingsErr    error
	updateSettingsErr error
	gotUpdateParams   zitadel.UpdateOrgSettingsParams
	smtpStatus        zitadel.SMTPStatus
	getSMTPErr        error
	updateSMTPErr     error
	gotSMTPParams     zitadel.SetSMTPConfigParams
}

func (s *stubIdentityAdminClient) ListUsers(context.Context) ([]zitadel.User, error) {
	return s.users, s.listErr
}

func (s *stubIdentityAdminClient) DeactivateUser(_ context.Context, userID string) error {
	s.gotDeactivateID = userID
	return s.deactivateErr
}

func (s *stubIdentityAdminClient) ReactivateUser(_ context.Context, userID string) error {
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

// TestListUsersDistinguishesEnrolledFromIdPOnly locks the core of #1482's
// Users-page fix: a self-registered IdP account with no erun mapping must
// not render identically to an actual tenant member. It also proves the
// machine-account signal survives the merge unchanged.
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

func TestGetOrgSettingsForbidsNonOperationsTenant(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	routes := IdentityRoutes{admin: admin}
	rec := httptest.NewRecorder()
	routes.getOrgSettings(rec, identityRequest(http.MethodGet, "/v1/identity/org-settings", "", string(model.TenantTypeCompany)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestUpdateOrgSettingsPassesOnlyProvidedFields(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	routes := IdentityRoutes{admin: admin}
	rec := httptest.NewRecorder()
	routes.updateOrgSettings(rec, identityRequest(http.MethodPatch, "/v1/identity/org-settings", `{"forceMfa":true}`, string(model.TenantTypeOperations)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if admin.gotUpdateParams.ForceMFA == nil || !*admin.gotUpdateParams.ForceMFA {
		t.Fatalf("gotUpdateParams.ForceMFA = %v, want true", admin.gotUpdateParams.ForceMFA)
	}
	if admin.gotUpdateParams.MinPasswordLength != nil {
		t.Fatalf("gotUpdateParams.MinPasswordLength = %v, want nil when not provided", admin.gotUpdateParams.MinPasswordLength)
	}
	if admin.gotUpdateParams.AllowRegister != nil {
		t.Fatalf("gotUpdateParams.AllowRegister = %v, want nil when not provided", admin.gotUpdateParams.AllowRegister)
	}
}

// TestUpdateOrgSettingsThreadsAllowRegister locks the actual lever #1482
// asks for: closing (or reopening) self-registration must reach the
// Zitadel client's params, the same way forceMfa already does.
func TestUpdateOrgSettingsThreadsAllowRegister(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	routes := IdentityRoutes{admin: admin}
	rec := httptest.NewRecorder()
	routes.updateOrgSettings(rec, identityRequest(http.MethodPatch, "/v1/identity/org-settings", `{"allowRegister":false}`, string(model.TenantTypeOperations)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if admin.gotUpdateParams.AllowRegister == nil || *admin.gotUpdateParams.AllowRegister {
		t.Fatalf("gotUpdateParams.AllowRegister = %v, want false", admin.gotUpdateParams.AllowRegister)
	}
	if admin.gotUpdateParams.ForceMFA != nil {
		t.Fatalf("gotUpdateParams.ForceMFA = %v, want nil when not provided", admin.gotUpdateParams.ForceMFA)
	}
}

// TestCreateUserReportsMailNotConfiguredWithATemporaryPassword locks the
// honest-failure behaviour (issue #1168): when Enroll reports mail delivery
// as unconfigured, the response must carry the temporary password and a
// warning explaining why, not silently look identical to a normal invite.
func TestCreateUserReportsMailNotConfiguredWithATemporaryPassword(t *testing.T) {
	enroller := &stubIdentityEnroller{result: service.EnrollIdentityResult{
		IdPUser:                zitadel.User{ID: "idp-5", Username: "erin"},
		ErunUser:               model.User{UserID: "erun-5", Username: "erin"},
		MailDeliveryConfigured: false,
		TemporaryPassword:      "Ertemp12345!",
	}}
	routes := IdentityRoutes{enroller: enroller}
	rec := httptest.NewRecorder()
	routes.createUser(rec, identityRequest(http.MethodPost, "/v1/identity/users", `{"username":"erin","email":"erin@example.com"}`, string(model.TenantTypeOperations)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Ertemp12345!")) {
		t.Fatalf("body = %s, want the temporary password reported", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("\"mailDeliveryConfigured\":false")) {
		t.Fatalf("body = %s, want mailDeliveryConfigured:false reported", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("warning")) {
		t.Fatalf("body = %s, want a warning explaining no invitation email was sent", rec.Body.String())
	}
}

// TestCreateUserReportsMailConfiguredWithNoWarning is the positive-path
// counterpart: a normal invite carries no warning and no password.
func TestCreateUserReportsMailConfiguredWithNoWarning(t *testing.T) {
	enroller := &stubIdentityEnroller{result: service.EnrollIdentityResult{
		IdPUser:                zitadel.User{ID: "idp-6", Username: "frank"},
		ErunUser:               model.User{UserID: "erun-6", Username: "frank"},
		MailDeliveryConfigured: true,
	}}
	routes := IdentityRoutes{enroller: enroller}
	rec := httptest.NewRecorder()
	routes.createUser(rec, identityRequest(http.MethodPost, "/v1/identity/users", `{"username":"frank","email":"frank@example.com"}`, string(model.TenantTypeOperations)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("warning")) {
		t.Fatalf("body = %s, want no warning when mail delivery is configured", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("temporaryPassword")) {
		t.Fatalf("body = %s, want no temporaryPassword field when mail delivery is configured", rec.Body.String())
	}
}

func TestGetSMTPSettingsForbidsNonOperationsTenant(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	routes := IdentityRoutes{admin: admin}
	rec := httptest.NewRecorder()
	routes.getSMTPSettings(rec, identityRequest(http.MethodGet, "/v1/identity/smtp-settings", "", string(model.TenantTypeCompany)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// TestGetSMTPSettingsReportsUnconfigured locks the visibility fix (issue
// #1168): the platform must be able to report "mail is not configured"
// through this surface, cleanly, rather than only via Zitadel's own
// unhandled 404.
func TestGetSMTPSettingsReportsUnconfigured(t *testing.T) {
	admin := &stubIdentityAdminClient{smtpStatus: zitadel.SMTPStatus{Configured: false}}
	routes := IdentityRoutes{admin: admin}
	rec := httptest.NewRecorder()
	routes.getSMTPSettings(rec, identityRequest(http.MethodGet, "/v1/identity/smtp-settings", "", string(model.TenantTypeOperations)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("\"configured\":false")) {
		t.Fatalf("body = %s, want configured:false reported", rec.Body.String())
	}
}

func TestUpdateSMTPSettingsRejectsMissingFields(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	routes := IdentityRoutes{admin: admin}
	rec := httptest.NewRecorder()
	routes.updateSMTPSettings(rec, identityRequest(http.MethodPatch, "/v1/identity/smtp-settings", `{"username":"erun"}`, string(model.TenantTypeOperations)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a missing host/senderAddress", rec.Code)
	}
}

func TestUpdateSMTPSettingsPassesDeclaredFields(t *testing.T) {
	admin := &stubIdentityAdminClient{smtpStatus: zitadel.SMTPStatus{Configured: true}}
	routes := IdentityRoutes{admin: admin}
	rec := httptest.NewRecorder()
	body := `{"host":"smtp.example.com:587","username":"erun","password":"s3cret","senderAddress":"noreply@example.com","senderName":"Erun Platform","tls":true}`
	routes.updateSMTPSettings(rec, identityRequest(http.MethodPatch, "/v1/identity/smtp-settings", body, string(model.TenantTypeOperations)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if admin.gotSMTPParams.Host != "smtp.example.com:587" || admin.gotSMTPParams.Password != "s3cret" || !admin.gotSMTPParams.TLS {
		t.Fatalf("gotSMTPParams = %+v, want the declared fields passed through", admin.gotSMTPParams)
	}
}

// An org is the per-tenant identity boundary an org-scoped issuer resolves
// by, so being unable to create one left tenant onboarding dependent on a
// hand-made org in Zitadel's own console (issue #1605).
func TestIdentityRoutesCreateOrg(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	routes := IdentityRoutes{admin: admin}
	rec := httptest.NewRecorder()

	routes.createOrg(rec, identityRequest(http.MethodPost, "/v1/identity/orgs", `{"name":"validationagent"}`, string(model.TenantTypeOperations)))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if admin.createdOrgName != "validationagent" {
		t.Fatalf("created org name = %q", admin.createdOrgName)
	}
	var org zitadel.Org
	if err := json.NewDecoder(rec.Body).Decode(&org); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if org.ID == "" {
		t.Fatal("the response must carry the org id: it is what an org-scoped mapping points at")
	}
}

func TestIdentityRoutesCreateOrgRequiresName(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	rec := httptest.NewRecorder()

	IdentityRoutes{admin: admin}.createOrg(rec, identityRequest(http.MethodPost, "/v1/identity/orgs", `{"name":"   "}`, string(model.TenantTypeOperations)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if admin.createdOrgName != "" {
		t.Fatal("a blank name must not reach the IdP")
	}
}

// Administering the platform's own IdP is not a company tenant's business,
// and creating orgs least of all.
func TestIdentityRoutesCreateOrgRequiresOperationsTenant(t *testing.T) {
	admin := &stubIdentityAdminClient{}
	rec := httptest.NewRecorder()

	IdentityRoutes{admin: admin}.createOrg(rec, identityRequest(http.MethodPost, "/v1/identity/orgs", `{"name":"acme"}`, string(model.TenantTypeCompany)))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if admin.createdOrgName != "" {
		t.Fatal("a company tenant must not reach the IdP")
	}
}
