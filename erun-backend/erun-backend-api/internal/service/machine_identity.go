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

// MachineIdentityAdmin is the identity-provider surface minting one
// environment's machine identity needs. *zitadel.Client satisfies it; tests
// supply a stub.
type MachineIdentityAdmin interface {
	EnsureMachineIdentity(ctx context.Context, params zitadel.EnsureMachineIdentityParams) (zitadel.MachineIdentity, error)
}

// ErrMachineIdentityProviderUnavailable means this control plane has no
// identity provider it administers, so it cannot mint an identity for anyone.
// It is a configuration gap, not a tenant's fault, and the route reports it as
// such rather than as a refusal of the caller.
var ErrMachineIdentityProviderUnavailable = errors.New("machine identity provisioning is not configured on this control plane")

// ErrMachineIdentityUnavailable means this *tenant* cannot be given a machine
// identity: the platform administers no organization in an issuer the tenant
// resolves by. Distinct from ErrMachineIdentityProviderUnavailable because the
// remedy is different — one is a deployment's configuration, the other is the
// tenant's issuer mapping.
var ErrMachineIdentityUnavailable = errors.New("this tenant has no issuer the platform can create an identity in")

// MachineIdentityEnvironmentReader reads the environment an identity is
// provisioned for. repository.EnvironmentRepository satisfies it.
type MachineIdentityEnvironmentReader interface {
	Get(ctx context.Context, environmentID string) (model.Environment, error)
}

// MachineIdentityUserEnroller is erun's half of provisioning: creating the
// user row and its external-identity mapping, and granting the roles named.
// repository.UserRepository satisfies it.
type MachineIdentityUserEnroller interface {
	Create(ctx context.Context, params repository.CreateUserParams) (model.User, bool, error)
}

// MachineIdentityRoleRepository is the authorization half.
// repository.RoleRepository satisfies it.
type MachineIdentityRoleRepository interface {
	List(ctx context.Context) ([]model.Role, error)
	Grant(ctx context.Context, userID string, roleID string, tenantID string) (model.UserRole, error)
}

// MachineIdentityIssuerLister reads the tenant's registered issuer mappings.
// repository.TenantIssuerRepository satisfies it.
type MachineIdentityIssuerLister interface {
	List(ctx context.Context, filter repository.TenantIssuerFilter) ([]model.TenantIssuer, error)
}

// MachineIdentityService provisions one environment's own platform identity:
// the credential an environment authenticates as, instead of the operator
// whose signed-in session `erun init` used to delegate to it.
//
// It exists as a service rather than as three route calls because the two
// writes it has to make are both things the caller driving provisioning is not
// allowed to do directly. Enrolling a user is POST /v1/users and granting a
// role is POST /v1/users/{user_id}/roles, and both are TenantAdminOnly, while
// the delegated alias provisioning runs under is an ordinary operator's
// session — so on a tenant whose operator is not its genesis user (the common
// case) either route answers 403. The privileged work therefore happens here,
// with the API's own authority, behind a route whose own classification is the
// authorization. A service that re-entered those routes would have needed the
// caller to hold exactly the permission the feature exists because they do not
// hold.
//
// Everything is idempotent on the environment: the provider looks its identity
// up by a name derived from the environment's own id before creating anything,
// the user row is looked up by (issuer, subject) before it is inserted, and
// the role grant converges rather than inserting twice.
type MachineIdentityService struct {
	environments  MachineIdentityEnvironmentReader
	users         MachineIdentityUserEnroller
	roles         MachineIdentityRoleRepository
	tenantIssuers MachineIdentityIssuerLister
	admin         MachineIdentityAdmin
}

func NewMachineIdentityService(environments MachineIdentityEnvironmentReader, users MachineIdentityUserEnroller, roles MachineIdentityRoleRepository, tenantIssuers MachineIdentityIssuerLister, admin MachineIdentityAdmin) *MachineIdentityService {
	return &MachineIdentityService{
		environments:  environments,
		users:         users,
		roles:         roles,
		tenantIssuers: tenantIssuers,
		admin:         admin,
	}
}

