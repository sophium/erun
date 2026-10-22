package service

import (
	"context"
	"errors"
	"strings"
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
	// deleteCalls records every revocation attempt, including the ones that
	// found nothing, so a test can tell "revoked the identity" from "searched
	// the right places and found none".
	deleteCalls []zitadel.DeleteMachineIdentityParams
	deleteErr   error
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

// DeleteMachineIdentity reproduces the provider's find-first delete: what is
// not there is reported as nothing removed, not as an error, so a test can
// assert on what is left rather than only on which call was made.
func (f *fakeMachineIdentityAdmin) DeleteMachineIdentity(_ context.Context, params zitadel.DeleteMachineIdentityParams) (bool, error) {
	f.deleteCalls = append(f.deleteCalls, params)
	if f.deleteErr != nil {
		return false, f.deleteErr
	}
	if _, ok := f.identities[params.LoginName]; !ok {
		return false, nil
	}
	delete(f.identities, params.LoginName)
	return true, nil
}

// hasIdentity reports whether the provider still holds an identity under
// loginName — the credential half of "nothing usable left behind".
func (f *fakeMachineIdentityAdmin) hasIdentity(loginName string) bool {
	_, ok := f.identities[loginName]
	return ok
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
	deleteErr error
	// deleteCalls records what revocation asked the store to remove, so a test
	// can assert the service scoped it to the environment's own tenant and to
	// the name derived from the environment's id rather than to the session's.
	deleteCalls []deleteByUsernameCall
}

type deleteByUsernameCall struct {
	tenantID string
	username string
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

// DeleteByUsername mirrors UserRepository.DeleteByUsername's contract: the
// derived name is what finds the row, and removing the user removes the
// identity's whole erun-side footprint — the row and the grants that pointed
// at it. A name that is not there is reported as nothing removed, which is what
// makes a second revocation a no-op.
func (f *fakeMachineIdentityUsers) DeleteByUsername(_ context.Context, tenantID string, username string) (bool, error) {
	f.deleteCalls = append(f.deleteCalls, deleteByUsernameCall{tenantID: tenantID, username: username})
	if f.deleteErr != nil {
		return false, f.deleteErr
	}
	for subject, user := range f.bySubject {
		if user.Username != username {
			continue
		}
		delete(f.bySubject, subject)
		delete(f.grants, user.UserID)
		return true, nil
	}
	return false, nil
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
		// TenantID is what the environment row actually carries, and what
		// revocation scopes the erun-side removal to: it is read off the row
		// rather than the session, because the delete workflow may be running
		// under an operations caller's own tenant.
		env: &fakeMachineIdentityEnvironments{environment: model.Environment{EnvironmentID: "env-1", TenantID: "tenant-1", Name: "alpha"}},
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

// TestRevokeRemovesTheIdentityTheUserRowAndTheGrant is the whole of what a
// provisioned identity is, removed: the provider application that mints the
// credential, the erun user a token from it resolves to, and the role grant
// that would otherwise still name a user that is gone.
//
// Removing only the provider application would leave an erun user behind, and
// removing only the user would leave a live client secret minting tokens — the
// half-measure that makes a revoked identity look revoked while it is not. This
// asserts both halves together.
func TestRevokeRemovesTheIdentityTheUserRowAndTheGrant(t *testing.T) {
	fixture := newMachineIdentityFixture(t)
	provisioned, err := fixture.service.Provision(fixture.ctx, "env-1")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	assertHoldsExactlyTheMachineRole(t, fixture, provisioned.UserID)

	if err := fixture.service.Revoke(fixture.ctx, "env-1"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	loginName := MachineIdentityLoginName("env-1")
	if fixture.admin.hasIdentity(loginName) {
		t.Fatal("the provider still holds the identity: its client secret can still mint tokens")
	}
	if enrolled := fixture.users.enrolledIdentities(); enrolled != 0 {
		t.Fatalf("expected no enrolled identity after revocation, got %d", enrolled)
	}
	if grants := len(fixture.users.grants[provisioned.UserID]); grants != 0 {
		t.Fatalf("expected the user's grants to go with it, got %d", grants)
	}
	// The erun-side removal is scoped to the environment's own tenant and to
	// the name derived from the environment's id: it runs behind the
	// environment-delete workflow, where the session's tenant may be an
	// operations caller's rather than the one that owns the environment.
	if len(fixture.users.deleteCalls) != 1 {
		t.Fatalf("expected the user store to be asked to delete once, got %+v", fixture.users.deleteCalls)
	}
	if call := fixture.users.deleteCalls[0]; call.tenantID != "tenant-1" || call.username != loginName {
		t.Fatalf("user deletion = %+v, want tenant-1/%s", call, loginName)
	}
}

// TestRevokeIsIdempotentAndToleratesAnEnvironmentThatNeverHadAnIdentity: the
// revocation runs inside the environment-delete workflow, which retries an
// attempt that failed partway from the top, so a second call must be the state
// asked for rather than a conflict — including for the common environment that
// was never provisioned a machine identity at all.
func TestRevokeIsIdempotentAndToleratesAnEnvironmentThatNeverHadAnIdentity(t *testing.T) {
	fixture := newMachineIdentityFixture(t)

	if err := fixture.service.Revoke(fixture.ctx, "env-1"); err != nil {
		t.Fatalf("revoking an environment that never had an identity: %v", err)
	}
	if len(fixture.admin.deleteCalls) != 1 {
		t.Fatalf("expected the provider to be searched once, got %+v", fixture.admin.deleteCalls)
	}
	if _, err := fixture.service.Provision(fixture.ctx, "env-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if err := fixture.service.Revoke(fixture.ctx, "env-1"); err != nil {
		t.Fatalf("first Revoke: %v", err)
	}
	if err := fixture.service.Revoke(fixture.ctx, "env-1"); err != nil {
		t.Fatalf("second Revoke: %v", err)
	}
	if enrolled := fixture.users.enrolledIdentities(); enrolled != 0 {
		t.Fatalf("expected no enrolled identity after two revocations, got %d", enrolled)
	}
}

// TestRevokeLeavesTheUserRowIntactWhenTheProviderCannotBeReached is the
// ordering property that keeps a partial failure recoverable: the erun-side
// rows are removed first and the provider application last, because the user
// row is found by the client id the provider holds. A revocation that deleted
// the application first and then failed would have lost the only thing that
// names the user row, stranding it — and re-running would find nothing.
func TestRevokeLeavesTheUserRowIntactWhenTheProviderCannotBeReached(t *testing.T) {
	fixture := newMachineIdentityFixture(t)
	if _, err := fixture.service.Provision(fixture.ctx, "env-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	fixture.admin.deleteErr = errors.New("identity provider unreachable")

	if err := fixture.service.Revoke(fixture.ctx, "env-1"); err == nil {
		t.Fatal("expected an error when the provider cannot be reached")
	}
	if !fixture.admin.hasIdentity(MachineIdentityLoginName("env-1")) {
		t.Fatal("the provider application was lost, so a retry can no longer find the user row it names")
	}
}

// TestRevokeSearchesEveryOrgScopedIssuer: provisioning refuses a tenant with
// several org-scoped issuers rather than guessing which organization an
// identity belongs to. Revocation has the opposite job — it must not refuse,
// because refusing leaves the identity alive exactly when the tenant's issuer
// configuration has drifted — so it searches every one of them and removes
// whatever it finds.
func TestRevokeSearchesEveryOrgScopedIssuer(t *testing.T) {
	fixture := newMachineIdentityFixture(t)
	// Provisioned while the tenant resolved by exactly one org-scoped issuer,
	// which is the only state provisioning permits.
	if _, err := fixture.service.Provision(fixture.ctx, "env-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	// And revoked after the tenant's mappings grew a second org-scoped entry
	// and a single-tenant one — the drift that used to be indistinguishable
	// from "there is nothing to revoke".
	fixture.issuers.issuers = []model.TenantIssuer{
		{TenantID: "tenant-1", Issuer: "https://auth.example", OrgFieldValue: "org-1"},
		{TenantID: "tenant-1", Issuer: "https://auth.example", OrgFieldValue: "org-2"},
		{TenantID: "tenant-1", Issuer: "https://auth.example", OrgFieldValue: ""},
	}

	if err := fixture.service.Revoke(fixture.ctx, "env-1"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	searched := make([]string, 0, len(fixture.admin.deleteCalls))
	for _, call := range fixture.admin.deleteCalls {
		searched = append(searched, call.OrgID)
	}
	if strings.Join(searched, ",") != "org-1,org-2" {
		t.Fatalf("provider orgs searched = %v, want every org-scoped issuer and no single-tenant one", searched)
	}
}

// TestRevokeWithoutAProviderStillRemovesTheERunSide: a control plane that
// administers no identity provider never minted an application, but the erun
// user row is still the platform's own to remove — and it is what makes a token
// unusable here, so leaving it because the provider is unconfigured would be
// the wrong half to skip.
func TestRevokeWithoutAProviderStillRemovesTheERunSide(t *testing.T) {
	fixture := newMachineIdentityFixture(t)
	if _, err := fixture.service.Provision(fixture.ctx, "env-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	withoutProvider := NewMachineIdentityService(fixture.env, fixture.users, fixture.roles, fixture.issuers, nil)

	if err := withoutProvider.Revoke(fixture.ctx, "env-1"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if enrolled := fixture.users.enrolledIdentities(); enrolled != 0 {
		t.Fatalf("expected the user row to be removed even with no provider configured, got %d", enrolled)
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
