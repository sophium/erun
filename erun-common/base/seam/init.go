package seam

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

type (
	RemoteRuntimeWaitFunc func(configpkg.ShellLaunchParams) error
)

type (
	NamespaceEnsurerFunc func(string, string) error
)

type (
	ProjectFinderFunc       func() (string, string, error)
	ProjectConfigLoaderFunc func(string) (configpkg.ProjectConfig, string, error)
)
