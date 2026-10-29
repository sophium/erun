package backendapi

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

const machineIdentityE2EOrgFieldKey = "urn:zitadel:iam:user:resourceowner:id"

// Idempotency rests on SQL contracts (unique index, ON CONFLICT, transactions), so
// this runs against real PostgreSQL with only the identity provider stubbed.
func machineIdentityDatabase(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	databaseURL := testpg.URL(t)
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	stamp := time.Now().Format("20060102150405.000000")
	tenantName := "machine-identity-e2e-" + stamp
	issuer := "https://issuer.example/" + tenantName

	var tenantID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`,
		tenantName,
	).Scan(&tenantID), "seed tenant")
	// Machine identities are org-scoped only; the issuer key is what makes the
	// mapping org-scoped.
	mustNoErr(t, db.QueryRow(
		`INSERT INTO issuers (issuer, org_field_key) VALUES ($1, $2)`,
		issuer, machineIdentityE2EOrgFieldKey,
	).Err(), "seed issuer")
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenant_issuers (tenant_id, issuer, org_field_value, name) VALUES ($1, $2, $3, $4)`,
		tenantID, issuer, tenantName+"-org", tenantName,
	).Err(), "seed tenant issuer mapping")

	t.Cleanup(func() {
		for _, table := range []string{"environments", "user_external_ids", "user_roles", "role_permissions", "roles", "users", "tenant_issuers"} {
			if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
				t.Logf("clearing %s for tenant %s: %v", table, tenantID, err)
			}
		}
		if _, err := db.Exec(`DELETE FROM issuers WHERE issuer = $1`, issuer); err != nil {
			t.Logf("clearing issuers for %s: %v", issuer, err)
		}
		if _, err := db.Exec(`DELETE FROM tenants WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing tenants for %s: %v", tenantID, err)
		}
	})
	return db, tenantID, issuer
}

// fakeE2EMachineIdentityAdmin returns the existing identity on a repeat login name,
// as the provider contract requires.
type fakeE2EMachineIdentityAdmin struct {
	identities map[string]zitadel.MachineIdentity
}

func (f *fakeE2EMachineIdentityAdmin) EnsureMachineIdentity(_ context.Context, params zitadel.EnsureMachineIdentityParams) (zitadel.MachineIdentity, error) {
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

func (f *fakeE2EMachineIdentityAdmin) DeleteMachineIdentity(_ context.Context, params zitadel.DeleteMachineIdentityParams) (bool, error) {
	if _, ok := f.identities[params.LoginName]; !ok {
		return false, nil
	}
	delete(f.identities, params.LoginName)
	return true, nil
}

type machineIdentityFixtureE2E struct {
	service *service.MachineIdentityService
	admin   *fakeE2EMachineIdentityAdmin
	envs    *repository.EnvironmentRepository
	db      *sql.DB
	ctx     context.Context
	tenant  string
}

func newMachineIdentityFixtureE2E(t *testing.T) *machineIdentityFixtureE2E {
	t.Helper()
	db, tenantID, _ := machineIdentityDatabase(t)
	txs := repository.NewTxManager(db, repository.DialectPostgres)
	admin := &fakeE2EMachineIdentityAdmin{identities: map[string]zitadel.MachineIdentity{}}
	envs := repository.NewEnvironmentRepository(txs)
	return &machineIdentityFixtureE2E{
		service: service.NewMachineIdentityService(
			envs,
			repository.NewUserRepository(txs),
			repository.NewRoleRepository(txs),
			repository.NewTenantIssuerRepository(txs),
			admin,
		),
		admin:  admin,
		envs:   envs,
		db:     db,
		tenant: tenantID,
		ctx: security.WithContext(context.Background(), security.Context{
			TenantID:   tenantID,
			TenantType: string(model.TenantTypeCompany),
			ErunUserID: "provisioning-operator",
		}),
	}
}

func (f *machineIdentityFixtureE2E) seedEnvironment(t *testing.T, name string) model.Environment {
	t.Helper()
	created, err := f.envs.Create(f.ctx, model.Environment{Name: name, Type: model.EnvironmentTypeRemoteAgent})
	mustNoErr(t, err, "seed environment "+name)
	return created
}

func (f *machineIdentityFixtureE2E) counts(t *testing.T, username string) (users, externalIDs, grants int) {
	t.Helper()
	mustNoErr(t, f.db.QueryRow(
		`SELECT count(*) FROM users WHERE tenant_id = $1 AND username = $2`, f.tenant, username,
	).Scan(&users), "count users")
	mustNoErr(t, f.db.QueryRow(`
		SELECT count(*) FROM user_external_ids uei
		  JOIN users u ON u.tenant_id = uei.tenant_id AND u.user_id = uei.user_id
		 WHERE u.tenant_id = $1 AND u.username = $2
	`, f.tenant, username).Scan(&externalIDs), "count external identity mappings")
	mustNoErr(t, f.db.QueryRow(`
		SELECT count(*) FROM user_roles ur
		  JOIN users u ON u.tenant_id = ur.tenant_id AND u.user_id = ur.user_id
		 WHERE u.tenant_id = $1 AND u.username = $2
	`, f.tenant, username).Scan(&grants), "count role assignments")
	return users, externalIDs, grants
}

func TestMachineIdentityProvisioningIsIdempotent(t *testing.T) {
	fixture := newMachineIdentityFixtureE2E(t)
	environment := fixture.seedEnvironment(t, "alpha")

	first := fixture.provision(t, environment)

	second, err := fixture.service.Provision(fixture.ctx, environment.EnvironmentID)
	if err != nil {
		t.Fatalf("second Provision: %v", err)
	}
	if !second.AlreadyEnrolled {
		t.Fatalf("second provisioning did not report the existing enrollment: %+v", second)
	}
	if second.UserID != first.UserID || second.ClientID != first.ClientID || second.ClientSecret != first.ClientSecret {
		t.Fatalf("second provisioning produced a different identity: %+v vs %+v", second, first)
	}
	if len(fixture.admin.identities) != 1 {
		t.Fatalf("expected one identity in the provider, got %d", len(fixture.admin.identities))
	}

	username := service.MachineIdentityLoginName(environment.EnvironmentID)
	users, externalIDs, grants := fixture.counts(t, username)
	if users != 1 || externalIDs != 1 || grants != 1 {
		t.Fatalf("after two provisioning calls: users=%d externalIds=%d grants=%d, want 1/1/1", users, externalIDs, grants)
	}
	fixture.assertHoldsOnlyTheMachineRole(t, username)
}

func (f *machineIdentityFixtureE2E) provision(t *testing.T, environment model.Environment) service.MachineIdentityResult {
	t.Helper()
	result, err := f.service.Provision(f.ctx, environment.EnvironmentID)
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	if result.AlreadyEnrolled {
		t.Fatalf("first provisioning reported an existing enrollment: %+v", result)
	}
	if result.ClientSecret == "" || result.Subject == "" {
		t.Fatalf("first provisioning returned no usable credential: %+v", result)
	}
	return result
}

// The enrollment default is a different role, so this proves the grant came from provisioning.
func (f *machineIdentityFixtureE2E) assertHoldsOnlyTheMachineRole(t *testing.T, username string) {
	t.Helper()
	var roleName string
	mustNoErr(t, f.db.QueryRow(`
		SELECT ro.name FROM user_roles ur
		  JOIN roles ro ON ro.tenant_id = ur.tenant_id AND ro.role_id = ur.role_id
		  JOIN users u ON u.tenant_id = ur.tenant_id AND u.user_id = ur.user_id
		 WHERE u.tenant_id = $1 AND u.username = $2
	`, f.tenant, username).Scan(&roleName), "read granted role")
	if roleName != repository.TenantAgentRoleName {
		t.Fatalf("identity held role %q, want %q", roleName, repository.TenantAgentRoleName)
	}
}

func TestMachineIdentityProvisioningGivesEachEnvironmentItsOwnIdentity(t *testing.T) {
	fixture := newMachineIdentityFixtureE2E(t)
	alpha := fixture.seedEnvironment(t, "alpha")
	beta := fixture.seedEnvironment(t, "beta")

	firstAlpha, err := fixture.service.Provision(fixture.ctx, alpha.EnvironmentID)
	if err != nil {
		t.Fatalf("Provision(alpha): %v", err)
	}
	firstBeta, err := fixture.service.Provision(fixture.ctx, beta.EnvironmentID)
	if err != nil {
		t.Fatalf("Provision(beta): %v", err)
	}

	if firstAlpha.ClientID == firstBeta.ClientID || firstAlpha.UserID == firstBeta.UserID {
		t.Fatalf("two environments shared one identity: %+v and %+v", firstAlpha, firstBeta)
	}
	if len(fixture.admin.identities) != 2 {
		t.Fatalf("expected one identity per environment, got %d", len(fixture.admin.identities))
	}
	for _, environment := range []model.Environment{alpha, beta} {
		users, externalIDs, grants := fixture.counts(t, service.MachineIdentityLoginName(environment.EnvironmentID))
		if users != 1 || externalIDs != 1 || grants != 1 {
			t.Fatalf("%s: users=%d externalIds=%d grants=%d, want 1/1/1", environment.Name, users, externalIDs, grants)
		}
	}
}

func TestMachineIdentityProvisioningRestoresARevokedGrant(t *testing.T) {
	fixture := newMachineIdentityFixtureE2E(t)
	environment := fixture.seedEnvironment(t, "alpha")

	first, err := fixture.service.Provision(fixture.ctx, environment.EnvironmentID)
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	_, err = fixture.db.Exec(`DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, fixture.tenant, first.UserID)
	mustNoErr(t, err, "revoke the machine role")

	if _, err := fixture.service.Provision(fixture.ctx, environment.EnvironmentID); err != nil {
		t.Fatalf("second Provision: %v", err)
	}

	username := service.MachineIdentityLoginName(environment.EnvironmentID)
	users, externalIDs, grants := fixture.counts(t, username)
	if users != 1 || externalIDs != 1 || grants != 1 {
		t.Fatalf("after restoring a revoked grant: users=%d externalIds=%d grants=%d, want 1/1/1", users, externalIDs, grants)
	}
}

