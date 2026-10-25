package deploytarget

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

type DeployStore interface {
	configpkg.OpenStore
	ListTenantConfigs() ([]model.TenantConfig, error)
	ListEnvConfigs(string) ([]configpkg.EnvConfig, error)
}

type (
	DeployContextResolverFunc func() (model.KubernetesDeployContext, error)
)

type EnvConfigSaver func(tenant string, config configpkg.EnvConfig) error
