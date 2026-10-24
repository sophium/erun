package expose

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

type listToolStore struct {
	initInteractionStore
	toolConfig    configpkg.ERunConfig
	tenantConfigs map[string]model.TenantConfig
	envConfigs    map[string]configpkg.EnvConfig
	envsByTenant  map[string][]configpkg.EnvConfig
}

func (s listToolStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.toolConfig, "", nil
}

func (s listToolStore) LoadTenantConfig(tenant string) (model.TenantConfig, string, error) {
	config, ok := s.tenantConfigs[tenant]
	if !ok {
		return model.TenantConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s listToolStore) ListTenantConfigs() ([]model.TenantConfig, error) {
	tenants := make([]model.TenantConfig, 0, len(s.tenantConfigs))
	for _, tenant := range s.tenantConfigs {
		tenants = append(tenants, tenant)
	}
	return tenants, nil
}

func (s listToolStore) LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error) {
	config, ok := s.envConfigs[tenant+"/"+environment]
	if !ok {
		return configpkg.EnvConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s listToolStore) SaveEnvConfig(tenant string, config configpkg.EnvConfig) error {
	if s.envConfigs == nil {
		s.envConfigs = make(map[string]configpkg.EnvConfig)
	}
	s.envConfigs[tenant+"/"+config.Name] = config
	return nil
}

func (s listToolStore) ListEnvConfigs(tenant string) ([]configpkg.EnvConfig, error) {
	return s.envsByTenant[tenant], nil
}
