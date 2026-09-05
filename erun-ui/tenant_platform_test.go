package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestLoadTenantDashboardRequiresATenant is the null-input case: an empty
// tenant must fail loudly, naming the operation and its recovery, rather
// than silently returning null the way the old apiUrl/cloudProviderAlias
// precondition used to.
func TestLoadTenantDashboardRequiresATenant(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	_, err := app.LoadTenantDashboard(uiTenantDashboardInput{})
	if err == nil || !errors.Is(err, ErrTenantNotGiven) {
		t.Fatalf("expected ErrTenantNotGiven, got %v", err)
	}
	if !strings.Contains(err.Error(), "loading the tenant dashboard") {
		t.Fatalf("expected the error to name its operation, got %v", err)
	}
}

// TestLoadTenantDashboardChoosesAliasWhenMoreThanOneIsConfigured is the
// multiple-erun-alias case: the CLI errors and asks for --alias; the desktop
// instead reports every configured alias as a choice so the operator can
// pick one, preselecting none until they do.
func TestLoadTenantDashboardChoosesAliasWhenMoreThanOneIsConfigured(t *testing.T) {
	rootConfig := eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{
		{Alias: "erun+a@erun", Provider: eruncommon.CloudProviderERun, ERun: &eruncommon.ERunProviderConfig{APIURL: "https://a.example", ClientID: "client-a"}},
		{Alias: "erun+b@erun", Provider: eruncommon.CloudProviderERun, ERun: &eruncommon.ERunProviderConfig{APIURL: "https://b.example", ClientID: "client-b"}},
	}}
	app := NewApp(erunUIDeps{
		store: stubUIStore{config: &rootConfig, tenants: map[string]eruncommon.TenantConfig{"frs": {Name: "frs"}}},
	})

	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{Tenant: "frs"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	if dashboard.PlatformState != tenantPlatformStateChooseAlias {
		t.Fatalf("expected choose-alias, got %q", dashboard.PlatformState)
	}
	got := append([]string(nil), dashboard.PlatformAliasChoices...)
	want := []string{"erun+a@erun", "erun+b@erun"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("PlatformAliasChoices = %v, want %v", got, want)
	}

	// An explicit PlatformAlias resolves the ambiguity, exactly like the
	// CLI's --alias.
	chosen, err := app.LoadTenantDashboard(uiTenantDashboardInput{Tenant: "frs", PlatformAlias: "erun+b@erun"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard with an explicit alias failed: %v", err)
	}
	if chosen.PlatformState != tenantPlatformStateNotSignedIn || chosen.PlatformAlias != "erun+b@erun" {
		t.Fatalf("expected not-signed-in for the chosen alias erun+b@erun, got %+v", chosen)
	}
}

// TestLoadTenantDashboardReportsNotSignedInLocally is state B's second row:
// an erun alias is configured but never signed in, so the bearer mint fails
// before any network call to the platform is made.
func TestLoadTenantDashboardReportsNotSignedInLocally(t *testing.T) {
	rootConfig := eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{
		{Alias: testERunAlias, Provider: eruncommon.CloudProviderERun, ERun: &eruncommon.ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "client-1"}},
	}}
	app := NewApp(erunUIDeps{
		store: stubUIStore{config: &rootConfig, tenants: map[string]eruncommon.TenantConfig{"frs": {Name: "frs"}}},
		cloudDeps: eruncommon.CloudDependencies{
			FetchOIDCDiscovery: func(eruncommon.Context, string) (eruncommon.OIDCDiscovery, error) {
				t.Fatal("must not fetch discovery with no refresh token and no cache to check")
				return eruncommon.OIDCDiscovery{}, nil
			},
		},
	})

	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{Tenant: "frs"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	if dashboard.PlatformState != tenantPlatformStateNotSignedIn {
		t.Fatalf("expected not-signed-in, got %q", dashboard.PlatformState)
	}
	if dashboard.PlatformAlias != testERunAlias {
		t.Fatalf("expected the resolved alias to be reported, got %q", dashboard.PlatformAlias)
	}
	if dashboard.PlatformURL != "https://api.example.test" {
		t.Fatalf("expected the resolved platform URL to be reported, got %q", dashboard.PlatformURL)
	}
}

// TestLoadTenantDashboardIgnoresTenantAPIURLOverrideForThePlatformRead:
// TenantConfig.APIURL is documented only as `erun open`'s own port-forward
// address (erun-docs/docs/reference/configuration.md), not a platform-URL
// override. Honoring it here used to send the resolved alias's bearer token
// to whatever address the field named — a credential-disclosure shape
// (erun#1955) — so the platform read must always use the alias's own known
// URL and never reach the address this test's httptest server would prove it
// used if the override still won.
func TestLoadTenantDashboardIgnoresTenantAPIURLOverrideForThePlatformRead(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "null", nil, &requests)
	defer server.Close()

	app := testERunPlatformAliasAppWithTenants(t, server.URL,
		map[string]eruncommon.TenantConfig{"frs": {Name: "frs", APIURL: "https://this-must-not-be-used.example"}})

	dashboard := loadTenantDashboardFrom(t, app)
	if dashboard.PlatformURL != server.URL || dashboard.APIURL != server.URL {
		t.Fatalf("expected the resolved alias's own URL to be used, got %+v", dashboard)
	}
	if dashboard.PlatformAlias != testERunAlias {
		t.Fatalf("expected the bearer's alias to stay the resolved erun alias, got %q", dashboard.PlatformAlias)
	}
	if len(requests) == 0 {
		t.Fatal("expected the dashboard to actually reach the alias's own server")
	}
}

