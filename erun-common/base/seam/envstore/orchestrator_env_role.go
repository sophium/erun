package envstore

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
)

// OrchestratorRoleStore includes LoadEnvConfig so a role edit re-checks the environment's real type.
type OrchestratorRoleStore interface {
	LoadERunConfig() (configpkg.ERunConfig, string, error)
	SaveERunConfig(configpkg.ERunConfig) error
	LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error)
}
