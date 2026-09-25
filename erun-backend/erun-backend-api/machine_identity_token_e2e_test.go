package backendapi

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
	eruncommon "github.com/sophium/erun/erun-common"
)

// This file closes the two proof gaps an audit of the landed machine-identity
// work found in its own validation list: nothing drove a client_credentials
// token through IdentityRepository.ResolveTenantByIssuer, and nothing drove
// `erun build`'s unattached self-report as the identity an environment now
// holds. Both are gaps in proof, not in behaviour -- the tests below are the
// artifact behind the "verified go/no-go" the module guidance asserts.
//
// The scenario is the real chain, with exactly one thing replaced. The tenant's
// IdP is a double (machineIdentityTokenIssuer: real OIDC discovery, real JWKS,
// real RS256 signing, real client_credentials endpoint), and everything on
// erun's side of the wire is production code: the real client_credentials grant
// (CloudProviderBearerToken), the real bearer verifier (JWKS-verified, no test
// seam), the real identity resolver against a real migrated PostgreSQL, and the
// real HTTP handler with its real database-backed authorizer.
//
// What the double decides, and therefore what these tests do NOT establish:
// whether Zitadel's own token endpoint honours urn:zitadel:iam:user:resourceowner
// on a client_credentials grant for a machine user. That is the question scope 1
// left unestablished, and a stub cannot answer it -- it is the stub's own
// behaviour. What is established here is the other half of the same question,
// the half that lives in erun's source: the grant asks for that scope, and the
// outcome is fully determined by whether the issuer honours it -- resolving the
// tenant when it does, and refusing with a named tenant-unresolved error when it
// does not. Both directions are pinned below, so a future change to either
// cannot land silently.

const (
	// machineIdentityTokenOrgClaimKey is the claim the erun-shipped Zitadel
	// asserts and the one an org-scoped issuers.org_field_key names
	// (bootstrapOrgScopeClaims in internal/repository/identity.go).
	machineIdentityTokenOrgClaimKey = "urn:zitadel:iam:user:resourceowner:id"
	// machineIdentityTokenOrgScope is the scope that claim needs.
	machineIdentityTokenOrgScope = "urn:zitadel:iam:user:resourceowner"

	machineIdentityTokenOrg         = "386994597030592700"
	machineIdentityTokenAlias       = "team-platform"
	machineIdentityTokenEnvironment = "machine-token-env"
)

// machineIdentityTokenGrant is one token request the issuer double answered.
type machineIdentityTokenGrant struct {
	GrantType   string
	Scope       string
	BasicUser   string
	BasicSecret string
	HadBasic    bool
}

// machineIdentityTokenIssuer is the tenant's own IdP: mockOIDCProvider's
// discovery document and JWKS, plus the client_credentials token endpoint a
// provisioned machine identity authenticates at.
//
// It models the one behaviour under test rather than implementing it: Zitadel's
// urn:zitadel:iam:user:resourceowner scope asserts the resource owner of the
// authenticated user, and a Zitadel machine user is a user inside an
// organization -- so a client_credentials token carries the resourceowner id
// exactly when the grant asked for the scope. That is the model, and it is the
// assumption a live instance would have to confirm.
type machineIdentityTokenIssuer struct {
	*mockOIDCProvider
	// refuseOrgScope answers invalid_scope to any grant naming the org scope,
	// once. It is a tenant's own BYO issuer, which has never heard of the scope
	// and is the case erun's retry exists for.
	refuseOrgScope bool
	// org is the resource owner a machine token carries when the scope was
	// granted.
	org string

	mu       sync.Mutex
	clientID string
	secret   string
	requests []machineIdentityTokenGrant
}

func newMachineIdentityTokenIssuer(t *testing.T, refuseOrgScope bool) *machineIdentityTokenIssuer {
	t.Helper()
	issuer := &machineIdentityTokenIssuer{org: machineIdentityTokenOrg, refuseOrgScope: refuseOrgScope}
	issuer.mockOIDCProvider = newMockOIDCProviderWithRoutes(t, func(provider *mockOIDCProvider, mux *http.ServeMux) {
		provider.tokenEndpoint = "/oauth/token"
		mux.HandleFunc(provider.tokenEndpoint, issuer.handleClientCredentials)
	})
	return issuer
}

// expectCredential records the credential the platform minted, so the token
// endpoint answers only the identity that was actually provisioned rather than
// any caller that reaches it.
func (i *machineIdentityTokenIssuer) expectCredential(clientID, secret string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.clientID = clientID
	i.secret = secret
}

