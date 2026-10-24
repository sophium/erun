package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

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
