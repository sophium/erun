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

// fakeMachineIdentityAdmin returns the existing identity on a repeat call, so a
// test can assert that a second provisioning minted nothing new.
type fakeMachineIdentityAdmin struct {
	identities map[string]zitadel.MachineIdentity
	ensureErr  error
	calls      []zitadel.EnsureMachineIdentityParams
	// deleteCalls includes revocations that found nothing.
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

// DeleteMachineIdentity reports a missing identity as nothing removed, not an error.
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

func (f *fakeMachineIdentityAdmin) hasIdentity(loginName string) bool {
	_, ok := f.identities[loginName]
	return ok
}

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

// fakeMachineIdentityUsers treats an already-mapped identity as a no-op.
type fakeMachineIdentityUsers struct {
	bySubject   map[string]model.User
	grants      map[string]map[string]bool
	createErr   error
	deleteErr   error
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

// DeleteByUsername removes the row and its grants; a missing name is nothing removed.
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

func (f *fakeMachineIdentityUsers) enrolledIdentities() int {
	return len(f.bySubject)
}

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

type machineIdentityFixture struct {
	service *MachineIdentityService
	admin   *fakeMachineIdentityAdmin
	users   *fakeMachineIdentityUsers
	roles   *fakeMachineIdentityRoles
	issuers *fakeMachineIdentityIssuers
	env     *fakeMachineIdentityEnvironments
	// The service names the tenant explicitly because an operations session bypasses RLS.
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
		// Revocation scopes to the row's tenant, not the session's: the delete workflow
		// may run under an operations caller.
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

// Removing only one half would leave either a stale user or a live client secret.
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
	if len(fixture.users.deleteCalls) != 1 {
		t.Fatalf("expected the user store to be asked to delete once, got %+v", fixture.users.deleteCalls)
	}
	if call := fixture.users.deleteCalls[0]; call.tenantID != "tenant-1" || call.username != loginName {
		t.Fatalf("user deletion = %+v, want tenant-1/%s", call, loginName)
	}
}

// The environment-delete workflow retries from the top, so revocation must be idempotent.
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

// The user row is found via the provider's client id, so erun-side rows go
// first; deleting the application first would strand the row on failure.
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

// Unlike provisioning, revocation must not refuse ambiguous issuers: it searches them all.
func TestRevokeSearchesEveryOrgScopedIssuer(t *testing.T) {
	fixture := newMachineIdentityFixture(t)
	if _, err := fixture.service.Provision(fixture.ctx, "env-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
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

// The erun user row is what makes a token unusable, so it goes even without a provider.
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

func TestProvisionRegrantsTheMachineRoleToAnIdentityThatLostIt(t *testing.T) {
	fixture := newMachineIdentityFixture(t)
	if _, err := fixture.service.Provision(fixture.ctx, "env-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
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

// An unconfigured provider is answered 501, distinct from a caller refusal.
func TestProvisionRefusesWithoutAProvider(t *testing.T) {
	fixture := newMachineIdentityFixture(t)
	// A literal nil: a typed nil would read as a configured provider.
	fixture.service = NewMachineIdentityService(fixture.env, fixture.users, fixture.roles, fixture.issuers, nil)

	if _, err := fixture.service.Provision(fixture.ctx, "env-1"); !errors.Is(err, ErrMachineIdentityProviderUnavailable) {
		t.Fatalf("expected ErrMachineIdentityProviderUnavailable, got %v", err)
	}
}

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

// Picking one by list order would be arbitrary.
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

func TestProvisionUsesTheEnvironmentsOwnIDForTheIdentityName(t *testing.T) {
	if MachineIdentityLoginName("env-a") == MachineIdentityLoginName("env-b") {
		t.Fatal("two environments derived the same identity name")
	}
	// A padded id must derive the same identity as the unpadded one.
	if got, want := MachineIdentityLoginName(" env-a "), "erun-env-env-a"; got != want {
		t.Fatalf("MachineIdentityLoginName(%q) = %q, want %q", " env-a ", got, want)
	}
}
