package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

// fakeMachineIdentityAdmin stands in for the identity provider. It holds one
// identity per login name and returns the one it already has for a repeat
// call, so a test can assert what provisioning did *not* mint on the second
// pass -- the half idempotence actually rests on, since a provider that minted
// a fresh identity every time would leave the service's own writes looking
// idempotent while the credential in the environment had silently changed.
type fakeMachineIdentityAdmin struct {
	identities map[string]zitadel.MachineIdentity
	ensureErr  error
	calls      []zitadel.EnsureMachineIdentityParams
}

func newFakeMachineIdentityAdmin() *fakeMachineIdentityAdmin {
	return &fakeMachineIdentityAdmin{identities: map[string]zitadel.MachineIdentity{}}
}

func (f *fakeMachineIdentityAdmin) EnsureMachineIdentity(_ context.Context, params zitadel.EnsureMachineIdentityParams) (zitadel.MachineIdentity, error) {
	f.calls = append(f.calls, params)
	if f.ensureErr != nil {
		return zitadel.MachineIdentity{}, f.ensureErr
	}
	if existing, ok := f.identities[params.LoginName]; ok {
		return existing, nil
	}
	identity := zitadel.MachineIdentity{
		ClientID:     "client-" + params.LoginName,
		ClientSecret: "secret-" + params.LoginName,
	}
	f.identities[params.LoginName] = identity
	return identity, nil
}

// fakeMachineIdentityEnvironments is the environment read.
type fakeMachineIdentityEnvironments struct {
	environment model.Environment
	err         error
}

func (f *fakeMachineIdentityEnvironments) Get(_ context.Context, environmentID string) (model.Environment, error) {
	if f.err != nil {
		return model.Environment{}, f.err
	}
	environment := f.environment
	if environment.EnvironmentID == "" {
		environment.EnvironmentID = environmentID
	}
	return environment, nil
}

// fakeMachineIdentityUsers is an in-memory stand-in for UserRepository.Create
// that reproduces the two behaviours the service depends on: an identity
// already mapped in the tenant is a no-op that reports the existing user, and
// a fresh one is inserted with the roles it was handed.
type fakeMachineIdentityUsers struct {
	bySubject map[string]model.User
	grants    map[string]map[string]bool
	createErr error
}

func newFakeMachineIdentityUsers() *fakeMachineIdentityUsers {
	return &fakeMachineIdentityUsers{bySubject: map[string]model.User{}, grants: map[string]map[string]bool{}}
}

func (f *fakeMachineIdentityUsers) Create(_ context.Context, params repository.CreateUserParams) (model.User, bool, error) {
	if f.createErr != nil {
		return model.User{}, false, f.createErr
	}
	if existing, ok := f.bySubject[params.Subject]; ok {
		return existing, true, nil
	}
	user := model.User{UserID: "user-" + params.Username, Username: params.Username}
	f.bySubject[params.Subject] = user
	f.grants[user.UserID] = map[string]bool{}
	for _, roleID := range params.RoleIDs {
		f.grants[user.UserID][roleID] = true
	}
	return user, false, nil
}

// enrolledIdentities is how many distinct identities this tenant has rows
// for. It is the count a second provisioning call must not increase.
func (f *fakeMachineIdentityUsers) enrolledIdentities() int {
	return len(f.bySubject)
}

// fakeMachineIdentityRoles is the role read and grant.
type fakeMachineIdentityRoles struct {
	roles      []model.Role
	granted    map[string]map[string]bool
	grantCalls int
	listErr    error
}

func newFakeMachineIdentityRoles(roles ...model.Role) *fakeMachineIdentityRoles {
	return &fakeMachineIdentityRoles{roles: roles, granted: map[string]map[string]bool{}}
}

func (f *fakeMachineIdentityRoles) List(context.Context) ([]model.Role, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.roles, nil
}

func (f *fakeMachineIdentityRoles) Grant(_ context.Context, userID string, roleID string, _ string) (model.UserRole, error) {
	f.grantCalls++
	if f.granted[userID] == nil {
		f.granted[userID] = map[string]bool{}
	}
	if f.granted[userID][roleID] {
		return model.UserRole{}, repository.ErrConflict
	}
	f.granted[userID][roleID] = true
	return model.UserRole{UserID: userID, RoleID: roleID}, nil
}

func (f *fakeMachineIdentityRoles) grantCount(userID string) int {
	return len(f.granted[userID])
}

// fakeMachineIdentityIssuers is the tenant issuer mapping read.
type fakeMachineIdentityIssuers struct {
	issuers []model.TenantIssuer
	err     error
}

func (f *fakeMachineIdentityIssuers) List(context.Context, repository.TenantIssuerFilter) ([]model.TenantIssuer, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.issuers, nil
}

