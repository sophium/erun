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
type InviteConsumer interface {
	ConsumeByToken(ctx context.Context, token string) (repository.ConsumedInvite, error)
}

// InviteZitadelAdmin is the Zitadel surface AcceptInvite needs.
type InviteZitadelAdmin interface {
	CreateHumanUser(ctx context.Context, params zitadel.CreateHumanUserParams) (zitadel.User, error)
}

// ErrInviteEmailMismatch reports an email differing from the one the invite pins.
var ErrInviteEmailMismatch = errors.New("invite is pinned to a different email address")

// InviteService coordinates invite acceptance across the token, the IdP and the erun user mapping.
type InviteService struct {
	invites InviteConsumer
	admin   InviteZitadelAdmin
	users   IdentityUserCreator
}

func NewInviteService(invites InviteConsumer, admin InviteZitadelAdmin, users IdentityUserCreator) *InviteService {
	return &InviteService{invites: invites, admin: admin, users: users}
}

// AcceptInviteParams is unauthenticated input; the token is the only credential.
type AcceptInviteParams struct {
	Token     string
	Username  string
	Email     string
	FirstName string
	LastName  string
	Password  string
}

// AcceptInviteResult keeps IdPUser populated when the erun mapping failed after the IdP user was created.
type AcceptInviteResult struct {
	IdPUser  zitadel.User
	ErunUser model.User
}

// Accept enrols the invitee into the invite's tenant. It always sets InitialPassword, unlike Enroll, because
// the invitee is present and choosing their own password.
func (s *InviteService) Accept(ctx context.Context, params AcceptInviteParams) (AcceptInviteResult, error) {
	consumed, err := s.invites.ConsumeByToken(ctx, params.Token)
	if err != nil {
		return AcceptInviteResult{}, err
	}
	invite := consumed.Invite

	email := strings.TrimSpace(params.Email)
	switch {
	case invite.Email == "":
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

	// There is no caller: bind a security context to the invite's resolved tenant.
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