// TestLoadTenantDashboardNeverUsesAnUnselectedERunAliasForTheTenant is the
// erun#1955 regression: a global erun alias exists and is even signed in,
// but this tenant's own configuration selected only an AWS alias (exactly
// the operator's own machine — an "erun" tenant whose Manage tenant dialog
// had an AWS alias checked and Primary, with the erun alias explicitly
// unchecked). The dashboard must never authenticate this tenant's platform
// reads with a credential the tenant itself did not select, even though it
// is the only erun alias configured anywhere.
func TestLoadTenantDashboardNeverUsesAnUnselectedERunAliasForTheTenant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Fatalf("must never reach the erun platform with an alias this tenant did not select, got %s", req.URL.Path)
	}))
	defer server.Close()

	jwt := testUIJWT("https://sts.aws.example")
	rootConfig := eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{
		{Alias: "team-cloud", Provider: eruncommon.CloudProviderAWS, Profile: "team"},
		{Alias: testERunAlias, Provider: eruncommon.CloudProviderERun, ERun: &eruncommon.ERunProviderConfig{APIURL: server.URL, ClientID: "test-client"}},
	}}
	app := NewApp(erunUIDeps{
		store: stubUIStore{config: &rootConfig, tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", CloudProviderAliases: []string{"team-cloud"}, PrimaryCloudProviderAlias: "team-cloud"},
		}},
		cloudDeps: eruncommon.CloudDependencies{
			RunAWSBearerToken: func(eruncommon.Context, string, string) (string, error) { return jwt, nil },
			CheckAWSStatus: func(_ eruncommon.Context, provider eruncommon.CloudProviderConfig) eruncommon.CloudProviderStatus {
				return eruncommon.CloudProviderStatus{CloudProviderConfig: provider, Status: eruncommon.CloudTokenStatusActive}
			},
		},
	})

	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{Tenant: "erun"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	if dashboard.PlatformState != tenantPlatformStateNotConnected {
		t.Fatalf("expected not-connected for a tenant that selected only an AWS alias, got %q (apiError=%q)", dashboard.PlatformState, dashboard.APIError)
	}
	if dashboard.PlatformAlias != "" || dashboard.PlatformURL != "" {
		t.Fatalf("expected no platform alias or URL to be reported, got %+v", dashboard)
	}

	statuses := app.ListTenantPlatformEnrollmentStatuses(uiListTenantPlatformEnrollmentStatusesInput{Tenants: []string{"erun"}})
	if len(statuses) != 1 || statuses[0].State != tenantEnrollmentLocalOnly {
		t.Fatalf("expected the sidebar dot to read local-only, not enrolled, got %+v", statuses)
	}
}

// TestLoadTenantDashboardUsesTheTenantsOwnSelectedERunAlias is the positive
// half of the erun#1955 fix: once the tenant's own selection DOES include an
// erun alias, that alias is used even though a different erun alias is also
// configured globally — the tenant's own choice, not "the only one on the
// machine", decides.
func TestLoadTenantDashboardUsesTheTenantsOwnSelectedERunAlias(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "null", nil, &requests)
	defer server.Close()

	secrets := eruncommon.NewFileCloudSecretStore(t.TempDir())
	refreshRef := "erun/refresh/" + testERunAlias
	if err := secrets.SaveCloudSecret(refreshRef, "refresh-1"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	jwt := testUIJWTWithSubject(testERunIssuer, testERunSubject)
	const otherERunAlias = "erun+api.other.example@erun"
	rootConfig := eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{
		{Alias: otherERunAlias, Provider: eruncommon.CloudProviderERun, ERun: &eruncommon.ERunProviderConfig{APIURL: "https://this-must-not-be-used.example", ClientID: "other-client"}},
		{Alias: testERunAlias, Provider: eruncommon.CloudProviderERun, Username: "erun", ERun: &eruncommon.ERunProviderConfig{APIURL: server.URL, ClientID: "test-client", RefreshTokenRef: refreshRef}},
	}}
	app := NewApp(erunUIDeps{
		store: stubUIStore{config: &rootConfig, tenants: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs", CloudProviderAliases: []string{otherERunAlias, testERunAlias}, PrimaryCloudProviderAlias: testERunAlias},
		}},
		cloudDeps: eruncommon.CloudDependencies{
			CloudSecretStore: secrets,
			FetchOIDCDiscovery: func(eruncommon.Context, string) (eruncommon.OIDCDiscovery, error) {
				return eruncommon.OIDCDiscovery{TokenEndpoint: "https://auth.erun.example/token"}, nil
			},
			RefreshERunTokens: func(eruncommon.Context, eruncommon.OIDCDiscovery, string, string) (eruncommon.ERunTokens, error) {
				return eruncommon.ERunTokens{AccessToken: jwt, ExpiresIn: time.Hour}, nil
			},
		},
	})

	dashboard := loadTenantDashboardFrom(t, app)
	if dashboard.PlatformAlias != testERunAlias {
		t.Fatalf("expected the tenant's own primary erun alias to be used, got %q", dashboard.PlatformAlias)
	}
	if dashboard.PlatformURL != server.URL {
		t.Fatalf("expected the chosen alias's own URL, got %q", dashboard.PlatformURL)
	}
}