// machineIdentityFixture is one assembled service plus the fakes behind it,
// with a single org-scoped issuer mapping for the tenant and the seeded
// machine role.
type machineIdentityFixture struct {
	service *MachineIdentityService
	admin   *fakeMachineIdentityAdmin
	users   *fakeMachineIdentityUsers
	roles   *fakeMachineIdentityRoles
	issuers *fakeMachineIdentityIssuers
	env     *fakeMachineIdentityEnvironments
	// ctx is the tenant-scoped context every call runs under. The service
	// names the tenant explicitly rather than leaning on RLS (an operations
	// session bypasses it), so it needs one.
	ctx context.Context
}

func newMachineIdentityFixture(t *testing.T) *machineIdentityFixture {
	t.Helper()
	fixture := &machineIdentityFixture{
		admin: newFakeMachineIdentityAdmin(),
		users: newFakeMachineIdentityUsers(),
		roles: newFakeMachineIdentityRoles(model.Role{RoleID: "role-agent", Name: repository.TenantAgentRoleName}),
		issuers: &fakeMachineIdentityIssuers{issuers: []model.TenantIssuer{
			{TenantID: "tenant-1", Issuer: "https://auth.example", OrgFieldKey: "urn:zitadel:iam:user:resourceowner:id", OrgFieldValue: "org-1"},
		}},
		env: &fakeMachineIdentityEnvironments{environment: model.Environment{EnvironmentID: "env-1", Name: "alpha"}},
	}
	fixture.ctx = security.WithContext(context.Background(), security.Context{
		TenantID:   "tenant-1",
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "caller-user",
	})
	fixture.service = NewMachineIdentityService(fixture.env, fixture.users, fixture.roles, fixture.issuers, fixture.admin)
	return fixture
}

// TestProvisionEnrollsTheIdentityAndGrantsTheMachineRole is the ordinary first
// provisioning: one identity in the tenant's org, one user row mapped to it,
// and exactly the TenantAgent role.
func TestProvisionEnrollsTheIdentityAndGrantsTheMachineRole(t *testing.T) {
	fixture := newMachineIdentityFixture(t)

	result, err := fixture.service.Provision(fixture.ctx, "env-1")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	assertProviderWasAskedForTheTenantsOwnOrg(t, fixture, "env-1")
	if result.Issuer != "https://auth.example" || result.Subject != result.ClientID || result.ClientSecret == "" {
		t.Fatalf("unexpected result %+v", result)
	}
	if result.AlreadyEnrolled {
		t.Fatalf("first provisioning reported an existing enrollment: %+v", result)
	}
	assertHoldsExactlyTheMachineRole(t, fixture, result.UserID)
}

func assertProviderWasAskedForTheTenantsOwnOrg(t *testing.T, fixture *machineIdentityFixture, environmentID string) {
	t.Helper()
	if len(fixture.admin.calls) != 1 || fixture.admin.calls[0].OrgID != "org-1" {
		t.Fatalf("expected one provider call against the tenant's own org, got %+v", fixture.admin.calls)
	}
	if want := MachineIdentityLoginName(environmentID); fixture.admin.calls[0].LoginName != want {
		t.Fatalf("provider was asked for %q, want %q", fixture.admin.calls[0].LoginName, want)
	}
}

func assertHoldsExactlyTheMachineRole(t *testing.T, fixture *machineIdentityFixture, userID string) {
	t.Helper()
	if grants := fixture.roles.grantCount(userID); grants != 1 {
		t.Fatalf("expected exactly one role grant, got %d", grants)
	}
	if !fixture.roles.granted[userID]["role-agent"] {
		t.Fatalf("expected the %s role to be granted, got %+v", repository.TenantAgentRoleName, fixture.roles.granted[userID])
	}
}

// TestProvisionIsIdempotentForOneEnvironment is the property the whole design
// rests on, and the one this change has to demonstrate rather than assert:
// provisioning the same environment twice must not create a second identity,
// a second user row, or a second grant.
func TestProvisionIsIdempotentForOneEnvironment(t *testing.T) {
	fixture := newMachineIdentityFixture(t)

	first, err := fixture.service.Provision(fixture.ctx, "env-1")
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	second, err := fixture.service.Provision(fixture.ctx, "env-1")
	if err != nil {
		t.Fatalf("second Provision: %v", err)
	}

	if len(fixture.admin.identities) != 1 {
		t.Fatalf("expected exactly one identity in the provider, got %d", len(fixture.admin.identities))
	}
	if second.ClientID != first.ClientID || second.ClientSecret != first.ClientSecret {
		t.Fatalf("second provisioning returned a different credential: %+v vs %+v", second, first)
	}
	if !second.AlreadyEnrolled {
		t.Fatalf("second provisioning did not report the existing enrollment: %+v", second)
	}
	if identities := fixture.users.enrolledIdentities(); identities != 1 {
		t.Fatalf("expected exactly one enrolled identity after two provisioning calls, got %d", identities)
	}
	if grants := fixture.roles.grantCount(first.UserID); grants != 1 {
		t.Fatalf("expected exactly one grant after two provisioning calls, got %d", grants)
	}
}

