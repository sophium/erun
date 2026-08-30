package service

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

// IdentityAdmin is the Zitadel Management API surface identity enrollment
// needs. *zitadel.Client satisfies it; tests supply a stub.
type IdentityAdmin interface {
	CreateHumanUser(ctx context.Context, params zitadel.CreateHumanUserParams) (zitadel.User, error)
	GetSMTPStatus(ctx context.Context) (zitadel.SMTPStatus, error)
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
	// OrgID enrolls into another organization: the identity boundary a
	// different tenant resolves by. It is what makes a freshly created
	// tenant reachable at all — without it every enrollment lands in the
	// platform's own org, so a new tenant can never receive a token that
	// resolves to it, never gets a first user, and can never own an
	// environment.
	//
	// No erun-side user row is written in that case, and that is not a
	// failure: the caller's tenant is not the new user's, and row-level
	// security would file them under the wrong one. The target tenant's own
	// first-user bootstrap enrolls them, with full access, on their first
	// sign-in.
	OrgID string
}

// EnrollIdentityResult reports what actually landed. ErunUser is the zero
// value when the erun-side mapping failed after the IdP user was created;
// IdPUser is still populated so the caller can report the orphaned IdP
// identity precisely rather than as an opaque failure.
//
// MailDeliveryConfigured and TemporaryPassword report the other half of
// what actually landed (issue #1168): when the platform's IdP has no SMTP
// configured, Zitadel's own invitation email can never arrive, so Enroll
// does not create the usual passwordless, email-pending account -- it mints
// a random initial password instead and reports it here, once, for the
// caller to hand to the enrollee out of band. TemporaryPassword is empty
// whenever MailDeliveryConfigured is true (the normal invite-email flow
// ran instead).
type EnrollIdentityResult struct {
	IdPUser                zitadel.User
	ErunUser               model.User
	MailDeliveryConfigured bool
	TemporaryPassword      string
	// MappingDeferred reports that no erun user row was written because the
	// identity was enrolled into another tenant's org, so a zero ErunUser
	// here means "by design", not "the mapping failed".
	MappingDeferred bool
}

// Enroll creates the IdP identity first — the erun mapping needs the
// external subject Zitadel assigns, so the IdP half must succeed before the
// erun half can even be attempted — then creates the erun user and its
// external-identity mapping using that subject. A failure after the IdP
// user exists is reported as ErrIdentityMappingFailed with the created
// IdPUser still attached, rather than as an opaque enrollment failure.
//
// Before creating the IdP identity, it checks whether the platform can
// actually send mail at all (issue #1168). When it cannot, Zitadel's usual
// invite-by-email flow would create an account stuck in USER_STATE_INITIAL
// forever, waiting on a link nothing will ever send -- a success response
// that silently does nothing, exactly the dead end this checks for. A
// failed or inconclusive SMTP status check is treated the same as
// "unconfigured": assuming mail works when it might not risks the exact
// same dead end, while the temporary-password fallback always leaves the
// account usable regardless.
func (s *IdentityService) Enroll(ctx context.Context, params EnrollIdentityParams) (EnrollIdentityResult, error) {
	mailStatus, _ := s.admin.GetSMTPStatus(ctx)

	createParams := zitadel.CreateHumanUserParams{
		Username:  params.Username,
		Email:     params.Email,
		FirstName: params.FirstName,
		LastName:  params.LastName,
		OrgID:     params.OrgID,
	}
	if !mailStatus.Configured {
		temporaryPassword, err := generateTemporaryPassword()
		if err != nil {
			return EnrollIdentityResult{}, fmt.Errorf("generate temporary password for a no-mail enrollment: %w", err)
		}
		createParams.InitialPassword = temporaryPassword
	}

	idpUser, err := s.admin.CreateHumanUser(ctx, createParams)
	if err != nil {
		return EnrollIdentityResult{}, fmt.Errorf("create identity provider user: %w", err)
	}

	// Enrolled into another tenant's org: stop at the IdP half. See
	// EnrollIdentityParams.OrgID for why writing an erun user here would file
	// them under the caller's tenant instead of their own.
	if strings.TrimSpace(params.OrgID) != "" {
		return EnrollIdentityResult{
			IdPUser:                idpUser,
			MailDeliveryConfigured: mailStatus.Configured,
			TemporaryPassword:      createParams.InitialPassword,
			MappingDeferred:        true,
		}, nil
	}

	erunUser, err := s.users.Create(ctx, repository.CreateUserParams{
		Username: params.Username,
		Issuer:   params.Issuer,
		Subject:  idpUser.ID,
	})
	if err != nil {
		return EnrollIdentityResult{IdPUser: idpUser, MailDeliveryConfigured: mailStatus.Configured, TemporaryPassword: createParams.InitialPassword}, fmt.Errorf("%w: idp user id %s: %w", ErrIdentityMappingFailed, idpUser.ID, err)
	}
	return EnrollIdentityResult{
		IdPUser:                idpUser,
		ErunUser:               erunUser,
		MailDeliveryConfigured: mailStatus.Configured,
		TemporaryPassword:      createParams.InitialPassword,
	}, nil
}

// temporaryPasswordAlphabet excludes visually ambiguous characters (0/O,
// 1/l/I) since this password is meant to be read aloud or retyped by an
// operator handing it to an enrollee out of band.
const temporaryPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// generateTemporaryPassword mints a random password satisfying Zitadel's
// default complexity policy (upper, lower, digit, symbol), the same shape
// the erun-zitadel chart already generates for its own bootstrap admin
// password.
func generateTemporaryPassword() (string, error) {
	const randomLength = 20
	raw := make([]byte, randomLength)
	if _, err := cryptorand.Read(raw); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	chars := make([]byte, randomLength)
	for i, b := range raw {
		chars[i] = temporaryPasswordAlphabet[int(b)%len(temporaryPasswordAlphabet)]
	}
	return fmt.Sprintf("Er%s!", string(chars)), nil
}
