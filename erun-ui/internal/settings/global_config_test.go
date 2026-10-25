package settings

import (
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

func TestLoadAndSaveTenantConfig(t *testing.T) {
	rootConfig := configpkg.ERunConfig{CloudProviders: []model.CloudProviderConfig{{
		Alias:         "team-cloud",
		Provider:      configpkg.CloudProviderAWS,
		OIDCIssuerURL: "https://issuer.team.example",
	}}}
	store := fakeStore{
		config: &rootConfig,
		tenants: map[string]model.TenantConfig{
			"frs": {
				Name:                      "frs",
				DefaultEnvironment:        "dev",
				APIURL:                    "https://api.old.example",
				CloudProviderAliases:      []string{"team-cloud"},
				PrimaryCloudProviderAlias: "team-cloud",
			},
		},
	}
	svc := newTestService(Deps{Store: store})

	loaded, err := svc.LoadTenantConfig(" frs ")
	if err != nil {
		t.Fatalf("LoadTenantConfig failed: %v", err)
	}
	assertLoadedTenantConfig(t, loaded)

	saved, err := svc.SaveTenantConfig(TenantConfig{
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

func assertLoadedTenantConfig(t *testing.T, loaded TenantConfig) {
	t.Helper()

	if loaded.Name != "frs" || loaded.DefaultEnvironment != "dev" || loaded.APIURL != "https://api.old.example" || loaded.PrimaryCloudProviderAlias != "team-cloud" || len(loaded.CloudProviderAliases) != 1 || loaded.CloudProviderAliases[0] != "team-cloud" {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}
	if len(loaded.CloudProviders) != 1 || loaded.CloudProviders[0].OIDCIssuerURL != "https://issuer.team.example" {
		t.Fatalf("expected cloud provider statuses with issuer URL, got %+v", loaded.CloudProviders)
	}
}

func TestSetupCloudProviderOIDCStoresIssuer(t *testing.T) {
	rootConfig := configpkg.ERunConfig{CloudProviders: []model.CloudProviderConfig{{
		Alias:    "team-cloud",
		Provider: configpkg.CloudProviderAWS,
		Profile:  "team",
		Username: "Rihards.Freimanis",
	}}}
	store := fakeStore{config: &rootConfig}
	svc := newTestService(Deps{
		Store: store,
		Cloud: cloud.CloudDependencies{
			RunAWSBearerToken: func(_ core.Context, profile, audience string) (string, error) {
				if profile != "team" || audience != configpkg.CloudProviderBearerAudience {
					t.Fatalf("unexpected bearer token input profile=%q audience=%q", profile, audience)
				}
				return testUIJWT("https://sts.aws.example/.well-known/openid-configuration"), nil
			},
			CheckAWSStatus: func(_ core.Context, provider model.CloudProviderConfig) model.CloudProviderStatus {
				return model.CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusActive}
			},
		},
	})

	status, err := svc.SetupCloudProviderOIDC("team-cloud")
	if err != nil {
		t.Fatalf("SetupCloudProviderOIDC failed: %v", err)
	}
	if status.OIDCIssuerURL != "https://sts.aws.example" || rootConfig.CloudProviders[0].OIDCIssuerURL != "https://sts.aws.example" {
		t.Fatalf("expected OIDC issuer to be stored, status=%+v config=%+v", status, rootConfig)
	}
}

func TestGetCloudProviderBearerTokenReturnsTokenAndStatus(t *testing.T) {
	jwt := testUIJWT("https://sts.aws.example/.well-known/openid-configuration")
	rootConfig := configpkg.ERunConfig{CloudProviders: []model.CloudProviderConfig{{
		Alias:    "team-cloud",
		Provider: configpkg.CloudProviderAWS,
		Profile:  "team",
		Username: "Rihards.Freimanis",
	}}}
	svc := newTestService(Deps{
		Store: fakeStore{config: &rootConfig},
		Cloud: cloud.CloudDependencies{
			RunAWSBearerToken: func(_ core.Context, profile, audience string) (string, error) {
				if profile != "team" || audience != configpkg.CloudProviderBearerAudience {
					t.Fatalf("unexpected bearer token input profile=%q audience=%q", profile, audience)
				}
				return jwt, nil
			},
			CheckAWSStatus: func(_ core.Context, provider model.CloudProviderConfig) model.CloudProviderStatus {
				return model.CloudProviderStatus{CloudProviderConfig: provider, Status: configpkg.CloudTokenStatusActive}
			},
		},
	})

	token, err := svc.GetCloudProviderBearerToken(" team-cloud ")
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
	store := fakeStore{
		config: &config,
	}
	svc := newTestService(Deps{Store: store})

	loaded, err := svc.LoadERunConfig()
	if err != nil {
		t.Fatalf("LoadERunConfig failed: %v", err)
	}
	if loaded.DefaultTenant != "old-tenant" {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}

	saved, err := svc.SaveERunConfig(ERunConfig{DefaultTenant: " new-tenant "})
	if err != nil {
		t.Fatalf("SaveERunConfig failed: %v", err)
	}
	if saved.DefaultTenant != "new-tenant" || config.DefaultTenant != "new-tenant" {
		t.Fatalf("unexpected saved config: result=%+v stored=%+v", saved, config)
	}
}

func TestLoadCloudContextStatusesRefreshesFromAWS(t *testing.T) {
	rootConfig := &configpkg.ERunConfig{
		CloudProviders: []model.CloudProviderConfig{
			{Alias: "team-cloud", Provider: configpkg.CloudProviderAWS},
		},
		CloudContexts: []model.CloudContextConfig{
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
	svc := newTestService(Deps{
		Store: fakeStore{config: rootConfig},
		ContextDeps: cloud.CloudContextDependencies{
			RunAWS: func(_ core.Context, _ model.CloudProviderConfig, _ string, args []string) (string, error) {
				awsCalls = append(awsCalls, strings.Join(args, " "))
				return "i-test\tstopped\n", nil
			},
		},
	})

	statuses, err := svc.LoadCloudContextStatuses()
	if err != nil {
		t.Fatalf("LoadCloudContextStatuses failed: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Status != cloud.CloudContextStatusStopped {
		t.Fatalf("expected stale running status to be replaced with live stopped, got %+v", statuses)
	}
	if len(awsCalls) != 1 || !strings.Contains(awsCalls[0], "ec2 describe-instances") {
		t.Fatalf("expected one describe-instances call, got %+v", awsCalls)
	}
	if got := svc.d.CloudStatuses.Status("team-context"); got != cloud.CloudContextStatusStopped {
		t.Fatalf("expected settings refresh to seed cloud-context cache with stopped, got %q", got)
	}
}
