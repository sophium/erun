package envstore

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

// TerraformStore is the config surface RunTerraform reads.
type TerraformStore interface {
	LoadERunConfig() (configpkg.ERunConfig, string, error)
	LoadTenantConfig(string) (model.TenantConfig, string, error)
	LoadEnvConfig(string, string) (configpkg.EnvConfig, string, error)
	ResolveEffectiveKubernetesContext(environment, configured string) string
}
