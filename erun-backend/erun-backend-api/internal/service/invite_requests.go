package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// inviteRequestInviteTTL mirrors routes.inviteTTL (routes/invites.go): both
// mint invites through the same repository.InviteRepository.Create, and this
// package must not import routes (erun-backend-api/AGENTS.md's Layer
// Layout), so the value is duplicated rather than shared.
const inviteRequestInviteTTL = 7 * 24 * time.Hour

// InviteRequestTenantCreator registers a new tenant and its OIDC issuer
// mapping, or resolves the tenant a name race lost to. *repository.TenantRepository
// satisfies it.
type InviteRequestTenantCreator interface {
	Create(ctx context.Context, params repository.CreateTenantParams) (model.Tenant, error)
	// GetByName resolves Create's ErrTenantNameConflict: the tenant this call
	// did not create but that now holds the requested name.
	GetByName(ctx context.Context, name string) (model.Tenant, error)
}

// InviteRequestUserEnroller enrols a user into the tenant ctx's own
// security.Context names. *repository.UserRepository satisfies it.
type InviteRequestUserEnroller interface {
	Create(ctx context.Context, params repository.CreateUserParams) (model.User, error)
}

// InviteRequestInviteMinter mints an invite. *repository.InviteRepository satisfies it.
type InviteRequestInviteMinter interface {
	Create(ctx context.Context, params repository.CreateInviteParams) (model.Invite, error)
}

// InviteRequestDecisionStore records an invite request's approve/decline
// outcome. *repository.InviteRequestRepository satisfies it.
type InviteRequestDecisionStore interface {
	MarkApproved(ctx context.Context, id string, decidedByUserID string, mintedInviteID string) (model.InviteRequest, error)
	MarkDeclined(ctx context.Context, id string, decidedByUserID string, reason string) (model.InviteRequest, error)
}

// ErrInviteRequestWrongKind reports that the caller tried to approve a
// request through the workflow for the other kind (e.g. ApproveJoin called
// on a CREATE_TENANT request).
var ErrInviteRequestWrongKind = errors.New("invite request kind does not match this approval workflow")

// InviteRequestService owns the invite-request decision workflow: approving
// coordinates enrolling the requester (and, for a CREATE_TENANT request,
// registering the tenant first) with minting the invite that carries expiry,
// single-use, and revocation for free through the existing POST /v1/invites
// path — systems that must agree on the outcome, the same shape
// InviteService.Accept already coordinates for invite acceptance.
type InviteRequestService struct {
	requests InviteRequestDecisionStore
	tenants  InviteRequestTenantCreator
	users    InviteRequestUserEnroller
	invites  InviteRequestInviteMinter
}

func NewInviteRequestService(requests InviteRequestDecisionStore, tenants InviteRequestTenantCreator, users InviteRequestUserEnroller, invites InviteRequestInviteMinter) *InviteRequestService {
	return &InviteRequestService{requests: requests, tenants: tenants, users: users, invites: invites}
}

// ApproveJoin enrols request's verified subject into the tenant ctx's own
// security.Context already names, then mints an invite for that same
// tenant. The caller (a route) must already have verified request.TenantName
// names the caller's own tenant — the "authority over that tenant" the issue
// requires — before calling this; ApproveJoin itself trusts ctx's tenant.
func (s *InviteRequestService) ApproveJoin(ctx context.Context, request model.InviteRequest, decidedByUserID string) (model.InviteRequest, error) {
	if request.Kind != model.InviteRequestKindJoinTenant {
		return model.InviteRequest{}, ErrInviteRequestWrongKind
	}
	if _, err := s.users.Create(ctx, repository.CreateUserParams{
		Username: enrollmentUsername(request),
		Issuer:   request.Issuer,
		Subject:  request.Subject,
	}); err != nil {
		return model.InviteRequest{}, fmt.Errorf("enrol requester: %w", err)
	}
	invite, err := s.invites.Create(ctx, repository.CreateInviteParams{
		Issuer: request.Issuer,
		Email:  request.Email,
		TTL:    inviteRequestInviteTTL,
	})
	if err != nil {
		return model.InviteRequest{}, fmt.Errorf("mint invite: %w", err)
	}
	return s.requests.MarkApproved(ctx, request.InviteRequestID, decidedByUserID, invite.InviteID)
}

