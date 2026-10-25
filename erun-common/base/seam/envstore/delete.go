package envstore

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

type DeleteStore interface {
	LoadTenantConfig(string) (model.TenantConfig, string, error)
	SaveTenantConfig(model.TenantConfig) error
	DeleteTenantConfig(string) error
	LoadERunConfig() (config.ERunConfig, string, error)
	SaveERunConfig(config.ERunConfig) error
	LoadEnvConfig(string, string) (config.EnvConfig, string, error)
	ListEnvConfigs(string) ([]config.EnvConfig, error)
	DeleteEnvConfig(string, string) error
}
