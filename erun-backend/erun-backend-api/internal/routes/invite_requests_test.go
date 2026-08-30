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

type stubInviteRequestSubmitter struct {
	submitted     model.InviteRequest
	submitErr     error
	gotSubmit     repository.SubmitInviteRequestParams
	byIdentity    model.InviteRequest
	byIdentityErr error
}

func (s *stubInviteRequestSubmitter) Submit(_ context.Context, params repository.SubmitInviteRequestParams) (model.InviteRequest, error) {
	s.gotSubmit = params
	return s.submitted, s.submitErr
}

func (s *stubInviteRequestSubmitter) GetByIdentity(_ context.Context, _ string, _ string) (model.InviteRequest, error) {
	return s.byIdentity, s.byIdentityErr
}

type stubInviteRequestTokenVerifier struct {
	claims security.Claims
	err    error
}

func (s stubInviteRequestTokenVerifier) VerifyBearerToken(_ context.Context, _ string) (security.Claims, error) {
	return s.claims, s.err
}

type stubInviteRequestWindowReader struct {
	windowSeconds int
}

func (s stubInviteRequestWindowReader) Get(_ context.Context) (model.PlatformRateLimit, error) {
	return model.PlatformRateLimit{InviteRequestWindowSeconds: s.windowSeconds}, nil
}

func submitInviteRequestHTTPRequest(body string, bearer string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/invite-requests", bytes.NewBufferString(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return req
}

