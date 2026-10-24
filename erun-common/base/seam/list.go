package seam

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

type ListStore interface {
	configpkg.OpenStore
	ListTenantConfigs() ([]model.TenantConfig, error)
	ListEnvConfigs(string) ([]configpkg.EnvConfig, error)
}
