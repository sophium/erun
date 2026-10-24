package open

import (
	"github.com/sophium/erun/erun-common/base/config"
)

// openTenantResolutionStore is a minimal OpenStore for exercising
// resolveOpenTenant/resolveOpenEnvironment without a real config root.
type openTenantResolutionStore struct {
	defaultTenant      string
	defaultEnvironment string
}

func (s openTenantResolutionStore) LoadERunConfig() (config.ERunConfig, string, error) {
	if s.defaultTenant == "" {
		return config.ERunConfig{}, "", config.ErrNotInitialized
	}
	return config.ERunConfig{DefaultTenant: s.defaultTenant}, "", nil
}

func (s openTenantResolutionStore) LoadTenantConfig(name string) (config.TenantConfig, string, error) {
	return config.TenantConfig{Name: name, DefaultEnvironment: s.defaultEnvironment}, "", nil
}

func (s openTenantResolutionStore) LoadEnvConfig(tenant, environment string) (config.EnvConfig, string, error) {
	return config.EnvConfig{Name: environment}, "", nil
}
