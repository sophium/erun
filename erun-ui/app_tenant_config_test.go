package main

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	openpkg "github.com/sophium/erun/erun-common/primitive/open"
)

func TestLoadAndSaveTenantConfig(t *testing.T) {
	rootConfig := configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{{
		Alias:         "team-cloud",
		Provider:      configpkg.CloudProviderAWS,
		OIDCIssuerURL: "https://issuer.team.example",
	}}}
	store := stubUIStore{
		config: &rootConfig,
		tenants: map[string]configpkg.TenantConfig{
			"frs": {
				Name:                      "frs",
				DefaultEnvironment:        "dev",
				APIURL:                    "https://api.old.example",
				CloudProviderAliases:      []string{"team-cloud"},
				PrimaryCloudProviderAlias: "team-cloud",
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	loaded, err := app.LoadTenantConfig(" frs ")
	if err != nil {
		t.Fatalf("LoadTenantConfig failed: %v", err)
	}
	assertLoadedTenantConfig(t, loaded)

	saved, err := app.SaveTenantConfig(uiTenantConfig{
		Name:                      "frs",
		DefaultEnvironment:        " prod ",
		APIURL:                    " https://api.new.example ",
		CloudProviderAliases:      []string{" team-cloud "},
		PrimaryCloudProviderAlias: "team-cloud",
	})
	if err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}
	if saved.DefaultEnvironment != "prod" || saved.APIURL != "https://api.new.example" || saved.PrimaryCloudProviderAlias != "team-cloud" {
		t.Fatalf("unexpected saved config: %+v", saved)
	}
	if store.tenants["frs"].APIURL != "https://api.new.example" || store.tenants["frs"].PrimaryCloudProviderAlias != "team-cloud" {
		t.Fatalf("expected tenant project root to be preserved, got %+v", store.tenants["frs"])
	}
}

func assertLoadedTenantConfig(t *testing.T, loaded uiTenantConfig) {
	t.Helper()

	if loaded.Name != "frs" || loaded.DefaultEnvironment != "dev" || loaded.APIURL != "https://api.old.example" || loaded.PrimaryCloudProviderAlias != "team-cloud" || len(loaded.CloudProviderAliases) != 1 || loaded.CloudProviderAliases[0] != "team-cloud" {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}
	if len(loaded.CloudProviders) != 1 || loaded.CloudProviders[0].OIDCIssuerURL != "https://issuer.team.example" {
		t.Fatalf("expected cloud provider statuses with issuer URL, got %+v", loaded.CloudProviders)
	}
}

func TestSetupCloudProviderOIDCStoresIssuer(t *testing.T) {
	rootConfig := configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{{
		Alias:    "team-cloud",
		Provider: configpkg.CloudProviderAWS,
		Profile:  "team",
		Username: "Rihards.Freimanis",
	}}}
	store := stubUIStore{config: &rootConfig}
	app := NewApp(erunUIDeps{
		store: store,
		cloudDeps: cloud.CloudDependencies{
			RunAWSBearerToken: func(_ core.Context, profile, audience string) (string, error) {
				if profile != "team" || audience != configpkg.CloudProviderBearerAudience {
					t.Fatalf("unexpected bearer token input profile=%q audience=%q", profile, audience)
				}
				return testUIJWT("https://sts.aws.example/.well-known/openid-configuration"), nil
			},
			CheckAWSStatus: func(_ core.Context, provider configpkg.CloudProviderConfig) cloud.CloudProviderStatus {
				return cloud.CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusActive}
			},
		},
	})

	status, err := app.SetupCloudProviderOIDC("team-cloud")
	if err != nil {
		t.Fatalf("SetupCloudProviderOIDC failed: %v", err)
	}
	if status.OIDCIssuerURL != "https://sts.aws.example" || rootConfig.CloudProviders[0].OIDCIssuerURL != "https://sts.aws.example" {
		t.Fatalf("expected OIDC issuer to be stored, status=%+v config=%+v", status, rootConfig)
	}
}

