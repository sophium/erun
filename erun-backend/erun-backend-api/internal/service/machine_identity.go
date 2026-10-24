package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

// MachineIdentityAdmin mints and revokes environment machine identities in the platform's identity provider.
type MachineIdentityAdmin interface {
	EnsureMachineIdentity(ctx context.Context, params zitadel.EnsureMachineIdentityParams) (zitadel.MachineIdentity, error)
	DeleteMachineIdentity(ctx context.Context, params zitadel.DeleteMachineIdentityParams) (bool, error)
}

// ErrMachineIdentityProviderUnavailable means no identity provider is configured: a deployment gap, not the caller's fault.
var ErrMachineIdentityProviderUnavailable = errors.New("machine identity provisioning is not configured on this control plane")

// ErrMachineIdentityUnavailable means the tenant has no org-scoped issuer the platform administers.
var ErrMachineIdentityUnavailable = errors.New("this tenant has no issuer the platform can create an identity in")

// MachineIdentityEnvironmentReader reads the environment an identity is provisioned for.
type MachineIdentityEnvironmentReader interface {
	Get(ctx context.Context, environmentID string) (model.Environment, error)
}

// MachineIdentityUserStore creates and removes an identity's user row, external-id mapping and role grants.
type MachineIdentityUserStore interface {
	Create(ctx context.Context, params repository.CreateUserParams) (model.User, bool, error)
	DeleteByUsername(ctx context.Context, tenantID string, username string) (bool, error)
}

// MachineIdentityRoleRepository is the authorization half.
type MachineIdentityRoleRepository interface {
	List(ctx context.Context) ([]model.Role, error)
	Grant(ctx context.Context, userID string, roleID string, tenantID string) (model.UserRole, error)
}

// MachineIdentityIssuerLister reads the tenant's registered issuer mappings.
type MachineIdentityIssuerLister interface {
	List(ctx context.Context, filter repository.TenantIssuerFilter) ([]model.TenantIssuer, error)
}

// MachineIdentityService provisions an environment's own platform identity. It writes with the API's own
// authority because user enrollment and role grants are TenantAdminOnly routes the provisioning caller
// typically cannot call. Everything is idempotent on the environment id.
type MachineIdentityService struct {
	environments  MachineIdentityEnvironmentReader
	users         MachineIdentityUserStore
	roles         MachineIdentityRoleRepository
	tenantIssuers MachineIdentityIssuerLister
	admin         MachineIdentityAdmin
}

func NewMachineIdentityService(environments MachineIdentityEnvironmentReader, users MachineIdentityUserStore, roles MachineIdentityRoleRepository, tenantIssuers MachineIdentityIssuerLister, admin MachineIdentityAdmin) *MachineIdentityService {
	return &MachineIdentityService{
		environments:  environments,
		users:         users,
		roles:         roles,
		tenantIssuers: tenantIssuers,
		admin:         admin,
	}
}

// MachineIdentityResult is a provisioned identity; ClientSecret is returned to the caller, never persisted here.
type MachineIdentityResult struct {
	EnvironmentID   string
	EnvironmentName string
	// Issuer is the issuer the identity's tokens carry and its user row is mapped under.
	Issuer       string
	Subject      string
	ClientID     string
	ClientSecret string
	UserID       string
	// AlreadyEnrolled reports that the same identity already existed.
	AlreadyEnrolled bool
}

// MachineIdentityLoginName derives the provider login name from the environment id; derivation, not storage,
// is what makes provisioning idempotent.
func MachineIdentityLoginName(environmentID string) string {
	return "erun-env-" + strings.TrimSpace(environmentID)
}

// Provision returns environmentID's machine identity, minting it on first call.
func (s *MachineIdentityService) Provision(ctx context.Context, environmentID string) (MachineIdentityResult, error) {
	if s.admin == nil {
		return MachineIdentityResult{}, ErrMachineIdentityProviderUnavailable
	}
	environment, err := s.environments.Get(ctx, environmentID)
	if err != nil {
		return MachineIdentityResult{}, err
	}
	issuer, err := s.machineIdentityIssuer(ctx)
	if err != nil {
		return MachineIdentityResult{}, err
	}
	loginName := MachineIdentityLoginName(environment.EnvironmentID)
	identity, err := s.admin.EnsureMachineIdentity(ctx, zitadel.EnsureMachineIdentityParams{
		OrgID:     issuer.OrgFieldValue,
		LoginName: loginName,
	})
	if err != nil {
		return MachineIdentityResult{}, err
	}
	roleID, err := s.tenantAgentRoleID(ctx)
	if err != nil {
		return MachineIdentityResult{}, err
	}
	user, alreadyEnrolled, err := s.users.Create(ctx, repository.CreateUserParams{
		Username: loginName,
		Issuer:   issuer.Issuer,
		Subject:  identity.ClientID,
		RoleIDs:  []string{roleID},
	})
	if err != nil {
		return MachineIdentityResult{}, err
	}
	if err := s.ensureTenantAgentGrant(ctx, user.UserID, roleID); err != nil {
		return MachineIdentityResult{}, err
	}
	return MachineIdentityResult{
		EnvironmentID:   environment.EnvironmentID,
		EnvironmentName: environment.Name,
		Issuer:          issuer.Issuer,
		Subject:         identity.ClientID,
		ClientID:        identity.ClientID,
		ClientSecret:    identity.ClientSecret,
		UserID:          user.UserID,
		AlreadyEnrolled: alreadyEnrolled,
	}, nil
}

