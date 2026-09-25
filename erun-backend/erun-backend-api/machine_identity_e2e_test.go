package backendapi

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

// machineIdentityE2EOrgFieldKey is the org claim the erun-shipped Zitadel
// asserts, and the one an org-scoped issuer mapping is written against here.
const machineIdentityE2EOrgFieldKey = "urn:zitadel:iam:user:resourceowner:id"

// Provisioning an environment's machine identity is only idempotent if the
// writes it makes are: the user row is looked up by (issuer, subject) before
// it is inserted, and the role grant converges rather than inserting twice.
// Both are SQL contracts — a unique index, an ON CONFLICT, and a transaction
// that either commits whole or not at all — so the property is exercised
// against a real migrated PostgreSQL rather than a fake that agrees with
// itself. Needs only the database, no cluster and no identity provider: the
// provider is the one dependency that is genuinely external, and stubbing it
// is what lets the erun-side writes be counted.
func machineIdentityDatabase(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_MACHINE_IDENTITY_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_MACHINE_IDENTITY_DATABASE_URL to a migrated PostgreSQL")
	}
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
	// Org-scoped, because that is the only shape a machine identity can be
	// created under: the organization value is what makes it this tenant's.
	// The key on the shared issuers row is what makes the mapping org-scoped at
	// all — an org value under a single-tenant issuer is read by nothing, and
	// the write below is refused outright in that state.
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

// fakeE2EMachineIdentityAdmin is the identity provider, stubbed so the test
// counts what erun wrote rather than what the provider minted. It holds one
// identity per login name and returns the one it already has for a repeat
// call, which is the behaviour the provider contract requires and the reason a
// second provisioning call is a no-op rather than a second identity.
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

// machineIdentityFixtureE2E wires the real repositories onto a migrated
// database with the provider stubbed.
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

// counts reports the three rows one identity is allowed to have: its user row,
// its external-identity mapping, and its role assignment. A second
// provisioning call that created any of them again would show up here as a
// count of two.
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

// TestMachineIdentityProvisioningIsIdempotent is the demonstration the whole
// design rests on, against a real database: provisioning one environment twice
// creates one user row, one external-identity mapping, and one grant — and the
// second call reports the identity that already existed rather than minting a
// second one.
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

// provision runs a first provisioning and asserts the state that call itself
// has to be in: a usable credential, and an identity the platform did not
// already have.
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

// assertHoldsOnlyTheMachineRole reads the granted role straight out of the
// database: the enrollment default would have been a different one, so this is
// what proves the grant came from provisioning rather than from enrollment.
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

// TestMachineIdentityProvisioningGivesEachEnvironmentItsOwnIdentity is the
// point of provisioning per environment rather than per tenant: two
// environments of one tenant must not share an identity, or the attribution
// this feature exists to add collapses back to one actor.
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

// TestMachineIdentityProvisioningRestoresARevokedGrant is the reconcile half
// against real SQL: an identity that already exists but has lost the machine
// role — revoked, or enrolled before the role shipped — is not left holding
// nothing while provisioning reports success. Without this the environment
// would keep a credential that every call refuses.
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