func TestGetCloudProviderBearerTokenReturnsTokenAndStatus(t *testing.T) {
	jwt := testUIJWT("https://sts.aws.example/.well-known/openid-configuration")
	rootConfig := configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{{
		Alias:    "team-cloud",
		Provider: configpkg.CloudProviderAWS,
		Profile:  "team",
		Username: "Rihards.Freimanis",
	}}}
	app := NewApp(erunUIDeps{
		store: stubUIStore{config: &rootConfig},
		cloudDeps: cloud.CloudDependencies{
			RunAWSBearerToken: func(_ core.Context, profile, audience string) (string, error) {
				if profile != "team" || audience != configpkg.CloudProviderBearerAudience {
					t.Fatalf("unexpected bearer token input profile=%q audience=%q", profile, audience)
				}
				return jwt, nil
			},
			CheckAWSStatus: func(_ core.Context, provider configpkg.CloudProviderConfig) cloud.CloudProviderStatus {
				return cloud.CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusActive}
			},
		},
	})

	token, err := app.GetCloudProviderBearerToken(" team-cloud ")
	if err != nil {
		t.Fatalf("GetCloudProviderBearerToken failed: %v", err)
	}
	if token.Token != jwt || token.Issuer != "https://sts.aws.example" {
		t.Fatalf("unexpected bearer token result: %+v", token)
	}
	if token.Provider.Alias != "team-cloud" || token.Provider.Status != configpkg.CloudTokenStatusActive {
		t.Fatalf("expected active provider status, got %+v", token.Provider)
	}
}

func TestLoadAndSaveERunConfig(t *testing.T) {
	config := configpkg.ERunConfig{DefaultTenant: "old-tenant"}
	store := stubUIStore{
		config: &config,
	}
	app := NewApp(erunUIDeps{store: store})

	loaded, err := app.LoadERunConfig()
	if err != nil {
		t.Fatalf("LoadERunConfig failed: %v", err)
	}
	if loaded.DefaultTenant != "old-tenant" {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}

	saved, err := app.SaveERunConfig(uiERunConfig{DefaultTenant: " new-tenant "})
	if err != nil {
		t.Fatalf("SaveERunConfig failed: %v", err)
	}
	if saved.DefaultTenant != "new-tenant" || config.DefaultTenant != "new-tenant" {
		t.Fatalf("unexpected saved config: result=%+v stored=%+v", saved, config)
	}
}

func testUIJWT(issuer string) string {
	return testUIJWTWithSubject(issuer, "test-subject")
}

func testUIJWTWithSubject(issuer, subject string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `","sub":"` + subject + `"}`))
	return header + "." + payload + ".signature"
}

