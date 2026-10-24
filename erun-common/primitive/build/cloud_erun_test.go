package build

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
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

func erunTestProvider() configpkg.CloudProviderConfig {
	return cloud.NormalizeCloudProviderConfig(configpkg.CloudProviderConfig{
		Alias:         "erun+api.example.test@erun",
		Provider:      configpkg.CloudProviderERun,
		OIDCIssuerURL: "https://auth.example.test",
		ERun:          &configpkg.ERunProviderConfig{APIURL: "https://api.example.test", ClientID: "cli-client-1"},
	})
}
