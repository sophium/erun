package initenv

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

type initInteractionStore struct{}

func (initInteractionStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return configpkg.ERunConfig{}, "", configpkg.ErrNotInitialized
}

func (initInteractionStore) SaveERunConfig(configpkg.ERunConfig) error {
	return nil
}

func (initInteractionStore) ListTenantConfigs() ([]model.TenantConfig, error) {
	return nil, nil
}

func (initInteractionStore) LoadTenantConfig(string) (model.TenantConfig, string, error) {
	return model.TenantConfig{}, "", configpkg.ErrNotInitialized
}

func (initInteractionStore) SaveTenantConfig(model.TenantConfig) error {
	return nil
}

func (initInteractionStore) LoadEnvConfig(string, string) (configpkg.EnvConfig, string, error) {
	return configpkg.EnvConfig{}, "", configpkg.ErrNotInitialized
}

func (initInteractionStore) ListEnvConfigs(string) ([]configpkg.EnvConfig, error) {
	return nil, nil
}

func (initInteractionStore) SaveEnvConfig(string, configpkg.EnvConfig) error {
	return nil
}
