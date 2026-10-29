package providerlifecycle

import (
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/infra/cloud/providertoken"
	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
)

// erunTestCloudStore records saves, unlike stubCloudContextStore's no-op SaveERunConfig.
type erunTestCloudStore struct {
	config configpkg.ERunConfig
}

func (s *erunTestCloudStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

func (s *erunTestCloudStore) SaveERunConfig(config configpkg.ERunConfig) error {
	s.config = config
	return nil
}

func TestInitERunCloudProviderRequiresAPIURL(t *testing.T) {
	store := erunTestCloudStore{}
	if _, err := InitERunCloudProvider(core.Context{}, &store, model.InitERunCloudProviderParams{}, cloud.CloudDependencies{}); err == nil {
		t.Fatal("expected an error for a missing api url")
	}
}

func TestInitERunCloudProviderRejectsMissingIssuerOrClientID(t *testing.T) {
	cases := map[string]model.PlatformInfo{
		"missing issuer":    {CLIClientID: "cli-1"},
		"missing client id": {Issuer: "https://auth.example.test"},
	}
	for name, info := range cases {
		t.Run(name, func(t *testing.T) {
			store := erunTestCloudStore{}
			deps := cloud.CloudDependencies{FetchPlatformInfo: func(core.Context, string) (model.PlatformInfo, error) { return info, nil }}
			if _, err := InitERunCloudProvider(core.Context{}, &store, model.InitERunCloudProviderParams{APIURL: "https://api.example.test"}, deps); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func erunTestProvider() model.CloudProviderConfig {
	return providerconfig.NormalizeCloudProviderConfig(model.CloudProviderConfig{
		Alias:         "erun+api.example.test@erun",
		Provider:      configpkg.CloudProviderERun,
		OIDCIssuerURL: "https://auth.example.test",
		ERun:          &model.ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "cli-client-1"},
	})
}

func TestERunCloudProviderLogoutDeletesRefreshAndCachedAccessToken(t *testing.T) {
	secrets := secretstore.NewFileCloudSecretStore(t.TempDir())
	provider := erunTestProvider()
	provider.ERun.RefreshTokenRef = providertoken.ErunRefreshTokenRef(provider.Alias)
	if err := secrets.SaveCloudSecret(provider.ERun.RefreshTokenRef, "refresh-1"); err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	if err := providertoken.SaveCachedERunAccessToken(secrets, provider.Alias, cloud.ERunTokens{AccessToken: "access-1", ExpiresIn: time.Hour}); err != nil {
		t.Fatalf("seed cached access token: %v", err)
	}

	if err := erunCloudProviderLogout(core.Context{}, provider, cloud.CloudDependencies{CloudSecretStore: secrets}); err != nil {
		t.Fatalf("erunCloudProviderLogout: %v", err)
	}
	if _, err := secrets.LoadCloudSecret(provider.ERun.RefreshTokenRef); err == nil {
		t.Fatal("expected the refresh token to be deleted")
	}
	if _, ok := providertoken.LoadCachedERunAccessToken(secrets, provider.Alias); ok {
		t.Fatal("expected the cached access token to be deleted")
	}
}
