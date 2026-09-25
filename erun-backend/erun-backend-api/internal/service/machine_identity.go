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

// MachineIdentityAdmin is the identity-provider surface this service
// administers: minting one environment's machine identity, and revoking it
// again. Both halves live on one collaborator because they are one
// relationship — the platform's own identity provider — and *zitadel.Client is
// the only thing that satisfies either. Tests supply a stub.
type MachineIdentityAdmin interface {
	EnsureMachineIdentity(ctx context.Context, params zitadel.EnsureMachineIdentityParams) (zitadel.MachineIdentity, error)
	DeleteMachineIdentity(ctx context.Context, params zitadel.DeleteMachineIdentityParams) (bool, error)
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

// MachineIdentityUserStore is erun's half of an environment identity's
// lifecycle: creating the user row, its external-identity mapping and the
// roles named, and removing all three again on revocation.
// repository.UserRepository satisfies it.
type MachineIdentityUserStore interface {
	Create(ctx context.Context, params repository.CreateUserParams) (model.User, bool, error)
	DeleteByUsername(ctx context.Context, tenantID string, username string) (bool, error)
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

// orgScopedIssuers lists the tenant's registered issuer mappings that carry an
// organization, in the order the repository returns them. tenantID names the
// tenant explicitly rather than leaving it to RLS, because an operations
// session bypasses RLS and an unfiltered read would offer every tenant's
// mappings.
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

// revocationResult reports what one revocation actually removed, so a caller
// can tell a delete that revoked an identity from one that had none to revoke
// without treating either as different outcomes.
type revocationResult struct {
	EnvironmentID string
	// LoginName is the name the identity was provisioned under, derived from
	// the environment's own id. It is reported so a delete's trace names the
	// identity it acted on rather than only the environment that held it.
	LoginName string
	// UserRemoved reports whether this tenant still held the erun-side rows —
	// the user, its external-identity mapping and its role grants. False is the
	// ordinary answer for an environment that never had a machine identity.
	UserRemoved bool
	// ApplicationsRemoved counts the identity-provider applications deleted,
	// summed over every organization the tenant resolves by.
	ApplicationsRemoved int
}

// revokeIdentity is Revoke's implementation, returning what it actually
// removed for the caller that reports it.
//
// The erun-side rows go first. Removing them is already enough to make the
// identity unusable at this API — the middleware refuses a token whose subject
// resolves to no user — while the provider half is the one that needs the
// platform's identity-provider credential and so is the likelier to fail.
// Ordering the reliable half first means a retry after a partial failure still
// has the provider application to find, which is exactly what its own lookup
// needs; the reverse order would lose the client id the user row is found by
// and strand a live credential.
//
// Every org-scoped issuer the tenant resolves by is searched, rather than the
// single one Provision insists on. The two have opposite biases: provisioning
// must not guess which organization an identity belongs to, but a revocation
// that gave up because the tenant's issuer mappings had since grown a second
// entry would leave the identity alive precisely when the configuration
// drifted. Searching each and removing what is found reconciles that instead of
// refusing.
func (s *MachineIdentityService) revokeIdentity(ctx context.Context, environmentID string) (revocationResult, error) {
	environment, err := s.environments.Get(ctx, environmentID)
	if err != nil {
		return revocationResult{}, err
	}
	result := revocationResult{
		EnvironmentID: environment.EnvironmentID,
		LoginName:     MachineIdentityLoginName(environment.EnvironmentID),
	}
	// The environment's own tenant, not the session's: an operations caller
	// deleting another tenant's environment must not scope this to their own.
	removed, err := s.users.DeleteByUsername(ctx, environment.TenantID, result.LoginName)
	if err != nil {
		return result, err
	}
	result.UserRemoved = removed

	if s.admin == nil {
		// No provider is administered here, so nothing was ever minted in one
		// and the erun-side half above is the whole of what could exist.
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

// Revoke removes environmentID's machine identity: the erun user row that a
// token from it resolves to, and the provider application that mints that
// token. It is what makes deleting an environment leave nothing usable behind,
// rather than leaving a credential that outlives the environment it was minted
// for.
//
// It is the whole public surface of revocation, and reports only whether it
// succeeded: the caller that drives it is the environment-delete teardown,
// which owns the delete's ordering and failure semantics and has no business
// interpreting what a revocation removed. What was removed is logged instead,
// so an environment's delete trace still names the identity it revoked.
//
// Idempotent by construction. Both halves are found by the same derived name
// Provision created them under, and "there was no identity there" is answered
// as the state asked for rather than as an error — which it has to be, because
// the environment-delete workflow it runs inside retries an attempt that
// failed partway from the top.
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
