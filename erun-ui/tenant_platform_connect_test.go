package main

import (
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// erunTestPlatformAPIURL is the address the Connect card pre-fills; the alias
// this action attaches is derived from it (erun+<host>@erun), which is what
// makes a second click on the same card collide with the alias the first one
// wrote rather than accumulating new ones.
const erunTestPlatformAPIURL = "https://api.erunpaas.com"

const erunTestAlias = "erun+api.erunpaas.com@erun"

// connectTestApp builds an app whose tenant already attaches the given non-erun
// aliases — the state the Connect card is rendered for, because
// resolveTenantPlatform takes the tenant-scoped path whenever that selection is
// non-empty and finds no erun-type alias in it.
func connectTestApp(t *testing.T, tenant string, attached []string) (*App, stubUIStore) {
	t.Helper()
	rootConfig := eruncommon.ERunConfig{}
	tenantConfig := eruncommon.TenantConfig{Name: tenant}
	if len(attached) > 0 {
		tenantConfig.CloudProviderAliases = append([]string(nil), attached...)
		tenantConfig.PrimaryCloudProviderAlias = attached[0]
	}
	store := stubUIStore{
		config:  &rootConfig,
		tenants: map[string]eruncommon.TenantConfig{tenant: tenantConfig},
	}
	app := NewApp(erunUIDeps{
		store: store,
		cloudDeps: eruncommon.CloudDependencies{
			FetchPlatformInfo: func(_ eruncommon.Context, apiURL string) (eruncommon.PlatformInfo, error) {
				if apiURL != erunTestPlatformAPIURL {
					t.Fatalf("platform discovery went to %q, want %q", apiURL, erunTestPlatformAPIURL)
				}
				return eruncommon.PlatformInfo{
					Issuer:      "https://auth.erunpaas.com",
					APIURL:      apiURL,
					CLIClientID: "cli-client-1",
				}, nil
			},
			FetchOIDCDiscovery: func(eruncommon.Context, string) (eruncommon.OIDCDiscovery, error) {
				return eruncommon.OIDCDiscovery{}, nil
			},
		},
	})
	return app, store
}

func loadTenantDashboardFor(t *testing.T, app *App, tenant string) uiTenantDashboard {
	t.Helper()
	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{Tenant: tenant})
	if err != nil {
		t.Fatalf("LoadTenantDashboard(%s): %v", tenant, err)
	}
	return dashboard
}

func loadTenantConfigFor(t *testing.T, app *App, tenant string) eruncommon.TenantConfig {
	t.Helper()
	config, _, err := app.deps.store.LoadTenantConfig(tenant)
	if err != nil {
		t.Fatalf("LoadTenantConfig(%s): %v", tenant, err)
	}
	return config
}

func requirePlatformState(t *testing.T, app *App, tenant, want, when string) {
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

func connectToTestPlatform(t *testing.T, app *App, tenant string) uiCloudProviderStatus {
	t.Helper()
	status, err := app.ConnectERunPlatform(uiConnectERunPlatformInput{
		APIURL: erunTestPlatformAPIURL,
		Tenant: tenant,
	})
	if err != nil {
		t.Fatalf("ConnectERunPlatform(%s): %v", tenant, err)
	}
	return status
}

// TestConnectERunPlatformMovesTheCardItWasClickedFrom is the reproduction of
// the reported failure: the operator clicks Connect on a tenant whose own alias selection
// is non-empty and holds no erun-type alias, and the card is expected to stop
// being the not-connected one.
//
// Before the fix the click attached the alias to the machine-global provider
// list only, resolveTenantPlatform kept taking the tenant-scoped path, found no
// erun alias in the tenant's own selection, and returned not-connected — so the
// dashboard re-rendered byte-identically and the button read as inert even
// though it had done real work. This asserts the state the operator was
// actually looking at, not the alias list.
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

// TestConnectERunPlatformKeepsExactlyOneErunAliasAttached guards the state the
// second click lands in: the alias this action derives from a URL is stable, so
// re-connecting the same platform must replace the tenant's erun choice rather
// than leave two — two candidates with a primary that names neither is the
// choose-alias state, which is a card the operator did not ask for.
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

// TestConnectERunPlatformWithoutATenantAttachesGlobally is the machine-wide
// settings dialog's path: it has no tenant to attach to, and must keep working
// (and must not invent one) — its alias is attached for the whole machine, the
// same as `erun cloud init erun` does.
func TestConnectERunPlatformWithoutATenantAttachesGlobally(t *testing.T) {
	const tenant = "erun"
	app, store := connectTestApp(t, tenant, []string{"team+aws"})

	if _, err := app.ConnectERunPlatform(uiConnectERunPlatformInput{APIURL: erunTestPlatformAPIURL}); err != nil {
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
