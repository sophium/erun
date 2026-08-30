package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type stubInviteRequestTenantCreator struct {
	tenant model.Tenant
	err    error
	got    repository.CreateTenantParams
	calls  int

	byNameTenant model.Tenant
	byNameErr    error
	gotByName    string
	byNameCalls  int
}

func (s *stubInviteRequestTenantCreator) Create(_ context.Context, params repository.CreateTenantParams) (model.Tenant, error) {
	s.got = params
	s.calls++
	return s.tenant, s.err
}

func (s *stubInviteRequestTenantCreator) GetByName(_ context.Context, name string) (model.Tenant, error) {
	s.gotByName = name
	s.byNameCalls++
	return s.byNameTenant, s.byNameErr
}

type stubInviteRequestUserEnroller struct {
	user               model.User
	err                error
	got                repository.CreateUserParams
	gotSecurityContext security.Context
	calls              int
}

func (s *stubInviteRequestUserEnroller) Create(ctx context.Context, params repository.CreateUserParams) (model.User, bool, error) {
	s.got = params
	s.calls++
	if sc, ok := security.FromContext(ctx); ok {
		s.gotSecurityContext = sc
	}
	return s.user, false, s.err
}

type stubInviteRequestInviteMinter struct {
	invite model.Invite
	err    error
	got    repository.CreateInviteParams
	calls  int
}

func (s *stubInviteRequestInviteMinter) Create(_ context.Context, params repository.CreateInviteParams) (model.Invite, error) {
	s.got = params
	s.calls++
	return s.invite, s.err
}

type stubInviteRequestDecisionStore struct {
	approved           model.InviteRequest
	declined           model.InviteRequest
	err                error
	gotApprovedID      string
	gotDecidedByUserID string
	gotMintedInviteID  string
	gotDeclineReason   string
	approveCalls       int
	declineCalls       int
}

func (s *stubInviteRequestDecisionStore) MarkApproved(_ context.Context, id string, decidedByUserID string, mintedInviteID string) (model.InviteRequest, error) {
	s.gotApprovedID = id
	s.gotDecidedByUserID = decidedByUserID
	s.gotMintedInviteID = mintedInviteID
	s.approveCalls++
	return s.approved, s.err
}

func (s *stubInviteRequestDecisionStore) MarkDeclined(_ context.Context, id string, decidedByUserID string, reason string) (model.InviteRequest, error) {
	s.gotApprovedID = id
	s.gotDecidedByUserID = decidedByUserID
	s.gotDeclineReason = reason
	s.declineCalls++
	return s.declined, s.err
}

func joinInviteRequest() model.InviteRequest {
	return model.InviteRequest{
		InviteRequestID: "req-1",
		Issuer:          "https://issuer.example.com",
		Subject:         "verified-subject",
		Email:           "requester@example.com",
		Kind:            model.InviteRequestKindJoinTenant,
		TenantName:      "acme",
		Status:          model.InviteRequestStatusPending,
	}
}

// TestApproveJoinEnrolsSubjectAndMintsInvite locks the issue's two required
// effects of approving a join request: the verified subject is enrolled
// into the caller's own (already-authorized) tenant, and an invite is minted
// for that same tenant through the existing repository.InviteRepository.Create
// path — never a reimplementation of invite minting.
func TestApproveJoinEnrolsSubjectAndMintsInvite(t *testing.T) {
	requests := &stubInviteRequestDecisionStore{approved: model.InviteRequest{InviteRequestID: "req-1", Status: model.InviteRequestStatusApproved}}
	users := &stubInviteRequestUserEnroller{user: model.User{UserID: "user-1"}}
	invites := &stubInviteRequestInviteMinter{invite: model.Invite{InviteID: "invite-1", Token: "tok"}}
	svc := NewInviteRequestService(requests, &stubInviteRequestTenantCreator{}, users, invites)

	ctx := security.WithContext(context.Background(), security.Context{TenantID: "tenant-a", TenantType: string(model.TenantTypeCompany)})
	if _, err := svc.ApproveJoin(ctx, joinInviteRequest(), "admin-user"); err != nil {
		t.Fatalf("ApproveJoin: %v", err)
	}

	if users.calls != 1 {
		t.Fatalf("UserRepository.Create called %d times, want 1", users.calls)
	}
	if users.got.Issuer != "https://issuer.example.com" || users.got.Subject != "verified-subject" {
		t.Fatalf("enrolled issuer/subject = %+v, want the request's own verified identity", users.got)
	}
	if users.gotSecurityContext.TenantID != "tenant-a" {
		t.Fatalf("enrollment ran under tenant %q, want the approving caller's own tenant %q", users.gotSecurityContext.TenantID, "tenant-a")
	}
	if invites.calls != 1 {
		t.Fatalf("InviteRepository.Create called %d times, want 1 (approval must mint through the existing invites path)", invites.calls)
	}
	if invites.got.Issuer != "https://issuer.example.com" {
		t.Fatalf("minted invite issuer = %q, want the request's own issuer", invites.got.Issuer)
	}
	if requests.gotMintedInviteID != "invite-1" {
		t.Fatalf("MarkApproved mintedInviteID = %q, want the minted invite's own id", requests.gotMintedInviteID)
	}
	if requests.gotDecidedByUserID != "admin-user" {
		t.Fatalf("MarkApproved decidedByUserID = %q, want the approving caller", requests.gotDecidedByUserID)
	}
}

