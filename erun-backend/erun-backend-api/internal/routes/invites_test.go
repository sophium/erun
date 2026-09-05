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

type stubInviteStore struct {
	created    model.Invite
	createErr  error
	gotCreate  repository.CreateInviteParams
	listed     []model.Invite
	listErr    error
	gotFilter  repository.InviteFilter
	revokeErr  error
	gotRevoked string
}

func (s *stubInviteStore) Create(_ context.Context, params repository.CreateInviteParams) (model.Invite, error) {
	s.gotCreate = params
	return s.created, s.createErr
}

func (s *stubInviteStore) List(_ context.Context, filter repository.InviteFilter) ([]model.Invite, error) {
	s.gotFilter = filter
	return s.listed, s.listErr
}

func (s *stubInviteStore) Revoke(_ context.Context, inviteID string) error {
	s.gotRevoked = inviteID
	return s.revokeErr
}

func inviteRequest(method, path, body, tenantID, tenantType, issuer string) *http.Request {
	reader := bytes.NewBufferString(body)
	req := httptest.NewRequest(method, path, reader)
	return req.WithContext(security.WithContext(req.Context(), security.Context{
		TenantID:       tenantID,
		TenantType:     tenantType,
		ErunUserID:     "caller-user",
		ExternalIssuer: issuer,
	}))
}

func TestCreateInviteForOwnTenantIsAllowedForAnyTenantType(t *testing.T) {
	store := &stubInviteStore{created: model.Invite{InviteID: "invite-1", TenantID: "tenant-a", Token: "tok"}}
	routes := InviteRoutes{invites: store}
	rec := httptest.NewRecorder()
	routes.createInvite(rec, inviteRequest(http.MethodPost, "/v1/invites", `{"email":"new@example.com"}`, "tenant-a", string(model.TenantTypeCompany), "https://auth.example.com"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if store.gotCreate.TenantID != "" {
		t.Fatalf("TenantID override = %q, want empty for the caller's own tenant", store.gotCreate.TenantID)
	}
	if store.gotCreate.Issuer != "https://auth.example.com" {
		t.Fatalf("Issuer = %q, want the caller's own authenticated issuer", store.gotCreate.Issuer)
	}
}

// TestCreateInviteForAnotherTenantRequiresOperations locks #1483 item 4's
// coarse gate: a COMPANY-tenant caller cannot mint an invite for a tenant
// other than their own.
func TestCreateInviteForAnotherTenantRequiresOperations(t *testing.T) {
	store := &stubInviteStore{}
	routes := InviteRoutes{invites: store}
	rec := httptest.NewRecorder()
	routes.createInvite(rec, inviteRequest(http.MethodPost, "/v1/invites", `{"tenantId":"tenant-b"}`, "tenant-a", string(model.TenantTypeCompany), "https://auth.example.com"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestCreateInviteForAnotherTenantSucceedsForOperations(t *testing.T) {
	store := &stubInviteStore{created: model.Invite{InviteID: "invite-2", TenantID: "tenant-b"}}
	routes := InviteRoutes{invites: store}
	rec := httptest.NewRecorder()
	routes.createInvite(rec, inviteRequest(http.MethodPost, "/v1/invites", `{"tenantId":"tenant-b"}`, "tenant-ops", string(model.TenantTypeOperations), "https://auth.example.com"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if store.gotCreate.TenantID != "tenant-b" {
		t.Fatalf("TenantID override = %q, want tenant-b", store.gotCreate.TenantID)
	}
}

func TestListInvitesScopesToTheResolvedTenant(t *testing.T) {
	store := &stubInviteStore{listed: []model.Invite{{InviteID: "invite-1"}}}
	routes := InviteRoutes{invites: store}
	rec := httptest.NewRecorder()
	routes.listInvites(rec, inviteRequest(http.MethodGet, "/v1/invites", "", "tenant-a", string(model.TenantTypeCompany), "https://auth.example.com"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if store.gotFilter.TenantID != "tenant-a" {
		t.Fatalf("TenantID filter = %q, want the caller's own tenant", store.gotFilter.TenantID)
	}
}

func TestRevokeInviteNotFound(t *testing.T) {
	store := &stubInviteStore{revokeErr: repository.ErrNotFound}
	routes := InviteRoutes{invites: store}
	req := inviteRequest(http.MethodDelete, "/v1/invites/invite-1", "", "tenant-a", string(model.TenantTypeCompany), "https://auth.example.com")
	req.SetPathValue("invite_id", "invite-1")
	rec := httptest.NewRecorder()
	routes.revokeInvite(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if store.gotRevoked != "invite-1" {
		t.Fatalf("gotRevoked = %q, want invite-1", store.gotRevoked)
	}
}

type stubInviteAccepter struct {
	result service.AcceptInviteResult
	err    error
}

func (s *stubInviteAccepter) Accept(context.Context, service.AcceptInviteParams) (service.AcceptInviteResult, error) {
	return s.result, s.err
}

func acceptRequest(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/v1/invites/accept", bytes.NewBufferString(body))
}

func TestInviteAcceptRequiresTokenUsernameAndPassword(t *testing.T) {
	mux := http.NewServeMux()
	RegisterInviteAcceptRoute(mux, &stubInviteAccepter{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, acceptRequest(`{"username":"someone","password":"pw"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a missing token; body=%s", rec.Code, rec.Body.String())
	}
}

func TestInviteAcceptSucceeds(t *testing.T) {
	accepter := &stubInviteAccepter{result: service.AcceptInviteResult{
		IdPUser:  zitadel.User{ID: "idp-1", Username: "newbie"},
		ErunUser: model.User{UserID: "erun-1", Username: "newbie"},
	}}
	mux := http.NewServeMux()
	RegisterInviteAcceptRoute(mux, accepter)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, acceptRequest(`{"token":"tok","username":"newbie","password":"S3cret!Pass"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp acceptInviteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ErunUser == nil || resp.ErunUser.UserID != "erun-1" {
		t.Fatalf("resp.ErunUser = %+v, want erun-1", resp.ErunUser)
	}
}

// TestInviteAcceptReportsEachTokenStatePlainly locks #1483 item 7: a stale
// or revoked link says exactly why, rather than a generic failure.
func TestInviteAcceptReportsEachTokenStatePlainly(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"unknown token", repository.ErrNotFound, http.StatusNotFound},
		{"expired", repository.ErrInviteExpired, http.StatusGone},
		{"already consumed", repository.ErrInviteConsumed, http.StatusGone},
		{"email mismatch", service.ErrInviteEmailMismatch, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			RegisterInviteAcceptRoute(mux, &stubInviteAccepter{err: tc.err})
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, acceptRequest(`{"token":"tok","username":"newbie","password":"S3cret!Pass"}`))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d for %s; body=%s", rec.Code, tc.want, tc.name, rec.Body.String())
			}
		})
	}
}

func TestInviteAcceptReportsHalfLandedFailure(t *testing.T) {
	accepter := &stubInviteAccepter{
		result: service.AcceptInviteResult{IdPUser: zitadel.User{ID: "idp-9", Username: "orphan"}},
		err:    service.ErrIdentityMappingFailed,
	}
	mux := http.NewServeMux()
	RegisterInviteAcceptRoute(mux, accepter)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, acceptRequest(`{"token":"tok","username":"orphan","password":"S3cret!Pass"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (IdP half landed); body=%s", rec.Code, rec.Body.String())
	}
	var resp acceptInviteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == "" || resp.ErunUser != nil || resp.IdPUser.ID != "idp-9" {
		t.Fatalf("resp = %+v, want the orphaned IdP identity reported with an error and no erunUser", resp)
	}
}