// ensureTenantAgentGrant converges the grant: a re-enrollment does not re-run role assignment.
func (s *MachineIdentityService) ensureTenantAgentGrant(ctx context.Context, userID string, roleID string) error {
	if _, err := s.roles.Grant(ctx, userID, roleID, ""); err != nil && !errors.Is(err, repository.ErrConflict) {
		return err
	}
	return nil
}

// tenantAgentRoleID errors on a miss: RoleRepository.List already ensures the role exists.
func (s *MachineIdentityService) tenantAgentRoleID(ctx context.Context) (string, error) {
	roles, err := s.roles.List(ctx)
	if err != nil {
		return "", err
	}
	for _, role := range roles {
		if role.Name == repository.TenantAgentRoleName {
			return role.RoleID, nil
		}
	}
	return "", fmt.Errorf("role %s is missing from this tenant's role set", repository.TenantAgentRoleName)
}

// machineIdentityIssuer requires exactly one org-scoped mapping. Minting in another tenant's org or picking
// one of several by list order would misattribute the identity.
func (s *MachineIdentityService) machineIdentityIssuer(ctx context.Context) (model.TenantIssuer, error) {
	// Filter by tenant explicitly: an operations session bypasses RLS.
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return model.TenantIssuer{}, repository.ErrMissingSecurityContext
	}
	orgScoped, err := s.orgScopedIssuers(ctx, securityContext.TenantID)
	if err != nil {
		return model.TenantIssuer{}, err
	}
	switch len(orgScoped) {
	case 0:
		return model.TenantIssuer{}, fmt.Errorf("%w: every issuer it resolves by is single-tenant, so the platform administers no organization to create one in", ErrMachineIdentityUnavailable)
	case 1:
		return orgScoped[0], nil
	default:
		named := make([]string, 0, len(orgScoped))
		for _, issuer := range orgScoped {
			named = append(named, issuer.Issuer)
		}
		return model.TenantIssuer{}, fmt.Errorf("%w: it resolves by %d org-scoped issuers (%s)", ErrMachineIdentityUnavailable, len(orgScoped), strings.Join(named, ", "))
	}
}

// orgScopedIssuers filters by tenantID explicitly because an operations session bypasses RLS.
func (s *MachineIdentityService) orgScopedIssuers(ctx context.Context, tenantID string) ([]model.TenantIssuer, error) {
	issuers, err := s.tenantIssuers.List(ctx, repository.TenantIssuerFilter{TenantID: tenantID})
	if err != nil {
		return nil, err
	}
	orgScoped := make([]model.TenantIssuer, 0, len(issuers))
	for _, issuer := range issuers {
		if strings.TrimSpace(issuer.OrgFieldValue) != "" {
			orgScoped = append(orgScoped, issuer)
		}
	}
	return orgScoped, nil
}

type revocationResult struct {
	EnvironmentID string
	LoginName     string
	// UserRemoved is false for an environment that never had a machine identity.
	UserRemoved bool
	// ApplicationsRemoved is summed over every org the tenant resolves by.
	ApplicationsRemoved int
}

// revokeIdentity removes the erun rows first: that alone makes the identity unusable, and the likelier-to-fail
// provider half stays findable for a retry. Unlike Provision, it searches every org-scoped issuer so
// configuration drift cannot leave an identity alive.
func (s *MachineIdentityService) revokeIdentity(ctx context.Context, environmentID string) (revocationResult, error) {
	environment, err := s.environments.Get(ctx, environmentID)
	if err != nil {
		return revocationResult{}, err
	}
	result := revocationResult{
		EnvironmentID: environment.EnvironmentID,
		LoginName:     MachineIdentityLoginName(environment.EnvironmentID),
	}
	// The environment's tenant, not the session's: an operations caller may delete another tenant's environment.
	removed, err := s.users.DeleteByUsername(ctx, environment.TenantID, result.LoginName)
	if err != nil {
		return result, err
	}
	result.UserRemoved = removed

	if s.admin == nil {
		return result, nil
	}
	issuers, err := s.orgScopedIssuers(ctx, environment.TenantID)
	if err != nil {
		return result, err
	}
	for _, issuer := range issuers {
		removed, err := s.admin.DeleteMachineIdentity(ctx, zitadel.DeleteMachineIdentityParams{
			OrgID:     issuer.OrgFieldValue,
			LoginName: result.LoginName,
		})
		if err != nil {
			return result, err
		}
		if removed {
			result.ApplicationsRemoved++
		}
	}
	return result, nil
}

// Revoke removes environmentID's machine identity so no credential outlives the environment. It is idempotent:
// the environment-delete workflow retries from the top.
func (s *MachineIdentityService) Revoke(ctx context.Context, environmentID string) error {
	result, err := s.revokeIdentity(ctx, environmentID)
	if err != nil {
		return err
	}
	if result.UserRemoved || result.ApplicationsRemoved > 0 {
		log.Printf("erun api machine identity: revoked %s for environment=%q (user row removed: %t, provider applications removed: %d)",
			result.LoginName, result.EnvironmentID, result.UserRemoved, result.ApplicationsRemoved)
	}
	return nil
}