func (i *machineIdentityTokenIssuer) handleClientCredentials(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	user, secret, hadBasic := r.BasicAuth()
	grant := machineIdentityTokenGrant{
		GrantType:   r.PostForm.Get("grant_type"),
		Scope:       r.PostForm.Get("scope"),
		BasicUser:   user,
		BasicSecret: secret,
		HadBasic:    hadBasic,
	}
	i.mu.Lock()
	i.requests = append(i.requests, grant)
	expectedID, expectedSecret := i.clientID, i.secret
	i.mu.Unlock()

	// The credential travels in the Authorization header, matching the
	// API_AUTH_METHOD_TYPE_BASIC application erun provisions -- the two have to
	// agree, so an issuer that accepted the secret in the body would not be a
	// model of anything.
	if grant.GrantType != "client_credentials" || !hadBasic || user != expectedID || secret != expectedSecret {
		writeOIDCError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	if i.refuseOrgScope && strings.Contains(grant.Scope, machineIdentityTokenOrgScope) {
		writeOIDCError(w, http.StatusBadRequest, "invalid_scope")
		return
	}

	now := time.Now()
	claims := map[string]any{
		"iss": i.issuer(),
		// A Zitadel machine user's own id is its client id, which is the
		// subject MachineIdentityService records when it enrols the identity
		// (service.MachineIdentityResult.Subject).
		"sub": user,
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
	if strings.Contains(grant.Scope, machineIdentityTokenOrgScope) {
		claims[machineIdentityTokenOrgClaimKey] = i.org
	}
	token, err := i.signClaims(claims)
	if err != nil {
		http.Error(w, "sign machine token: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeOIDCJSON(w, map[string]any{"access_token": token, "expires_in": 3600})
}

func (i *machineIdentityTokenIssuer) grantLog() []machineIdentityTokenGrant {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]machineIdentityTokenGrant(nil), i.requests...)
}

func writeOIDCError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	writeOIDCJSON(w, map[string]string{"error": code})
}

// staticCloudReadStore is the cloud config reader the grant resolves its alias
// through: one provider, already saved, exactly as `erun init` leaves it.
type staticCloudReadStore struct {
	config eruncommon.ERunConfig
}

func (s staticCloudReadStore) LoadERunConfig() (eruncommon.ERunConfig, string, error) {
	return s.config, "", nil
}

// machineIdentityTokenFixture is one tenant whose only issuer mapping is
// org-scoped on the shipped resource-owner claim and pointed at the issuer
// double, plus an environment of that tenant and the machine identity the real
// provisioning path minted for it.
type machineIdentityTokenFixture struct {
	db            *sql.DB
	tenantID      string
	environmentID string
	identity      service.MachineIdentityResult
}

func newMachineIdentityTokenFixture(t *testing.T, issuerURL string) *machineIdentityTokenFixture {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_MACHINE_IDENTITY_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_MACHINE_IDENTITY_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	stamp := time.Now().Format("20060102150405.000000")
	name := "machine-token-e2e-" + stamp

	var tenantID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`, name,
	).Scan(&tenantID), "seed tenant")
	// Org-scoped, because that is the only shape a machine identity is created
	// under and the only shape whose resolution depends on the org claim this
	// whole scenario turns on.
	mustNoErr(t, db.QueryRow(
		`INSERT INTO issuers (issuer, org_field_key) VALUES ($1, $2)`,
		issuerURL, machineIdentityTokenOrgClaimKey,
	).Err(), "seed issuer")
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenant_issuers (tenant_id, issuer, org_field_value, name) VALUES ($1, $2, $3, $4)`,
		tenantID, issuerURL, machineIdentityTokenOrg, name,
	).Err(), "seed tenant issuer mapping")

	t.Cleanup(func() {
		// LIFO, so this runs before the tenant's own teardown: the builds and
		// audit events the HTTP scenario leaves behind reference the tenant and
		// its user.
		for _, table := range []string{"builds", "audit_events", "environments", "user_external_ids", "user_roles", "role_permissions", "roles", "users", "tenant_issuers"} {
			if _, err := db.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenantID); err != nil {
				t.Logf("clearing %s for tenant %s: %v", table, tenantID, err)
			}
		}
		if _, err := db.Exec(`DELETE FROM issuers WHERE issuer = $1`, issuerURL); err != nil {
			t.Logf("clearing issuers for %s: %v", issuerURL, err)
		}
		if _, err := db.Exec(`DELETE FROM tenants WHERE tenant_id = $1`, tenantID); err != nil {
			t.Logf("clearing tenants for %s: %v", tenantID, err)
		}
	})

	txs := repository.NewTxManager(db, repository.DialectPostgres)
	identityContext := security.WithContext(context.Background(), security.Context{
		TenantID:   tenantID,
		TenantType: string(model.TenantTypeCompany),
		ErunUserID: "provisioning-operator",
	})
	environments := repository.NewEnvironmentRepository(txs)
	environment, err := environments.Create(identityContext, model.Environment{Name: machineIdentityTokenEnvironment, Type: model.EnvironmentTypeRemoteAgent})
	mustNoErr(t, err, "seed environment")

	identities := service.NewMachineIdentityService(
		environments,
		repository.NewUserRepository(txs),
		repository.NewRoleRepository(txs),
		repository.NewTenantIssuerRepository(txs),
		&fakeE2EMachineIdentityAdmin{identities: map[string]zitadel.MachineIdentity{}},
	)
	identity, err := identities.Provision(identityContext, environment.EnvironmentID)
	mustNoErr(t, err, "provision the environment's machine identity")

	return &machineIdentityTokenFixture{
		db:            db,
		tenantID:      tenantID,
		environmentID: environment.EnvironmentID,
		identity:      identity,
	}
}

