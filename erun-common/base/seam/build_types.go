package seam

import (
	"io"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

type (
	DockerRegistryLoginFunc func(string, io.Reader, io.Writer, io.Writer) error
	BuildScriptRunnerFunc   func(string, string, []string, io.Reader, io.Writer, io.Writer) error
)

type (
	DockerImagePusherFunc func(string, int, io.Writer, io.Writer) error
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