// ApproveCreateTenant registers a new tenant with request's verified issuer
// as its mapping, enrols the requester as its first user, and mints an
// invite for that tenant. The caller (a route) must already have verified
// the caller is an OPERATIONS tenant before calling this — registering a
// tenant is operations-only regardless of who asks for it.
//
// A CREATE_TENANT request can be overtaken by its own chosen name between
// being raised and being decided — someone else registers the same name
// first, or (per the "does not remain PENDING" requirement below) an earlier
// approve attempt on this very request created the tenant and then failed a
// later step. Either way the tenant existing is a race, not a mistake
// (erun#1722): enrolling the requester into it is what CREATE_TENANT actually
// asks for, so Create's ErrTenantNameConflict resolves to that tenant instead
// of failing the approval. This is also what makes the overall workflow
// retry-safe without a spanning database transaction (see the package doc
// comment): resolving-or-creating the tenant is now idempotent, so retrying a
// partially-failed approve (e.g. tenant registered, enrollment then failed)
// converges to success instead of repeating the same fatal error.
func (s *InviteRequestService) ApproveCreateTenant(ctx context.Context, request model.InviteRequest, decidedByUserID string) (model.InviteRequest, error) {
	if request.Kind != model.InviteRequestKindCreateTenant {
		return model.InviteRequest{}, ErrInviteRequestWrongKind
	}
	tenant, err := s.tenants.Create(ctx, repository.CreateTenantParams{
		Name:   request.TenantName,
		Type:   model.TenantTypeCompany,
		Issuer: request.Issuer,
	})
	switch {
	case err == nil:
		// registered fresh; fall through to enrollment below.
	case errors.Is(err, repository.ErrTenantNameConflict):
		tenant, err = s.tenants.GetByName(ctx, request.TenantName)
		if err != nil {
			return model.InviteRequest{}, fmt.Errorf("resolve existing tenant %q: %w", request.TenantName, err)
		}
	default:
		return model.InviteRequest{}, fmt.Errorf("register tenant: %w", err)
	}

	// The new tenant is not the operations caller's own tenant, so its first
	// user is enrolled under a synthetic security context bound to it — the
	// same "resolve tenant, then bind its own security context" shape
	// IdentityRepository's per-tenant first-user bootstrap already uses for
	// an equally caller-less enrollment. tenant.Type (not a hardcoded
	// COMPANY) matters once tenant may be one Create didn't just mint.
	tenantCtx := security.WithContext(ctx, security.Context{
		TenantID:   tenant.TenantID,
		TenantType: string(tenant.Type),
	})
	if _, err := s.users.Create(tenantCtx, repository.CreateUserParams{
		Username: enrollmentUsername(request),
		Issuer:   request.Issuer,
		Subject:  request.Subject,
	}); err != nil {
		return model.InviteRequest{}, fmt.Errorf("enrol requester as first user: %w", err)
	}

	// Minting for the new tenant is a cross-tenant write from the operations
	// caller's own session, the same shape invites.go's resolveTargetTenant
	// already uses for an operations caller inviting into a tenant it is not
	// itself a member of — TenantID is passed explicitly so this does not
	// fall back to the operations tenant itself.
	invite, err := s.invites.Create(ctx, repository.CreateInviteParams{
		TenantID: tenant.TenantID,
		Issuer:   request.Issuer,
		Email:    request.Email,
		TTL:      inviteRequestInviteTTL,
	})
	if err != nil {
		return model.InviteRequest{}, fmt.Errorf("mint invite: %w", err)
	}
	return s.requests.MarkApproved(ctx, request.InviteRequestID, decidedByUserID, invite.InviteID)
}

// Decline transitions a pending request to DECLINED with reason. The schema
// itself refuses an empty one; this trims but does not otherwise validate,
// leaving that refusal as the single source of truth.
func (s *InviteRequestService) Decline(ctx context.Context, request model.InviteRequest, decidedByUserID string, reason string) (model.InviteRequest, error) {
	return s.requests.MarkDeclined(ctx, request.InviteRequestID, decidedByUserID, strings.TrimSpace(reason))
}

// enrollmentUsername prefers the requester's own display name, then their
// email, then falls back to their verified subject — mirroring
// identity.go's bootstrapUsername fallback so an enrolled user always has a
// display name even when the request carried neither.
func enrollmentUsername(request model.InviteRequest) string {
	if name := strings.TrimSpace(request.DisplayName); name != "" {
		return name
	}
	if email := strings.TrimSpace(request.Email); email != "" {
		return email
	}
	return request.Subject
}
