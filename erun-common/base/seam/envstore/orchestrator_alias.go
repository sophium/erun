package envstore

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
)

// OrchestratorAliasStore is the root-config store SetOrchestratorAlias needs; it resolves
// aliases exactly as `erun platform --erun-alias` does.
type OrchestratorAliasStore interface {
	LoadERunConfig() (configpkg.ERunConfig, string, error)
	SaveERunConfig(configpkg.ERunConfig) error
}