// mintToken runs the real client_credentials grant against the issuer double,
// through the same entry point `erun build` and every other platform call use
// to obtain a token, and returns the access token it minted.
func (f *machineIdentityTokenFixture) mintToken(t *testing.T, issuer *machineIdentityTokenIssuer) string {
	t.Helper()
	issuer.expectCredential(f.identity.ClientID, f.identity.ClientSecret)

	const secretRef = "erun/clientsecret/" + machineIdentityTokenAlias
	secrets := eruncommon.NewFileCloudSecretStore(t.TempDir())
	mustNoErr(t, secrets.SaveCloudSecret(secretRef, f.identity.ClientSecret), "save the provisioned client secret")

	store := staticCloudReadStore{config: eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{{
		Alias:         machineIdentityTokenAlias,
		Provider:      eruncommon.CloudProviderERun,
		OIDCIssuerURL: issuer.issuer(),
		ERun: &eruncommon.ERunProviderConfig{
			APIURL:          "http://platform.invalid",
			ClientID:        f.identity.ClientID,
			ClientSecretRef: secretRef,
		},
	}}}}
	token, err := eruncommon.CloudProviderBearerToken(
		eruncommon.Context{},
		store,
		eruncommon.CloudBearerParams{Alias: machineIdentityTokenAlias},
		eruncommon.CloudDependencies{CloudSecretStore: secrets},
	)
	mustNoErr(t, err, "mint the machine identity's token through the client_credentials grant")
	if strings.TrimSpace(token.Token) == "" {
		t.Fatal("the client_credentials grant returned no access token")
	}
	return token.Token
}

// verifyMachineToken verifies a minted token through the real API bearer
// verifier -- the same JWKS-backed verification every protected route performs,
// with no test seam of its own.
func verifyMachineToken(t *testing.T, issuer *machineIdentityTokenIssuer, token string) Claims {
	t.Helper()
	verifier := NewBearerTokenVerifier(BearerTokenVerifierOptions{AllowedIssuers: []string{issuer.issuer()}})
	claims, err := verifier.VerifyBearerToken(context.Background(), token)
	mustNoErr(t, err, "verify the machine identity's token")
	return claims
}

// TestMachineIdentityClientCredentialsTokenResolvesItsTenant drives a token
// minted through the real client_credentials grant through the real
// ResolveTenantByIssuer against a real migrated PostgreSQL -- the artifact
// behind the module guidance's "verified go/no-go". The tenant here resolves by
// no claim except the resourceowner id, so this can only pass if the token
// carries that claim, which in turn can only happen if the grant asked for the
// scope that makes the issuer emit it. That is the question scope 1 left
// unestablished, decided here in the one direction a stub can decide it.
func TestMachineIdentityClientCredentialsTokenResolvesItsTenant(t *testing.T) {
	issuer := newMachineIdentityTokenIssuer(t, false)
	fixture := newMachineIdentityTokenFixture(t, issuer.issuer())
	token := fixture.mintToken(t, issuer)

	grants := issuer.grantLog()
	if len(grants) != 1 {
		t.Fatalf("want exactly one token request, got %d: %+v", len(grants), grants)
	}
	if grants[0].GrantType != "client_credentials" {
		t.Fatalf("grant_type = %q, want the client_credentials grant", grants[0].GrantType)
	}
	if !strings.Contains(grants[0].Scope, machineIdentityTokenOrgScope) {
		t.Fatalf(
			"the machine identity's grant asked for scope %q, which does not include %q -- against an org-scoped issuer its token would carry no %s and resolve to no tenant at all",
			grants[0].Scope, machineIdentityTokenOrgScope, machineIdentityTokenOrgClaimKey,
		)
	}
	if grants[0].BasicSecret == "" {
		t.Fatalf("the client credential did not travel in the Authorization header: %+v", grants[0])
	}

	claims := verifyMachineToken(t, issuer, token)
	if claims.Subject != fixture.identity.ClientID {
		t.Fatalf("token subject = %q, want the enrolled machine identity %q", claims.Subject, fixture.identity.ClientID)
	}
	if got := claims.Raw[machineIdentityTokenOrgClaimKey]; got != machineIdentityTokenOrg {
		t.Fatalf("token claim %s = %v, want the tenant's org %q", machineIdentityTokenOrgClaimKey, got, machineIdentityTokenOrg)
	}

	identities := repository.NewIdentityRepository(fixture.db, repository.DialectPostgres, "")
	tenant, err := identities.ResolveTenantByIssuer(context.Background(), claims)
	mustNoErr(t, err, "resolve the tenant from a client_credentials token")
	if tenant.TenantID != fixture.tenantID {
		t.Fatalf("resolved tenant %q, want the tenant the identity was provisioned in, %q", tenant.TenantID, fixture.tenantID)
	}
}

