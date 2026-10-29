package registrylogin

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
)

// erunTestCloudStore persists saves, unlike stubCloudContextStore.
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

func erunTestProvider() model.CloudProviderConfig {
	return providerconfig.NormalizeCloudProviderConfig(model.CloudProviderConfig{
		Alias:         "erun+api.example.test@erun",
		Provider:      configpkg.CloudProviderERun,
		OIDCIssuerURL: "https://auth.example.test",
		ERun:          &model.ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "cli-client-1"},
	})
}