// TestApproveCreateTenantRegistersTenantEnrolsFirstUserAndMintsInvite locks
// the CREATE_TENANT approval workflow: the tenant is registered with the
// requester's own verified issuer as its mapping, the requester is enrolled
// as that new tenant's first user (under a security context bound to the
// new tenant, not the approving operations caller's own), and an invite is
// minted for the new tenant.
func TestApproveCreateTenantRegistersTenantEnrolsFirstUserAndMintsInvite(t *testing.T) {
	requests := &stubInviteRequestDecisionStore{approved: model.InviteRequest{InviteRequestID: "req-1", Status: model.InviteRequestStatusApproved}}
	tenants := &stubInviteRequestTenantCreator{tenant: model.Tenant{TenantID: "tenant-new", Name: "newco", Type: model.TenantTypeCompany}}
	users := &stubInviteRequestUserEnroller{user: model.User{UserID: "user-1"}}
	invites := &stubInviteRequestInviteMinter{invite: model.Invite{InviteID: "invite-1", Token: "tok"}}
	svc := NewInviteRequestService(requests, tenants, users, invites)

	request := model.InviteRequest{
		InviteRequestID: "req-1",
		Issuer:          "https://newco-issuer.example.com",
		Subject:         "verified-subject",
		Kind:            model.InviteRequestKindCreateTenant,
		TenantName:      "newco",
		Status:          model.InviteRequestStatusPending,
	}
	ctx := security.WithContext(context.Background(), security.Context{TenantID: "ops-tenant", TenantType: string(model.TenantTypeOperations)})
	if _, err := svc.ApproveCreateTenant(ctx, request, "ops-user"); err != nil {
		t.Fatalf("ApproveCreateTenant: %v", err)
	}

	if tenants.calls != 1 {
		t.Fatalf("TenantRepository.Create called %d times, want 1", tenants.calls)
	}
	if tenants.got.Name != "newco" || tenants.got.Issuer != "https://newco-issuer.example.com" {
		t.Fatalf("tenant registered with %+v, want name %q mapped to the request's own verified issuer", tenants.got, "newco")
	}
	if users.calls != 1 {
		t.Fatalf("UserRepository.Create called %d times, want 1", users.calls)
	}
	if users.gotSecurityContext.TenantID != "tenant-new" {
		t.Fatalf("first-user enrollment ran under tenant %q, want the newly registered tenant %q", users.gotSecurityContext.TenantID, "tenant-new")
	}
	if invites.got.TenantID != "tenant-new" {
		t.Fatalf("minted invite tenant = %q, want the newly registered tenant", invites.got.TenantID)
	}
}

