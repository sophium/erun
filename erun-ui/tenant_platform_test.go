package main

import (
	"errors"
	"strings"
	"testing"

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

// TestLoadTenantDashboardHonorsTenantAPIURLOverride: TenantConfig.APIURL
// (the same field `erun list` already treats as a tenant's own stable API
// address) still wins over the resolved alias's own ERun.APIURL — but the
// bearer still comes from that alias, never from an environment.
func TestLoadTenantDashboardHonorsTenantAPIURLOverride(t *testing.T) {
	var requests []string
	server := tenantDashboardAPI(t, "null", nil, &requests)
	defer server.Close()

	app := testERunPlatformAliasAppWithTenants(t, "https://this-must-not-be-used.example",
		map[string]eruncommon.TenantConfig{"frs": {Name: "frs", APIURL: server.URL}})

	dashboard := loadTenantDashboardFrom(t, app)
	if dashboard.PlatformURL != server.URL || dashboard.APIURL != server.URL {
		t.Fatalf("expected the tenant-level APIURL override to win, got %+v", dashboard)
	}
	if dashboard.PlatformAlias != testERunAlias {
		t.Fatalf("expected the bearer's alias to stay the resolved erun alias, got %q", dashboard.PlatformAlias)
	}
}