func TestLoadCloudContextStatusesRefreshesFromAWS(t *testing.T) {
	rootConfig := &configpkg.ERunConfig{
		CloudProviders: []configpkg.CloudProviderConfig{
			{Alias: "team-cloud", Provider: configpkg.CloudProviderAWS},
		},
		CloudContexts: []configpkg.CloudContextConfig{
			{
				Name:               "team-context",
				Provider:           configpkg.CloudProviderAWS,
				CloudProviderAlias: "team-cloud",
				Region:             cloud.DefaultCloudContextRegion,
				InstanceID:         "i-test",
				KubernetesContext:  "cluster-prod",
			},
		},
	}
	var awsCalls []string
	app := NewApp(erunUIDeps{
		store: stubUIStore{config: rootConfig},
		cloudContextDeps: cloud.CloudContextDependencies{
			RunAWS: func(_ core.Context, _ configpkg.CloudProviderConfig, _ string, args []string) (string, error) {
				awsCalls = append(awsCalls, strings.Join(args, " "))
				return "i-test\tstopped\n", nil
			},
		},
	})

	statuses, err := app.LoadCloudContextStatuses()
	if err != nil {
		t.Fatalf("LoadCloudContextStatuses failed: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Status != cloud.CloudContextStatusStopped {
		t.Fatalf("expected stale running status to be replaced with live stopped, got %+v", statuses)
	}
	if len(awsCalls) != 1 || !strings.Contains(awsCalls[0], "ec2 describe-instances") {
		t.Fatalf("expected one describe-instances call, got %+v", awsCalls)
	}
	if got := app.cloudContextStatus("team-context"); got != cloud.CloudContextStatusStopped {
		t.Fatalf("expected settings refresh to seed cloud-context cache with stopped, got %q", got)
	}
}

func TestApplyCloudContextStatusesToCachePreservesKnownStatusOnTransientUnknown(t *testing.T) {
	// A transient Unknown must not overwrite a known status; it only fills empty slots.
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	defer app.shutdown(context.Background())

	app.applyCloudContextStatusesToCache([]configpkg.CloudContextStatus{
		{
			CloudContextConfig: configpkg.CloudContextConfig{Name: "ctx-a"},
			Status:             cloud.CloudContextStatusRunning,
		},
		{
			CloudContextConfig: configpkg.CloudContextConfig{Name: "ctx-b"},
			Status:             cloud.CloudContextStatusStopped,
		},
	})
	if got := app.cloudContextStatus("ctx-a"); got != cloud.CloudContextStatusRunning {
		t.Fatalf("seed running, got %q", got)
	}
	if got := app.cloudContextStatus("ctx-b"); got != cloud.CloudContextStatusStopped {
		t.Fatalf("seed stopped, got %q", got)
	}

	app.applyCloudContextStatusesToCache([]configpkg.CloudContextStatus{
		{
			CloudContextConfig: configpkg.CloudContextConfig{Name: "ctx-a"},
			Status:             cloud.CloudContextStatusUnknown,
			Message:            "status refresh failed: token expired",
		},
		{
			CloudContextConfig: configpkg.CloudContextConfig{Name: "ctx-b"},
			Status:             cloud.CloudContextStatusRunning,
		},
		{
			CloudContextConfig: configpkg.CloudContextConfig{Name: "ctx-c"},
			Status:             cloud.CloudContextStatusUnknown,
		},
	})
	if got := app.cloudContextStatus("ctx-a"); got != cloud.CloudContextStatusRunning {
		t.Fatalf("transient unknown should not overwrite running, got %q", got)
	}
	if got := app.cloudContextStatus("ctx-b"); got != cloud.CloudContextStatusRunning {
		t.Fatalf("authoritative transition stopped->running should overwrite, got %q", got)
	}
	if got := app.cloudContextStatus("ctx-c"); got != cloud.CloudContextStatusUnknown {
		t.Fatalf("first-observation unknown should populate empty slot, got %q", got)
	}
}

func TestSaveRemoteEnvironmentConfigSetsCloudAliasViaMCP(t *testing.T) {
	projectRoot := openpkg.RemoteWorktreePathForRepoName("frs")
	rootConfig := &configpkg.ERunConfig{
		CloudContexts: []configpkg.CloudContextConfig{{
			Name:               "team-context",
			CloudProviderAlias: "team-cloud",
			KubernetesContext:  "cluster-dev",
		}},
	}
	store := stubUIStore{
		config: rootConfig,
		tenants: map[string]configpkg.TenantConfig{
			"frs": {
				Name:               "frs",
				DefaultEnvironment: "dev",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/dev": {
				Name:               "dev",
				LocalRepoPath:      projectRoot,
				KubernetesContext:  "cluster-dev",
				CloudProviderAlias: "old-cloud",
				Type:               configpkg.EnvironmentTypeRuntime,
			},
		},
	}
	var remoteEndpoint string
	var remoteTenant string
	var remoteEnvironment string
	var remoteAlias string
	app := NewApp(erunUIDeps{
		store:               store,
		canConnectLocalPort: func(int) bool { return true },
		canReachMCPEndpoint: func(int) bool { return true },
		setRemoteCloudAlias: func(_ context.Context, endpoint, _, tenant, environment, alias string) (configpkg.EnvConfig, error) {
			remoteEndpoint = endpoint
			remoteTenant = tenant
			remoteEnvironment = environment
			remoteAlias = alias
			return configpkg.EnvConfig{Name: environment, CloudProviderAlias: alias}, nil
		},
	})

	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "dev"}, uiEnvironmentConfig{
		Name:               "dev",
		CloudProviderAlias: "team-cloud",
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	if saved.CloudProviderAlias != "team-cloud" || store.envs["frs/dev"].CloudProviderAlias != "team-cloud" || !store.envs["frs/dev"].ManagedCloud {
		t.Fatalf("unexpected saved config: result=%+v stored=%+v", saved, store.envs["frs/dev"])
	}
	if remoteEndpoint != "http://127.0.0.1:17000/mcp" || remoteTenant != "frs" || remoteEnvironment != "dev" || remoteAlias != "team-cloud" {
		t.Fatalf("unexpected remote alias call: endpoint=%q tenant=%q environment=%q alias=%q", remoteEndpoint, remoteTenant, remoteEnvironment, remoteAlias)
	}
}
