package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

// IdentityAdmin is the Zitadel Management API surface identity enrollment
// needs. *zitadel.Client satisfies it; tests supply a stub.
type IdentityAdmin interface {
	CreateHumanUser(ctx context.Context, params zitadel.CreateHumanUserParams) (zitadel.User, error)
}

// IdentityUserCreator is the erun-side half of enrollment: creating the
// user row and its external-identity mapping. repository.UserRepository
// satisfies it.
type IdentityUserCreator interface {
	Create(ctx context.Context, params repository.CreateUserParams) (model.User, error)
}

// ErrIdentityMappingFailed marks an enrollment where the IdP half landed but
// the erun-side mapping did not. Wrapping it (rather than only the
// underlying repository error) lets a caller distinguish "nothing happened"
// from "an IdP user now exists with no erun mapping" — the exact half-landed
// state the issue asks to report precisely rather than silently swallow.
var ErrIdentityMappingFailed = errors.New("identity created in the identity provider but the erun user mapping failed")

// IdentityService coordinates enrollment: creating the IdP identity and the
// erun user plus its user_external_ids mapping are two separate systems, and
// nothing else in this codebase already has to reconcile a partial failure
// between them.
type IdentityService struct {
	admin IdentityAdmin
	users IdentityUserCreator
}

func NewIdentityService(admin IdentityAdmin, users IdentityUserCreator) *IdentityService {
	return &IdentityService{admin: admin, users: users}
}

// EnrollIdentityParams is the enrollment input. Issuer is the OIDC issuer the
// caller themselves authenticated with — since identity administration is
// restricted to an OPERATIONS tenant, that issuer is definitionally the
// tenant's own Zitadel, the same IdP the new user is created in, and it is
// already registered in tenant_issuers (the caller could not have signed in
// otherwise) so the new user_external_ids row's foreign key resolves.
type EnrollIdentityParams struct {
	Username  string
	Email     string
	FirstName string
	LastName  string
	Issuer    string
}

// EnrollIdentityResult reports what actually landed. ErunUser is the zero
// value when the erun-side mapping failed after the IdP user was created;
// IdPUser is still populated so the caller can report the orphaned IdP
// identity precisely rather than as an opaque failure.
type EnrollIdentityResult struct {
	IdPUser  zitadel.User
	ErunUser model.User
}

// Enroll creates the IdP identity first — the erun mapping needs the
// external subject Zitadel assigns, so the IdP half must succeed before the
// erun half can even be attempted — then creates the erun user and its
// external-identity mapping using that subject. A failure after the IdP
// user exists is reported as ErrIdentityMappingFailed with the created
// IdPUser still attached, rather than as an opaque enrollment failure.
func (s *IdentityService) Enroll(ctx context.Context, params EnrollIdentityParams) (EnrollIdentityResult, error) {
	idpUser, err := s.admin.CreateHumanUser(ctx, zitadel.CreateHumanUserParams{
		Username:  params.Username,
		Email:     params.Email,
		FirstName: params.FirstName,
		LastName:  params.LastName,
	})
	if err != nil {
		return EnrollIdentityResult{}, fmt.Errorf("create identity provider user: %w", err)
	}

	erunUser, err := s.users.Create(ctx, repository.CreateUserParams{
		Username: params.Username,
		Issuer:   params.Issuer,
		Subject:  idpUser.ID,
	})
	if err != nil {
		return EnrollIdentityResult{IdPUser: idpUser}, fmt.Errorf("%w: idp user id %s: %w", ErrIdentityMappingFailed, idpUser.ID, err)
	}
	return EnrollIdentityResult{IdPUser: idpUser, ErunUser: erunUser}, nil
}