// TestProvisionRegrantsTheMachineRoleToAnIdentityThatLostIt covers the
// reconcile half: an identity enrolled earlier (by hand, or with the grant
// since revoked) is not left holding nothing, which would make every call it
// makes refused while provisioning reported success.
func TestProvisionRegrantsTheMachineRoleToAnIdentityThatLostIt(t *testing.T) {
	fixture := newMachineIdentityFixture(t)
	if _, err := fixture.service.Provision(fixture.ctx, "env-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	// Simulate a revoked grant on an identity that is still enrolled.
	for userID := range fixture.roles.granted {
		fixture.roles.granted[userID] = map[string]bool{}
	}

	result, err := fixture.service.Provision(fixture.ctx, "env-1")
	if err != nil {
		t.Fatalf("second Provision: %v", err)
	}
	if grants := fixture.roles.grantCount(result.UserID); grants != 1 {
		t.Fatalf("expected the machine role to be re-granted, found %d grants", grants)
	}
}

// TestProvisionRefusesWithoutAProvider is the unconfigured control plane: not
// a refusal of the caller, and distinguishable from one, because the route
// answers it 501 rather than 4xx.
func TestProvisionRefusesWithoutAProvider(t *testing.T) {
	fixture := newMachineIdentityFixture(t)
	// A literal nil, not a typed nil fake: an interface holding a typed nil
	// would not compare equal to nil and would be reached as a configured
	// provider.
	fixture.service = NewMachineIdentityService(fixture.env, fixture.users, fixture.roles, fixture.issuers, nil)

	if _, err := fixture.service.Provision(fixture.ctx, "env-1"); !errors.Is(err, ErrMachineIdentityProviderUnavailable) {
		t.Fatalf("expected ErrMachineIdentityProviderUnavailable, got %v", err)
	}
}

// TestProvisionRefusesATenantWithNoOrgScopedIssuer: without an organization
// the platform administers, there is nowhere to create the identity, and
// minting one somewhere else would be the attribution collapse this feature
// exists to remove.
func TestProvisionRefusesATenantWithNoOrgScopedIssuer(t *testing.T) {
	fixture := newMachineIdentityFixture(t)
	fixture.issuers.issuers = []model.TenantIssuer{
		{TenantID: "tenant-1", Issuer: "https://byo.example"},
	}

	_, err := fixture.service.Provision(fixture.ctx, "env-1")
	if !errors.Is(err, ErrMachineIdentityUnavailable) {
		t.Fatalf("expected ErrMachineIdentityUnavailable, got %v", err)
	}
	if len(fixture.admin.calls) != 0 {
		t.Fatalf("expected no provider call, got %+v", fixture.admin.calls)
	}
}

// TestProvisionRefusesAmbiguousOrgScopedIssuers: two org-scoped mappings is a
// question the caller did not answer, and picking one by list order would put
// the identity under whichever happened to sort first.
func TestProvisionRefusesAmbiguousOrgScopedIssuers(t *testing.T) {
	fixture := newMachineIdentityFixture(t)
	fixture.issuers.issuers = []model.TenantIssuer{
		{TenantID: "tenant-1", Issuer: "https://a.example", OrgFieldValue: "org-a"},
		{TenantID: "tenant-1", Issuer: "https://b.example", OrgFieldValue: "org-b"},
	}

	_, err := fixture.service.Provision(fixture.ctx, "env-1")
	if !errors.Is(err, ErrMachineIdentityUnavailable) {
		t.Fatalf("expected ErrMachineIdentityUnavailable, got %v", err)
	}
	if len(fixture.admin.calls) != 0 {
		t.Fatalf("expected no provider call, got %+v", fixture.admin.calls)
	}
}

// TestProvisionUsesTheEnvironmentsOwnIDForTheIdentityName: the derived name is
// the only link between an environment and the identity it already has, so two
// environments must never derive the same one and one environment must always
// derive the same one.
func TestProvisionUsesTheEnvironmentsOwnIDForTheIdentityName(t *testing.T) {
	if MachineIdentityLoginName("env-a") == MachineIdentityLoginName("env-b") {
		t.Fatal("two environments derived the same identity name")
	}
	// Derived from the id rather than the name, and trimmed: the id is what is
	// stable and unique, and a padded one must not derive a different identity
	// than the same id unpadded.
	if got, want := MachineIdentityLoginName(" env-a "), "erun-env-env-a"; got != want {
		t.Fatalf("MachineIdentityLoginName(%q) = %q, want %q", " env-a ", got, want)
	}
}
