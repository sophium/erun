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

// IdentityAdmin is the Zitadel Management API surface identity enrollment needs.
type IdentityAdmin interface {
	CreateHumanUser(ctx context.Context, params zitadel.CreateHumanUserParams) (zitadel.User, error)
	GetSMTPStatus(ctx context.Context) (zitadel.SMTPStatus, error)
}

// IdentityUserCreator creates the erun user row and its external-identity mapping.
type IdentityUserCreator interface {
	Create(ctx context.Context, params repository.CreateUserParams) (model.User, bool, error)
}

// ErrIdentityMappingFailed marks an enrollment whose IdP user exists but whose erun mapping does not.
var ErrIdentityMappingFailed = errors.New("identity created in the identity provider but the erun user mapping failed")

// IdentityService coordinates enrollment across the IdP and the erun database.
type IdentityService struct {
	admin IdentityAdmin
	users IdentityUserCreator
}

func NewIdentityService(admin IdentityAdmin, users IdentityUserCreator) *IdentityService {
	return &IdentityService{admin: admin, users: users}
}

// EnrollIdentityParams is the enrollment input. Issuer is the caller's own issuer, which is the tenant's
// Zitadel and already in tenant_issuers, so the new mapping's foreign key resolves.
type EnrollIdentityParams struct {
	Username  string
	Email     string
	FirstName string
	LastName  string
	Issuer    string
	// OrgID enrolls into another tenant's org. No erun user row is written then: RLS would file it under the
	// caller's tenant, and the target tenant's first-user bootstrap enrolls them on first sign-in.
	OrgID string
}

// EnrollIdentityResult reports what landed. TemporaryPassword is set, once, only when the IdP has no SMTP,
// since Zitadel's invitation email could never arrive.
type EnrollIdentityResult struct {
	IdPUser                zitadel.User
	ErunUser               model.User
	MailDeliveryConfigured bool
	TemporaryPassword      string
	// MappingDeferred means a zero ErunUser is by design (enrolled into another tenant's org).
	MappingDeferred bool
}

// Enroll creates the IdP identity first because the erun mapping needs its subject. An unconfigured or
// unknown SMTP status falls back to a temporary password so the account is never stuck in USER_STATE_INITIAL.
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

	if strings.TrimSpace(params.OrgID) != "" {
		return EnrollIdentityResult{
			IdPUser:                idpUser,
			MailDeliveryConfigured: mailStatus.Configured,
			TemporaryPassword:      createParams.InitialPassword,
			MappingDeferred:        true,
		}, nil
	}

	erunUser, _, err := s.users.Create(ctx, repository.CreateUserParams{
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

// Excludes ambiguous characters (0/O, 1/l/I): an operator reads this password out of band.
const temporaryPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// generateTemporaryPassword satisfies Zitadel's default complexity policy (upper, lower, digit, symbol).
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