// TestMachineIdentityTokenWithoutTheOrgClaimResolvesNoTenant is the other side
// of the same question, and the reason the scope request above matters: an
// issuer that will not honour the org scope leaves the token with no
// resourceowner id, and erun must then refuse the token as an unresolvable
// tenant -- never resolve it to some other tenant, and never answer as if the
// identity were simply not enrolled (there is no tenant to be enrolled in).
// A live BYO issuer takes this path today; the erun-shipped Zitadel takes it
// the moment it stops honouring the scope on a client_credentials grant.
func TestMachineIdentityTokenWithoutTheOrgClaimResolvesNoTenant(t *testing.T) {
	issuer := newMachineIdentityTokenIssuer(t, true)
	fixture := newMachineIdentityTokenFixture(t, issuer.issuer())
	token := fixture.mintToken(t, issuer)

	grants := issuer.grantLog()
	if len(grants) != 2 {
		t.Fatalf("want the refused scope followed by one retry without it, got %d requests: %+v", len(grants), grants)
	}
	if grants[0].Scope == "" || grants[1].Scope != "" {
		t.Fatalf("want the org-claim scope dropped on the retry, got %+v", grants)
	}

	claims := verifyMachineToken(t, issuer, token)
	if value, present := claims.Raw[machineIdentityTokenOrgClaimKey]; present {
		t.Fatalf("token carries %s = %v even though the issuer refused the scope", machineIdentityTokenOrgClaimKey, value)
	}

	identities := repository.NewIdentityRepository(fixture.db, repository.DialectPostgres, "")
	_, err := identities.ResolveTenantByIssuer(context.Background(), claims)
	if !errors.Is(err, security.ErrTenantUnresolved) {
		t.Fatalf("err = %v, want security.ErrTenantUnresolved", err)
	}
	if errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("err = %v, must not also satisfy plain ErrNotFound -- the issuer is registered, so this must not read as an unknown issuer", err)
	}
	if !strings.Contains(err.Error(), machineIdentityTokenOrgClaimKey) {
		t.Fatalf("the refusal %q must name the claim the token is missing, so an operator can act on it", err.Error())
	}
}

// TestMachineIdentityBuildSelfReportIsNotRefused drives `erun build`'s own
// unattached self-report -- POST /v1/builds, the call an environment makes
// after every build -- against the real handler as the identity the environment
// now holds. The route is TenantAgentClass precisely so this call is not a 403;
// that classification was asserted at the authorizer before now, and never
// driven through the route. The negative control beside it is what makes the
// 201 mean something: the same identity is still refused the tenant-wide build
// history, which TenantAgent deliberately excludes.
func TestMachineIdentityBuildSelfReportIsNotRefused(t *testing.T) {
	issuer := newMachineIdentityTokenIssuer(t, false)
	fixture := newMachineIdentityTokenFixture(t, issuer.issuer())
	token := fixture.mintToken(t, issuer)

	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: NewBearerTokenVerifier(BearerTokenVerifierOptions{AllowedIssuers: []string{issuer.issuer()}}),
		IdentityCache: NewIdentityResolutionCache(IdentityCacheOptions{}),
		DB:            fixture.db,
		DBDialect:     repository.DialectPostgres,
	})
	mustNoErr(t, err, "new handler")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	code, body := postBuildAs(t, server.URL, token, fixture.environmentID)
	if code != http.StatusCreated {
		t.Fatalf("the machine identity's own build self-report was refused: HTTP %d (want 201): %s", code, body)
	}

	code, body = getBuildsAs(t, server.URL, token)
	if code != http.StatusForbidden {
		t.Fatalf("the machine identity read the tenant-wide build history: HTTP %d (want 403): %s", code, body)
	}
}

// getBuildsAs reads the tenant-wide build history as the bearer's identity.
func getBuildsAs(t *testing.T, baseURL, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/builds", nil)
	mustNoErr(t, err, "new request")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	mustNoErr(t, err, "do request")
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	mustNoErr(t, err, "read body")
	return resp.StatusCode, string(out)
}
