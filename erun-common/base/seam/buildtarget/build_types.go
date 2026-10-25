package buildtarget

import (
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

type (
	BuildContextResolverFunc func() (model.DockerBuildContext, error)
	NowFunc                  func() time.Time
)

type DockerStore interface {
	ListTenantConfigs() ([]model.TenantConfig, error)
	LoadTenantConfig(string) (model.TenantConfig, string, error)
	ListEnvConfigs(string) ([]config.EnvConfig, error)
}
