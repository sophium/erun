package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type stubMachineIdentityProvisioner struct {
	calls int
	gotID string
	err   error
	// already marks a repeat provisioning.
	already bool
}

func (s *stubMachineIdentityProvisioner) Provision(_ context.Context, environmentID string) (service.MachineIdentityResult, error) {
	s.calls++
	s.gotID = environmentID
	if s.err != nil {
		return service.MachineIdentityResult{}, s.err
	}
	return service.MachineIdentityResult{
		EnvironmentID:   environmentID,
		EnvironmentName: "alpha",
		Issuer:          "https://auth.example",
		Subject:         "client-env-1",
		ClientID:        "client-env-1",
		ClientSecret:    "secret-env-1",
		UserID:          "user-env-1",
		AlreadyEnrolled: s.already,
	}, nil
}

func postMachineIdentity(t *testing.T, provisioner MachineIdentityProvisioner, environmentID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/environments/"+environmentID+"/machine-identity", nil)
	req.SetPathValue("environment_id", environmentID)
	req = req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:   "tenant-1",
		TenantType: "COMPANY",
		ErunUserID: "caller-user",
	}))
	rec := httptest.NewRecorder()
	MachineIdentityRoutes{identities: provisioner}.provisionMachineIdentity(rec, req)
	return rec
}

func TestProvisionMachineIdentityReturnsTheCredentialForANewIdentity(t *testing.T) {
	provisioner := &stubMachineIdentityProvisioner{}
	rec := postMachineIdentity(t, provisioner, "env-1")

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if provisioner.gotID != "env-1" {
		t.Fatalf("provisioner was asked for %q, want the path's environment id", provisioner.gotID)
	}
	var body machineIdentityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ClientID != "client-env-1" || body.ClientSecret != "secret-env-1" {
		t.Fatalf("response carried no usable credential: %+v", body)
	}
	if body.Subject == "" || body.Issuer == "" || body.UserID == "" {
		t.Fatalf("response did not report what the identity resolves as: %+v", body)
	}
}

func TestProvisionMachineIdentityReportsAnExistingIdentityAsAlreadyEnrolled(t *testing.T) {
	provisioner := &stubMachineIdentityProvisioner{already: true}
	rec := postMachineIdentity(t, provisioner, "env-1")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an identity that already existed (body %s)", rec.Code, rec.Body.String())
	}
	var body machineIdentityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.AlreadyEnrolled {
		t.Fatalf("expected alreadyEnrolled in the response, got %+v", body)
	}
}

// 501, not 404, so a deployment gap is distinguishable from a typo.
func TestProvisionMachineIdentityRefusesAnUnconfiguredControlPlane(t *testing.T) {
	rec := postMachineIdentity(t, stubMachineIdentityUnconfigured{}, "env-1")

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (body %s)", rec.Code, rec.Body.String())
	}
	var body errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Code != "MACHINE_IDENTITY_UNCONFIGURED" {
		t.Fatalf("code = %q, want MACHINE_IDENTITY_UNCONFIGURED", body.Code)
	}
}

type stubMachineIdentityUnconfigured struct{}

func (stubMachineIdentityUnconfigured) Provision(context.Context, string) (service.MachineIdentityResult, error) {
	return service.MachineIdentityResult{}, service.ErrMachineIdentityProviderUnavailable
}

func TestProvisionMachineIdentityRefusesATenantItCannotServeAsAConflict(t *testing.T) {
	provisioner := &stubMachineIdentityProvisioner{
		err: fmt.Errorf("%w: every issuer it resolves by is single-tenant", service.ErrMachineIdentityUnavailable),
	}
	rec := postMachineIdentity(t, provisioner, "env-1")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	var body errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Code != "MACHINE_IDENTITY_UNAVAILABLE" {
		t.Fatalf("code = %q, want MACHINE_IDENTITY_UNAVAILABLE", body.Code)
	}
}

func TestProvisionMachineIdentityReportsAnUnknownEnvironmentAsNotFound(t *testing.T) {
	provisioner := &stubMachineIdentityProvisioner{err: repository.ErrNotFound}
	rec := postMachineIdentity(t, provisioner, "env-missing")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if provisioner.calls != 1 {
		t.Fatalf("expected the provisioner to be asked once, got %d", provisioner.calls)
	}
}
