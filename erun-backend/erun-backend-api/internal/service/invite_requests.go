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

// inviteRequestInviteTTL duplicates routes.inviteTTL because service must not import routes.
const inviteRequestInviteTTL = 7 * 24 * time.Hour

// InviteRequestTenantCreator registers a tenant and its issuer mapping.
type InviteRequestTenantCreator interface {
	Create(ctx context.Context, params repository.CreateTenantParams) (model.Tenant, error)
	// GetByName resolves the tenant that won a Create name race.
	GetByName(ctx context.Context, name string) (model.Tenant, error)
}

// InviteRequestUserEnroller enrols a user into the tenant named by ctx's security.Context.
type InviteRequestUserEnroller interface {
	Create(ctx context.Context, params repository.CreateUserParams) (model.User, bool, error)
}

// InviteRequestInviteMinter mints an invite.
type InviteRequestInviteMinter interface {
	Create(ctx context.Context, params repository.CreateInviteParams) (model.Invite, error)
}

// InviteRequestDecisionStore records an invite request's approve/decline outcome.
type InviteRequestDecisionStore interface {
	MarkApproved(ctx context.Context, id string, decidedByUserID string, mintedInviteID string) (model.InviteRequest, error)
	MarkDeclined(ctx context.Context, id string, decidedByUserID string, reason string) (model.InviteRequest, error)
}

// ErrInviteRequestWrongKind reports approval through the other kind's workflow.
var ErrInviteRequestWrongKind = errors.New("invite request kind does not match this approval workflow")

// InviteRequestService approves or declines invite requests, enrolling the requester and minting the invite.
type InviteRequestService struct {
	requests InviteRequestDecisionStore
	tenants  InviteRequestTenantCreator
	users    InviteRequestUserEnroller
	invites  InviteRequestInviteMinter
}

func NewInviteRequestService(requests InviteRequestDecisionStore, tenants InviteRequestTenantCreator, users InviteRequestUserEnroller, invites InviteRequestInviteMinter) *InviteRequestService {
	return &InviteRequestService{requests: requests, tenants: tenants, users: users, invites: invites}
}

// ApproveJoin trusts ctx's tenant; the route must already have checked request.TenantName against it.
func (s *InviteRequestService) ApproveJoin(ctx context.Context, request model.InviteRequest, decidedByUserID string) (model.InviteRequest, error) {
	if request.Kind != model.InviteRequestKindJoinTenant {
		return model.InviteRequest{}, ErrInviteRequestWrongKind
	}
	if _, _, err := s.users.Create(ctx, repository.CreateUserParams{
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

// ApproveCreateTenant registers the tenant, enrols the requester and mints an invite; the route must already
// have required an OPERATIONS caller. A name conflict enrols into the existing tenant, which keeps a retried
// partial approve idempotent.
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
	case errors.Is(err, repository.ErrTenantNameConflict):
		tenant, err = s.tenants.GetByName(ctx, request.TenantName)
		if err != nil {
			return model.InviteRequest{}, fmt.Errorf("resolve existing tenant %q: %w", request.TenantName, err)
		}
	default:
		return model.InviteRequest{}, fmt.Errorf("register tenant: %w", err)
	}

	// Bound to the new tenant, not the caller's; tenant.Type matters when the tenant already existed.
	tenantCtx := security.WithContext(ctx, security.Context{
		TenantID:   tenant.TenantID,
		TenantType: string(tenant.Type),
	})
	if _, _, err := s.users.Create(tenantCtx, repository.CreateUserParams{
		Username: enrollmentUsername(request),
		Issuer:   request.Issuer,
		Subject:  request.Subject,
	}); err != nil {
		return model.InviteRequest{}, fmt.Errorf("enrol requester as first user: %w", err)
	}

	// TenantID is explicit so the invite does not fall back to the operations tenant.
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

// Decline leaves reason validation to the schema.
func (s *InviteRequestService) Decline(ctx context.Context, request model.InviteRequest, decidedByUserID string, reason string) (model.InviteRequest, error) {
	return s.requests.MarkDeclined(ctx, request.InviteRequestID, decidedByUserID, strings.TrimSpace(reason))
}

func enrollmentUsername(request model.InviteRequest) string {
	if name := strings.TrimSpace(request.DisplayName); name != "" {
		return name
	}
	if email := strings.TrimSpace(request.Email); email != "" {
		return email
	}
	return request.Subject
}
