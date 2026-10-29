package envstore

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

type BootstrapStore interface {
	LoadERunConfig() (configpkg.ERunConfig, string, error)
	SaveERunConfig(configpkg.ERunConfig) error
	ListTenantConfigs() ([]model.TenantConfig, error)
	LoadTenantConfig(string) (model.TenantConfig, string, error)
	SaveTenantConfig(model.TenantConfig) error
	LoadEnvConfig(string, string) (configpkg.EnvConfig, string, error)
	SaveEnvConfig(string, configpkg.EnvConfig) error
}