// TestApproveCreateTenantEnrolsIntoExistingTenantWhenNameConflicts is
// erun#1722's reproduction: the requested tenant name was registered
// out-of-band between the request being raised and decided (a race, not a
// mistake). Approving must not 500 on the resulting unique violation and
// must not leave the request PENDING with no reachable outcome — it enrols
// the requester into the tenant that already holds the name and completes
// the approval, landing on APPROVED, a state the operator can act on.
func TestApproveCreateTenantEnrolsIntoExistingTenantWhenNameConflicts(t *testing.T) {
	requests := &stubInviteRequestDecisionStore{approved: model.InviteRequest{InviteRequestID: "req-1", Status: model.InviteRequestStatusApproved}}
	tenants := &stubInviteRequestTenantCreator{
		err:          repository.ErrTenantNameConflict,
		byNameTenant: model.Tenant{TenantID: "tenant-existing", Name: "newco", Type: model.TenantTypeCompany},
	}
	users := &stubInviteRequestUserEnroller{user: model.User{UserID: "user-1"}}
	invites := &stubInviteRequestInviteMinter{invite: model.Invite{InviteID: "invite-1", Token: "tok"}}
	svc := NewInviteRequestService(requests, tenants, users, invites)

	request := model.InviteRequest{
		InviteRequestID: "req-1",
		Issuer:          "https://newco-issuer.example.com",
		Subject:         "verified-subject",
		Kind:            model.InviteRequestKindCreateTenant,
		TenantName:      "newco",
		Status:          model.InviteRequestStatusPending,
	}
	ctx := security.WithContext(context.Background(), security.Context{TenantID: "ops-tenant", TenantType: string(model.TenantTypeOperations)})
	decided, err := svc.ApproveCreateTenant(ctx, request, "ops-user")
	if err != nil {
		t.Fatalf("ApproveCreateTenant: %v", err)
	}

	if tenants.byNameCalls != 1 || tenants.gotByName != "newco" {
		t.Fatalf("GetByName called %d times with %q, want 1 call with %q", tenants.byNameCalls, tenants.gotByName, "newco")
	}
	if users.gotSecurityContext.TenantID != "tenant-existing" {
		t.Fatalf("enrollment ran under tenant %q, want the existing tenant %q", users.gotSecurityContext.TenantID, "tenant-existing")
	}
	if invites.got.TenantID != "tenant-existing" {
		t.Fatalf("minted invite tenant = %q, want the existing tenant", invites.got.TenantID)
	}
	if requests.approveCalls != 1 {
		t.Fatalf("MarkApproved called %d times, want 1 — the request must not stay PENDING", requests.approveCalls)
	}
	if decided.Status != model.InviteRequestStatusApproved {
		t.Fatalf("decided.Status = %q, want APPROVED", decided.Status)
	}
}

// TestApproveCreateTenantSurfacesUnrelatedTenantCreateFailure is the
// catch-all guard: only the specific name-conflict sentinel resolves to the
// existing tenant. Any other failure registering the tenant (a genuine
// database fault) must still fail the approval outright, leaving the
// request PENDING rather than silently marking it decided.
func TestApproveCreateTenantSurfacesUnrelatedTenantCreateFailure(t *testing.T) {
	requests := &stubInviteRequestDecisionStore{approved: model.InviteRequest{InviteRequestID: "req-1", Status: model.InviteRequestStatusApproved}}
	tenants := &stubInviteRequestTenantCreator{err: errors.New("connection reset by peer")}
	users := &stubInviteRequestUserEnroller{}
	invites := &stubInviteRequestInviteMinter{}
	svc := NewInviteRequestService(requests, tenants, users, invites)

	request := model.InviteRequest{
		InviteRequestID: "req-1",
		Issuer:          "https://newco-issuer.example.com",
		Subject:         "verified-subject",
		Kind:            model.InviteRequestKindCreateTenant,
		TenantName:      "newco",
		Status:          model.InviteRequestStatusPending,
	}
	ctx := security.WithContext(context.Background(), security.Context{TenantID: "ops-tenant", TenantType: string(model.TenantTypeOperations)})
	_, err := svc.ApproveCreateTenant(ctx, request, "ops-user")

	if err == nil {
		t.Fatal("ApproveCreateTenant: want an error for an unrelated tenant-create failure, got nil")
	}
	if tenants.byNameCalls != 0 {
		t.Fatalf("GetByName called %d times, want 0 — only ErrTenantNameConflict resolves this way", tenants.byNameCalls)
	}
	if users.calls != 0 || invites.calls != 0 || requests.approveCalls != 0 {
		t.Fatalf("enrollment/invite/approve ran (%d/%d/%d calls), want none once tenant registration fails for an unrelated reason", users.calls, invites.calls, requests.approveCalls)
	}
}
