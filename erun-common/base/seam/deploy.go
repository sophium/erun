package seam

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

type DeployStore interface {
	configpkg.OpenStore
	ListTenantConfigs() ([]model.TenantConfig, error)
	ListEnvConfigs(string) ([]configpkg.EnvConfig, error)
}

type (
	DeployContextResolverFunc       func() (model.KubernetesDeployContext, error)
	KubernetesDeploymentCheckerFunc func(core.Context, model.KubernetesDeploymentCheckParams) (bool, error)
)

type EnvConfigSaver func(tenant string, config configpkg.EnvConfig) error

// HelmReleaseVersionResolverFunc reads a deployed release's appVersion.
type HelmReleaseVersionResolverFunc func(ctx core.Context, releaseName, namespace, kubernetesContext string) (string, error)