// MachineIdentityResult is a provisioned environment identity. ClientSecret is
// the credential's confidential half and is returned only to the caller that
// asked for it, which places it in the environment's own secret store rather
// than persisting it here.
type MachineIdentityResult struct {
	EnvironmentID   string
	EnvironmentName string
	// Issuer is the registered issuer a token from this identity carries, and
	// the value the erun user row is mapped under.
	Issuer string
	// Subject is what such a token resolves by.
	Subject      string
	ClientID     string
	ClientSecret string
	UserID       string
	// AlreadyEnrolled is true when this environment's identity already existed
	// — the same identity, returned again. It is what makes a second
	// provisioning of one environment visibly a no-op rather than a second
	// identity a caller has to compare credentials to notice.
	AlreadyEnrolled bool
}

// MachineIdentityLoginName is the identity's name inside the provider, derived
// from the environment's own public id.
//
// Derivation rather than storage is the whole idempotency mechanism: there is
// no column anywhere linking an environment to its identity, so a second
// provisioning call has to arrive at the same name by the same arithmetic for
// the provider to find what the first one created. The id is a UUIDv7, so it
// is stable, unique, and safe as a login name.
func MachineIdentityLoginName(environmentID string) string {
	return "erun-env-" + strings.TrimSpace(environmentID)
}

// Provision returns the machine identity for environmentID, minting it if this
// is the first time. It is idempotent: a second call for the same environment
// returns the same identity, the same user row, and the same single grant.
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

// ensureTenantAgentGrant converges the identity's grant instead of relying on
// enrollment to have made it.
//
// A re-enrollment is Create's no-op path and deliberately does not re-run role
// assignment, so an identity that was enrolled earlier — by hand, before this
// existed, or with the grant since removed — would come back holding nothing
// and every call it makes would be refused. Asking for the role it is supposed
// to hold and treating "already holds it" as the state asked for makes a
// provisioning call a reconcile, the same way ensureNarrowerRolesExist
// reconciles the role's own grants rather than only inserting into an empty
// set.
func (s *MachineIdentityService) ensureTenantAgentGrant(ctx context.Context, userID string, roleID string) error {
	if _, err := s.roles.Grant(ctx, userID, roleID, ""); err != nil && !errors.Is(err, repository.ErrConflict) {
		return err
	}
	return nil
}

// tenantAgentRoleID resolves the machine role by name.
//
// RoleRepository.List ensures the narrower roles exist, and carry every route
// routeroles currently classifies for them, before it reads — so the role is
// present even for a tenant that bootstrapped before it shipped. A miss here is
// therefore a genuine inconsistency rather than "not seeded yet", and is
// reported as an error instead of silently enrolling the identity with no role.
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

// machineIdentityIssuer picks the issuer mapping the identity is created
// under, and the organization inside it.
//
// Only an org-scoped mapping can carry a machine identity: the identity is
// created in one organization of a shared issuer, and the org value is what
// makes it this tenant's. A tenant whose only mapping is single-tenant is one
// where the platform administers no organization it could create an identity
// in, and that is a refusal rather than a fallback — minting it in some other
// tenant's organization would produce exactly the attribution collapse this
// whole feature exists to remove.
//
// Several org-scoped mappings is ambiguous, not a choice to make silently: the
// caller named no issuer, the platform has no basis to prefer one, and picking
// by list order would put the machine identity under whichever mapping sorts
// first. The refusal names them so the operator can see what to reconcile.
func (s *MachineIdentityService) machineIdentityIssuer(ctx context.Context) (model.TenantIssuer, error) {
	// The tenant is named explicitly rather than left to RLS: an operations
	// session bypasses RLS, so an unfiltered read would offer this caller every
	// tenant's mappings to choose from.
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return model.TenantIssuer{}, repository.ErrMissingSecurityContext
	}
	issuers, err := s.tenantIssuers.List(ctx, repository.TenantIssuerFilter{TenantID: securityContext.TenantID})
	if err != nil {
		return model.TenantIssuer{}, err
	}
	orgScoped := make([]model.TenantIssuer, 0, len(issuers))
	for _, issuer := range issuers {
		if strings.TrimSpace(issuer.OrgFieldValue) != "" {
			orgScoped = append(orgScoped, issuer)
		}
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
