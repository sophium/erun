package tenant

import (
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// erunTestPlatformAPIURL derives a stable alias, so a second Connect collides
// with the first alias instead of adding another.
const erunTestPlatformAPIURL = "https://api.erunpaas.com"

const erunTestAlias = "erun+api.erunpaas.com@erun"

// connectTestApp builds an app whose tenant attaches only non-erun aliases.
func connectTestApp(t *testing.T, tenant string, attached []string) (*Service, stubStore) {
	t.Helper()
	rootConfig := configpkg.ERunConfig{}
	tenantConfig := model.TenantConfig{Name: tenant}
	if len(attached) > 0 {
		tenantConfig.CloudProviderAliases = append([]string(nil), attached...)
		tenantConfig.PrimaryCloudProviderAlias = attached[0]
	}
	store := stubStore{
		config:  &rootConfig,
		tenants: map[string]model.TenantConfig{tenant: tenantConfig},
	}
	app := newTestService(t, Deps{
		Store: store,
		Cloud: cloud.CloudDependencies{
			FetchPlatformInfo: func(_ core.Context, apiURL string) (model.PlatformInfo, error) {
				if apiURL != erunTestPlatformAPIURL {
					t.Fatalf("platform discovery went to %q, want %q", apiURL, erunTestPlatformAPIURL)
				}
				return model.PlatformInfo{
					Issuer:      "https://auth.erunpaas.com",
					APIURL:      apiURL,
					CLIClientID: "cli-client-1",
				}, nil
			},
			FetchOIDCDiscovery: func(core.Context, string) (cloud.OIDCDiscovery, error) {
				return cloud.OIDCDiscovery{}, nil
			},
		},
	})
	return app, store
}

func loadTenantDashboardFor(t *testing.T, app *Service, tenant string) Dashboard {
	t.Helper()
	dashboard, err := app.LoadTenantDashboard(DashboardInput{Tenant: tenant})
	if err != nil {
		t.Fatalf("LoadTenantDashboard(%s): %v", tenant, err)
	}
	return dashboard
}

func loadTenantConfigFor(t *testing.T, app *Service, tenant string) model.TenantConfig {
	t.Helper()
	config, _, err := app.d.Store.LoadTenantConfig(tenant)
	if err != nil {
		t.Fatalf("LoadTenantConfig(%s): %v", tenant, err)
	}
	return config
}

func requirePlatformState(t *testing.T, app *Service, tenant, want, when string) {
	t.Helper()
	dashboard := loadTenantDashboardFor(t, app, tenant)
	if dashboard.PlatformState != want {
		t.Fatalf("%s: platform state = %q, want %q", when, dashboard.PlatformState, want)
	}
}

func requireAliasesContain(t *testing.T, aliases []string, want, why string) {
	t.Helper()
	if !containsString(aliases, want) {
		t.Fatalf("%s: %v does not contain %q", why, aliases, want)
	}
}

func countErunAliases(aliases []string) int {
	count := 0
	for _, alias := range aliases {
		if strings.HasSuffix(alias, "@erun") {
			count++
		}
	}
	return count
}

func connectToTestPlatform(t *testing.T, app *Service, tenant string) model.CloudProviderStatus {
	t.Helper()
	status, err := app.ConnectERunPlatform(ConnectERunPlatformInput{
		APIURL: erunTestPlatformAPIURL,
		Tenant: tenant,
	})
	if err != nil {
		t.Fatalf("ConnectERunPlatform(%s): %v", tenant, err)
	}
	return status
}

// Clicking Connect on a tenant with only non-erun aliases must leave the
// not-connected card, not just update the global alias list.
func TestConnectERunPlatformMovesTheCardItWasClickedFrom(t *testing.T) {
	const tenant = "erun"
	app, _ := connectTestApp(t, tenant, []string{"team+aws"})
	requirePlatformState(t, app, tenant, tenantPlatformStateNotConnected, "precondition, before the click")

	status := connectToTestPlatform(t, app, tenant)
	if status.Alias != erunTestAlias {
		t.Fatalf("attached alias = %q, want %q", status.Alias, erunTestAlias)
	}

	after := loadTenantDashboardFor(t, app, tenant)
	if after.PlatformState == tenantPlatformStateNotConnected {
		t.Fatalf("still not-connected after connecting %s: the click did not move the state it was clicked from", status.Alias)
	}
	if after.PlatformAlias != status.Alias {
		t.Fatalf("tenant resolves through %q, want %q", after.PlatformAlias, status.Alias)
	}
	aliases := loadTenantConfigFor(t, app, tenant).CloudProviderAliases
	requireAliasesContain(t, aliases, status.Alias, "tenant selection")
	requireAliasesContain(t, aliases, "team+aws", "tenant selection dropped the alias it already had")
}

// Re-connecting the same platform replaces the erun alias; two would land in
// the choose-alias state.
func TestConnectERunPlatformKeepsExactlyOneErunAliasAttached(t *testing.T) {
	const tenant = "erun"
	app, _ := connectTestApp(t, tenant, []string{"team+aws"})

	for attempt := 0; attempt < 2; attempt++ {
		connectToTestPlatform(t, app, tenant)
	}

	aliases := loadTenantConfigFor(t, app, tenant).CloudProviderAliases
	if got := countErunAliases(aliases); got != 1 {
		t.Fatalf("tenant selection %v names %d erun aliases after two clicks, want exactly 1", aliases, got)
	}
	if dashboard := loadTenantDashboardFor(t, app, tenant); dashboard.PlatformState == tenantPlatformStateChooseAlias {
		t.Fatalf("second click landed in choose-alias: %v", dashboard.PlatformAliasChoices)
	}
}

func TestConnectERunPlatformWithoutATenantAttachesGlobally(t *testing.T) {
	const tenant = "erun"
	app, store := connectTestApp(t, tenant, []string{"team+aws"})

	if _, err := app.ConnectERunPlatform(ConnectERunPlatformInput{APIURL: erunTestPlatformAPIURL}); err != nil {
		t.Fatalf("ConnectERunPlatform without a tenant failed: %v", err)
	}

	if store.config == nil || len(store.config.CloudProviders) != 1 {
		t.Fatalf("expected one machine-global provider, got %+v", store.config)
	}
	aliases := loadTenantConfigFor(t, app, tenant).CloudProviderAliases
	if containsString(aliases, erunTestAlias) {
		t.Fatalf("a click with no tenant attached the alias to %q anyway: %v", tenant, aliases)
	}
}
