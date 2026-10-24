package backendapi

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/testpg"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// Everything on erun's side is production code; only the tenant IdP is a double.
// Whether Zitadel honours the resourceowner scope on client_credentials is the
// double's assumption, not something these tests establish.

const (
	// machineIdentityTokenOrgClaimKey matches bootstrapOrgScopeClaims in internal/repository/identity.go.
	machineIdentityTokenOrgClaimKey = "urn:zitadel:iam:user:resourceowner:id"
	machineIdentityTokenOrgScope    = "urn:zitadel:iam:user:resourceowner"

	machineIdentityTokenOrg         = "386994597030592700"
	machineIdentityTokenAlias       = "team-platform"
	machineIdentityTokenEnvironment = "machine-token-env"
)

type machineIdentityTokenGrant struct {
	GrantType   string
	Scope       string
	BasicUser   string
	BasicSecret string
	HadBasic    bool
}

// machineIdentityTokenIssuer models Zitadel: a client_credentials token carries the
// resourceowner id exactly when the grant asked for the scope.
type machineIdentityTokenIssuer struct {
	*mockOIDCProvider
	// refuseOrgScope answers invalid_scope once, like a BYO issuer erun retries against.
	refuseOrgScope bool
	org            string

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

	// Basic auth must match the API_AUTH_METHOD_TYPE_BASIC application erun provisions.
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
		// A Zitadel machine user's id is its client id.
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

type staticCloudReadStore struct {
	config configpkg.ERunConfig
}

func (s staticCloudReadStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

type machineIdentityTokenFixture struct {
	db            *sql.DB
	tenantID      string
	environmentID string
	identity      service.MachineIdentityResult
}

func newMachineIdentityTokenFixture(t *testing.T, issuerURL string) *machineIdentityTokenFixture {
	t.Helper()
	databaseURL := testpg.URL(t)
	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	stamp := time.Now().Format("20060102150405.000000")
	name := "machine-token-e2e-" + stamp

	var tenantID string
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenants (name, type) VALUES ($1, 'COMPANY') RETURNING tenant_id`, name,
	).Scan(&tenantID), "seed tenant")
	mustNoErr(t, db.QueryRow(
		`INSERT INTO issuers (issuer, org_field_key) VALUES ($1, $2)`,
		issuerURL, machineIdentityTokenOrgClaimKey,
	).Err(), "seed issuer")
	mustNoErr(t, db.QueryRow(
		`INSERT INTO tenant_issuers (tenant_id, issuer, org_field_value, name) VALUES ($1, $2, $3, $4)`,
		tenantID, issuerURL, machineIdentityTokenOrg, name,
	).Err(), "seed tenant issuer mapping")

	t.Cleanup(func() {
		// Registered last so it runs before the tenant teardown that these rows reference.
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

func (f *machineIdentityTokenFixture) mintToken(t *testing.T, issuer *machineIdentityTokenIssuer) string {
	t.Helper()
	issuer.expectCredential(f.identity.ClientID, f.identity.ClientSecret)

	const secretRef = "erun/clientsecret/" + machineIdentityTokenAlias
	secrets := cloud.NewFileCloudSecretStore(t.TempDir())
	mustNoErr(t, secrets.SaveCloudSecret(secretRef, f.identity.ClientSecret), "save the provisioned client secret")

	store := staticCloudReadStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{{
		Alias:         machineIdentityTokenAlias,
		Provider:      configpkg.CloudProviderERun,
		OIDCIssuerURL: issuer.issuer(),
		ERun: &configpkg.ERunProviderConfig{
			APIURL:          "http://platform.invalid",
			ClientID:        f.identity.ClientID,
			ClientSecretRef: secretRef,
		},
	}}}}
	token, err := cloud.CloudProviderBearerToken(
		core.Context{},
		store,
		cloud.CloudBearerParams{Alias: machineIdentityTokenAlias},
		cloud.CloudDependencies{CloudSecretStore: secrets},
	)
	mustNoErr(t, err, "mint the machine identity's token through the client_credentials grant")
	if strings.TrimSpace(token.Token) == "" {
		t.Fatal("the client_credentials grant returned no access token")
	}
	return token.Token
}

func verifyMachineToken(t *testing.T, issuer *machineIdentityTokenIssuer, token string) Claims {
	t.Helper()
	verifier := NewBearerTokenVerifier(BearerTokenVerifierOptions{AllowedIssuers: []string{issuer.issuer()}})
	claims, err := verifier.VerifyBearerToken(context.Background(), token)
	mustNoErr(t, err, "verify the machine identity's token")
	return claims
}

// TestMachineIdentityClientCredentialsTokenResolvesItsTenant: the tenant resolves by no claim but
// the resourceowner id, so this passes only if the grant requested the org scope.
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

// TestMachineIdentityTokenWithoutTheOrgClaimResolvesNoTenant: without the org claim erun must
// refuse as tenant-unresolved, never resolve another tenant or answer not-enrolled.
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

// TestMachineIdentityBuildSelfReportIsNotRefused: POST /v1/builds is TenantAgentClass so the
// self-report is not a 403; the same identity is still refused tenant-wide build history.
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
