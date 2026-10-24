package seam

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// TerraformStore is the config surface RunTerraform reads.
type TerraformStore interface {
	LoadERunConfig() (configpkg.ERunConfig, string, error)
	LoadTenantConfig(string) (model.TenantConfig, string, error)
	LoadEnvConfig(string, string) (configpkg.EnvConfig, string, error)
	ResolveEffectiveKubernetesContext(environment, configured string) string
}

// TerraformConfirmFunc makes the operator restate the environment name before
// apply/destroy; it is never called for plan or dry-run.
type TerraformConfirmFunc func(ctx core.Context, environment string) error