// TestSubmitInviteRequestRecordsVerifiedIdentityNotBody locks the issue's
// load-bearing invariant: the issuer/subject a request records come from the
// verified bearer token, never anything the caller's body claims. The body
// here deliberately cannot even carry issuer/subject fields
// (submitInviteRequestBody has none), so this proves the recorded identity
// is exactly the token's own claims by construction, not by omission.
func TestSubmitInviteRequestRecordsVerifiedIdentityNotBody(t *testing.T) {
	submitter := &stubInviteRequestSubmitter{submitted: model.InviteRequest{InviteRequestID: "req-1", Status: model.InviteRequestStatusPending}}
	verifier := stubInviteRequestTokenVerifier{claims: security.Claims{Issuer: "https://issuer.example.com", Subject: "verified-subject"}}
	limiter := NewInviteRequestRateLimiter(stubInviteRequestWindowReader{windowSeconds: 60})
	mux := http.NewServeMux()
	RegisterInviteRequestPublicRoutes(mux, verifier, submitter, limiter)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, submitInviteRequestHTTPRequest(`{"kind":"JOIN_TENANT","tenantName":"acme"}`, "tok"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if submitter.gotSubmit.Issuer != "https://issuer.example.com" {
		t.Fatalf("Submit params.Issuer = %q, want the verified token's own issuer", submitter.gotSubmit.Issuer)
	}
	if submitter.gotSubmit.Subject != "verified-subject" {
		t.Fatalf("Submit params.Subject = %q, want the verified token's own subject", submitter.gotSubmit.Subject)
	}
}

// TestSubmitInviteRequestRejectsInvalidBearerToken proves an unverifiable
// caller never reaches Submit at all — the endpoint verifies a real bearer
// token, it does not skip verification entirely just because it tolerates
// "not enrolled anywhere".
func TestSubmitInviteRequestRejectsInvalidBearerToken(t *testing.T) {
	submitter := &stubInviteRequestSubmitter{}
	verifier := stubInviteRequestTokenVerifier{err: context.DeadlineExceeded}
	limiter := NewInviteRequestRateLimiter(stubInviteRequestWindowReader{windowSeconds: 60})
	mux := http.NewServeMux()
	RegisterInviteRequestPublicRoutes(mux, verifier, submitter, limiter)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, submitInviteRequestHTTPRequest(`{"kind":"JOIN_TENANT","tenantName":"acme"}`, "bad-tok"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if submitter.gotSubmit != (repository.SubmitInviteRequestParams{}) {
		t.Fatalf("Submit was called with %+v, want it never called for an invalid token", submitter.gotSubmit)
	}
}

type stubInviteRequestStore struct {
	got    model.InviteRequest
	getErr error
}

func (s *stubInviteRequestStore) Get(_ context.Context, _ string) (model.InviteRequest, error) {
	return s.got, s.getErr
}

func (s *stubInviteRequestStore) List(_ context.Context, _ repository.InviteRequestFilter) ([]model.InviteRequest, error) {
	return nil, nil
}

type stubInviteRequestApprover struct {
	decided      model.InviteRequest
	decideErr    error
	gotReason    string
	approveCalls int
	declineCalls int
}

func (s *stubInviteRequestApprover) ApproveJoin(_ context.Context, _ model.InviteRequest, _ string) (model.InviteRequest, error) {
	s.approveCalls++
	return s.decided, s.decideErr
}

func (s *stubInviteRequestApprover) ApproveCreateTenant(_ context.Context, _ model.InviteRequest, _ string) (model.InviteRequest, error) {
	s.approveCalls++
	return s.decided, s.decideErr
}

func (s *stubInviteRequestApprover) Decline(_ context.Context, _ model.InviteRequest, _ string, reason string) (model.InviteRequest, error) {
	s.declineCalls++
	s.gotReason = reason
	return s.decided, s.decideErr
}

// TestDeclineInviteRequestRequiresReason locks the issue's "a decline with no
// reason is a dead end" rule at the API boundary: an empty reason is
// refused before the service (and the schema's own CHECK constraint behind
// it) is ever reached.
func TestDeclineInviteRequestRequiresReason(t *testing.T) {
	store := &stubInviteRequestStore{got: model.InviteRequest{
		InviteRequestID: "req-1",
		Status:          model.InviteRequestStatusPending,
		Kind:            model.InviteRequestKindJoinTenant,
		TenantName:      "acme",
	}}
	tenants := &stubTenantRepository{current: model.Tenant{Name: "acme"}}
	approver := &stubInviteRequestApprover{}
	routes := inviteRequestRoutes{requests: store, tenants: tenants, decisions: approver}

	req := inviteRequest(http.MethodPost, "/v1/invite-requests/req-1/decline", `{}`, "tenant-a", string(model.TenantTypeCompany), "https://auth.example.com")
	req.SetPathValue("invite_request_id", "req-1")
	rec := httptest.NewRecorder()
	routes.decline(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a missing reason; body=%s", rec.Code, rec.Body.String())
	}
	if approver.declineCalls != 0 {
		t.Fatalf("Decline was called %d times, want 0 for a missing reason", approver.declineCalls)
	}

	rec = httptest.NewRecorder()
	req2 := inviteRequest(http.MethodPost, "/v1/invite-requests/req-1/decline", `{"reason":"no capacity"}`, "tenant-a", string(model.TenantTypeCompany), "https://auth.example.com")
	req2.SetPathValue("invite_request_id", "req-1")
	routes.decline(rec, req2)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a real reason; body=%s", rec.Code, rec.Body.String())
	}
	if approver.gotReason != "no capacity" {
		t.Fatalf("Decline reason = %q, want %q", approver.gotReason, "no capacity")
	}
}

// TestApproveCreateTenantRequiresOperationsTenant locks the issue's
// authority split: approving a CREATE_TENANT request needs an operations
// tenant even though the same route serves JOIN_TENANT approvals for an
// ordinary tenant admin.
func TestApproveCreateTenantRequiresOperationsTenant(t *testing.T) {
	store := &stubInviteRequestStore{got: model.InviteRequest{
		InviteRequestID: "req-1",
		Status:          model.InviteRequestStatusPending,
		Kind:            model.InviteRequestKindCreateTenant,
		TenantName:      "newco",
	}}
	tenants := &stubTenantRepository{current: model.Tenant{Name: "acme"}}
	approver := &stubInviteRequestApprover{}
	routes := inviteRequestRoutes{requests: store, tenants: tenants, decisions: approver}

	req := inviteRequest(http.MethodPost, "/v1/invite-requests/req-1/approve", `{}`, "tenant-a", string(model.TenantTypeCompany), "https://auth.example.com")
	req.SetPathValue("invite_request_id", "req-1")
	rec := httptest.NewRecorder()
	routes.approve(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-operations caller; body=%s", rec.Code, rec.Body.String())
	}
	if approver.approveCalls != 0 {
		t.Fatalf("approve workflow was called %d times, want 0 without operations authority", approver.approveCalls)
	}

	rec = httptest.NewRecorder()
	req2 := inviteRequest(http.MethodPost, "/v1/invite-requests/req-1/approve", `{}`, "ops-tenant", string(model.TenantTypeOperations), "https://auth.example.com")
	req2.SetPathValue("invite_request_id", "req-1")
	routes.approve(rec, req2)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an operations caller; body=%s", rec.Code, rec.Body.String())
	}
	if approver.approveCalls != 1 {
		t.Fatalf("approve workflow was called %d times, want 1 for an operations caller", approver.approveCalls)
	}
}
