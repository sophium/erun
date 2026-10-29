package tenant

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestLoadTenantDashboardRequiresATenant(t *testing.T) {
	app := newTestService(t, Deps{Store: stubStore{}})
	_, err := app.LoadTenantDashboard(DashboardInput{})
	if err == nil || !errors.Is(err, uimodel.ErrTenantNotGiven) {
		t.Fatalf("expected ErrTenantNotGiven, got %v", err)
	}
	if !strings.Contains(err.Error(), "loading the tenant dashboard") {
		t.Fatalf("expected the error to name its operation, got %v", err)
	}
}

// Several erun aliases are reported as choices with none preselected.
func TestLoadTenantDashboardChoosesAliasWhenMoreThanOneIsConfigured(t *testing.T) {
	rootConfig := config.ERunConfig{CloudProviders: []model.CloudProviderConfig{
		{Alias: "erun+a@erun", Provider: config.CloudProviderERun, ERun: &model.ERunProviderConfig{APIURL: "https://s.example", ClientID: "client-a"}},
		{Alias: "erun+b@erun", Provider: config.CloudProviderERun, ERun: &model.ERunProviderConfig{APIURL: "https://b.example", ClientID: "client-b"}},
	}}
	app := newTestService(t, Deps{
		Store: stubStore{config: &rootConfig, tenants: map[string]model.TenantConfig{"frs": {Name: "frs"}}},
	})

	dashboard, err := app.LoadTenantDashboard(DashboardInput{Tenant: "frs"})
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

	chosen, err := app.LoadTenantDashboard(DashboardInput{Tenant: "frs", PlatformAlias: "erun+b@erun"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard with an explicit alias failed: %v", err)
	}
	if chosen.PlatformState != tenantPlatformStateNotSignedIn || chosen.PlatformAlias != "erun+b@erun" {
		t.Fatalf("expected not-signed-in for the chosen alias erun+b@erun, got %+v", chosen)
	}
}

// An alias that never signed in fails before any network call.
func TestLoadTenantDashboardReportsNotSignedInLocally(t *testing.T) {
	rootConfig := config.ERunConfig{CloudProviders: []model.CloudProviderConfig{
		{Alias: testERunAlias, Provider: config.CloudProviderERun, ERun: &model.ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "client-1"}},
	}}
	app := newTestService(t, Deps{
		Store: stubStore{config: &rootConfig, tenants: map[string]model.TenantConfig{"frs": {Name: "frs"}}},
		Cloud: cloud.CloudDependencies{
			FetchOIDCDiscovery: func(core.Context, string) (cloud.OIDCDiscovery, error) {
				t.Fatal("must not fetch discovery with no refresh token and no cache to check")
				return cloud.OIDCDiscovery{}, nil
			},
		},
	})

	dashboard, err := app.LoadTenantDashboard(DashboardInput{Tenant: "frs"})
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

// TenantConfig.APIURL is not a platform URL; honoring it would leak the
// alias's bearer token to that address.
func TestLoadTenantDashboardIgnoresTenantAPIURLOverrideForThePlatformRead(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "null", nil, &requests)
	defer server.Close()

	app := testERunPlatformAliasAppWithTenants(t, server.URL,
		map[string]model.TenantConfig{"frs": {Name: "frs", APIURL: "https://this-must-not-be-used.example"}})

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

// A tenant must never use an erun alias it did not select.
func TestLoadTenantDashboardNeverUsesAnUnselectedERunAliasForTheTenant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Fatalf("must never reach the erun platform with an alias this tenant did not select, got %s", req.URL.Path)
	}))
	defer server.Close()

	jwt := testUIJWT("https://sts.aws.example")
	rootConfig := config.ERunConfig{CloudProviders: []model.CloudProviderConfig{
		{Alias: "team-cloud", Provider: config.CloudProviderAWS, Profile: "team"},
		{Alias: testERunAlias, Provider: config.CloudProviderERun, ERun: &model.ERunProviderConfig{APIURL: server.URL, ClientID: "test-client"}},
	}}
	app := newTestService(t, Deps{
		Store: stubStore{config: &rootConfig, tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", CloudProviderAliases: []string{"team-cloud"}, PrimaryCloudProviderAlias: "team-cloud"},
		}},
		Cloud: cloud.CloudDependencies{
			RunAWSBearerToken: func(core.Context, string, string) (string, error) { return jwt, nil },
			CheckAWSStatus: func(_ core.Context, provider model.CloudProviderConfig) model.CloudProviderStatus {
				return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusActive}
			},
		},
	})

	dashboard, err := app.LoadTenantDashboard(DashboardInput{Tenant: "erun"})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	if dashboard.PlatformState != tenantPlatformStateNotConnected {
		t.Fatalf("expected not-connected for a tenant that selected only an AWS alias, got %q (apiError=%q)", dashboard.PlatformState, dashboard.APIError)
	}
	if dashboard.PlatformAlias != "" || dashboard.PlatformURL != "" {
		t.Fatalf("expected no platform alias or URL to be reported, got %+v", dashboard)
	}

	statuses := app.ListTenantPlatformEnrollmentStatuses(ListEnrollmentStatusesInput{Tenants: []string{"erun"}})
	if len(statuses) != 1 || statuses[0].State != tenantEnrollmentLocalOnly {
		t.Fatalf("expected the sidebar dot to read local-only, not enrolled, got %+v", statuses)
	}
}

func TestLoadTenantDashboardUsesTheTenantsOwnSelectedERunAlias(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "null", nil, &requests)
	defer server.Close()

	secrets := secretstore.NewFileCloudSecretStore(t.TempDir())
	refreshRef := "erun/refresh/" + testERunAlias
	if err := secrets.SaveCloudSecret(refreshRef, "refresh-1"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	jwt := testUIJWTWithSubject(testERunIssuer, testERunSubject)
	const otherERunAlias = "erun+api.other.example@erun"
	rootConfig := config.ERunConfig{CloudProviders: []model.CloudProviderConfig{
		{Alias: otherERunAlias, Provider: config.CloudProviderERun, ERun: &model.ERunProviderConfig{APIURL: "https://this-must-not-be-used.example", ClientID: "other-client"}},
		{Alias: testERunAlias, Provider: config.CloudProviderERun, Username: "erun", ERun: &model.ERunProviderConfig{APIURL: server.URL, ClientID: "test-client", RefreshTokenRef: refreshRef}},
	}}
	app := newTestService(t, Deps{
		Store: stubStore{config: &rootConfig, tenants: map[string]model.TenantConfig{
			"frs": {Name: "frs", CloudProviderAliases: []string{otherERunAlias, testERunAlias}, PrimaryCloudProviderAlias: testERunAlias},
		}},
		Cloud: cloud.CloudDependencies{
			CloudSecretStore: secrets,
			FetchOIDCDiscovery: func(core.Context, string) (cloud.OIDCDiscovery, error) {
				return cloud.OIDCDiscovery{TokenEndpoint: "https://auth.erun.example/token"}, nil
			},
			RefreshERunTokens: func(core.Context, cloud.OIDCDiscovery, string, string) (cloud.ERunTokens, error) {
				return cloud.ERunTokens{AccessToken: jwt, ExpiresIn: time.Hour}, nil
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