// TestMachineIdentityRevocationLeavesNothingBehind: revoking either side alone leaves a live
// credential or orphan rows, so every count is asserted.
func TestMachineIdentityRevocationLeavesNothingBehind(t *testing.T) {
	fixture := newMachineIdentityFixtureE2E(t)
	environment := fixture.seedEnvironment(t, "alpha")
	fixture.provision(t, environment)

	username := service.MachineIdentityLoginName(environment.EnvironmentID)
	if users, externalIDs, grants := fixture.counts(t, username); users != 1 || externalIDs != 1 || grants != 1 {
		t.Fatalf("before revocation: users=%d externalIds=%d grants=%d, want 1/1/1", users, externalIDs, grants)
	}

	if err := fixture.service.Revoke(fixture.ctx, environment.EnvironmentID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if len(fixture.admin.identities) != 0 {
		t.Fatalf("the provider still holds %d identities: the credential can still mint tokens", len(fixture.admin.identities))
	}
	users, externalIDs, grants := fixture.counts(t, username)
	if users != 0 || externalIDs != 0 || grants != 0 {
		t.Fatalf("after revocation: users=%d externalIds=%d grants=%d, want 0/0/0", users, externalIDs, grants)
	}
}

func TestMachineIdentityRevocationIsIdempotentAgainstRealSQL(t *testing.T) {
	fixture := newMachineIdentityFixtureE2E(t)
	environment := fixture.seedEnvironment(t, "alpha")
	fixture.provision(t, environment)

	if err := fixture.service.Revoke(fixture.ctx, environment.EnvironmentID); err != nil {
		t.Fatalf("first Revoke: %v", err)
	}
	if err := fixture.service.Revoke(fixture.ctx, environment.EnvironmentID); err != nil {
		t.Fatalf("second Revoke: %v", err)
	}

	username := service.MachineIdentityLoginName(environment.EnvironmentID)
	if users, externalIDs, grants := fixture.counts(t, username); users != 0 || externalIDs != 0 || grants != 0 {
		t.Fatalf("after two revocations: users=%d externalIds=%d grants=%d, want 0/0/0", users, externalIDs, grants)
	}
}

func TestMachineIdentityRevocationLeavesOtherEnvironmentsAlone(t *testing.T) {
	fixture := newMachineIdentityFixtureE2E(t)
	alpha := fixture.seedEnvironment(t, "alpha")
	beta := fixture.seedEnvironment(t, "beta")
	fixture.provision(t, alpha)
	fixture.provision(t, beta)

	if err := fixture.service.Revoke(fixture.ctx, alpha.EnvironmentID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	betaUsername := service.MachineIdentityLoginName(beta.EnvironmentID)
	if users, externalIDs, grants := fixture.counts(t, betaUsername); users != 1 || externalIDs != 1 || grants != 1 {
		t.Fatalf("the sibling environment was revoked too: users=%d externalIds=%d grants=%d, want 1/1/1", users, externalIDs, grants)
	}
	if len(fixture.admin.identities) != 1 {
		t.Fatalf("expected exactly the sibling's identity to remain, got %d", len(fixture.admin.identities))
	}
}
