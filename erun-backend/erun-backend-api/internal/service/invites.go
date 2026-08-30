package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

// InviteConsumer resolves and consumes an invite token.
// *repository.InviteRepository satisfies it.
type InviteConsumer interface {
	ConsumeByToken(ctx context.Context, token string) (repository.ConsumedInvite, error)
}

// InviteZitadelAdmin is the Zitadel surface AcceptInvite needs. *zitadel.Client satisfies it.
type InviteZitadelAdmin interface {
	CreateHumanUser(ctx context.Context, params zitadel.CreateHumanUserParams) (zitadel.User, error)
}

// ErrInviteEmailMismatch reports that the invite pinned a specific email and
// the accepting request supplied a different one.
var ErrInviteEmailMismatch = errors.New("invite is pinned to a different email address")

// InviteService coordinates invite acceptance: consuming the token, creating
// the IdP identity, and creating the erun user mapping are three separate
// systems that must agree on the outcome, the same shape IdentityService
// already coordinates for admin-composed enrollment.
type InviteService struct {
	invites InviteConsumer
	admin   InviteZitadelAdmin
	users   IdentityUserCreator
}

func NewInviteService(invites InviteConsumer, admin InviteZitadelAdmin, users IdentityUserCreator) *InviteService {
	return &InviteService{invites: invites, admin: admin, users: users}
}

// AcceptInviteParams is the accept-flow input. Unauthenticated — the token
// itself is the only credential the caller presents — so every other field
// is supplied by the invitee.
type AcceptInviteParams struct {
	Token     string
	Username  string
	Email     string
	FirstName string
	LastName  string
	Password  string
}

// AcceptInviteResult reports what actually landed, mirroring
// EnrollIdentityResult's half-landed-failure shape: ErunUser is the zero
// value when the erun-side mapping failed after the IdP user was created,
// and IdPUser is still populated so the caller can report the orphaned IdP
// identity precisely.
type AcceptInviteResult struct {
	IdPUser  zitadel.User
	ErunUser model.User
}

// Accept consumes an invite token and lands the invitee as an enrolled erun
// user of the invite's target tenant.
//
// Unlike Enroll's admin-composed path — which never sets a caller-supplied
// password, so no admin ever handles a credential belonging to someone
// else's account (see zitadel.CreateHumanUserParams' own doc) — the
// invitee is present and choosing their own password right now: there is no
// "someone else's account" to protect them from, and Zitadel's usual
// email-invite flow would be the wrong choice regardless, since that link
// has nothing to do with the credential just supplied. So this always sets
// InitialPassword, which also marks the email verified and lands
// USER_STATE_ACTIVE immediately — the same combination CreateHumanUser
// already uses for the no-SMTP fallback, reused here for a different
// reason (a present invitee, not absent mail).
func (s *InviteService) Accept(ctx context.Context, params AcceptInviteParams) (AcceptInviteResult, error) {
	consumed, err := s.invites.ConsumeByToken(ctx, params.Token)
	if err != nil {
		return AcceptInviteResult{}, err
	}
	invite := consumed.Invite

	email := strings.TrimSpace(params.Email)
	switch {
	case invite.Email == "":
		// no pinned email; use whatever the invitee supplied
	case email == "":
		email = invite.Email
	case !strings.EqualFold(email, invite.Email):
		return AcceptInviteResult{}, ErrInviteEmailMismatch
	}

	idpUser, err := s.admin.CreateHumanUser(ctx, zitadel.CreateHumanUserParams{
		Username:        params.Username,
		Email:           email,
		FirstName:       params.FirstName,
		LastName:        params.LastName,
		InitialPassword: params.Password,
	})
	if err != nil {
		return AcceptInviteResult{}, fmt.Errorf("create identity provider user: %w", err)
	}

	// The invite's target tenant is not the ctx caller's tenant (there is no
	// caller) — it is a synthetic security context built from what
	// ConsumeByToken resolved, the same "resolve tenant, then bind its own
	// security context" shape IdentityRepository's per-tenant first-user
	// bootstrap already uses for an equally caller-less enrollment.
	tenantCtx := security.WithContext(ctx, security.Context{
		TenantID:   invite.TenantID,
		TenantType: consumed.TenantType,
	})
	erunUser, _, err := s.users.Create(tenantCtx, repository.CreateUserParams{
		Username: params.Username,
		Issuer:   invite.Issuer,
		Subject:  idpUser.ID,
	})
	if err != nil {
		return AcceptInviteResult{IdPUser: idpUser}, fmt.Errorf("%w: idp user id %s: %w", ErrIdentityMappingFailed, idpUser.ID, err)
	}
	return AcceptInviteResult{IdPUser: idpUser, ErunUser: erunUser}, nil
}
