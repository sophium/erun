package orchestrator

import (
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/cloudconfig"
)

// OrchestratorAliasNone is the CLI token for clearing the alias, since "" already means
// "no flag given".
const OrchestratorAliasNone = "none"

// ParseOrchestratorAliasFlag validates the alias token's shape only; SetOrchestratorAlias checks the store.
func ParseOrchestratorAliasFlag(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, OrchestratorAliasNone) {
		return "", nil
	}
	if trimmed == "" {
		return "", fmt.Errorf("invalid alias %q: must be a configured erun platform alias, or %q to declare none",
			value, OrchestratorAliasNone)
	}
	return trimmed, nil
}

// OrchestratorAliasStore is the root-config store SetOrchestratorAlias needs; it resolves
// aliases exactly as `erun platform --erun-alias` does.
type OrchestratorAliasStore interface {
	LoadERunConfig() (configpkg.ERunConfig, string, error)
	SaveERunConfig(configpkg.ERunConfig) error
}

// ValidateOrchestratorAlias reports whether alias names a configured erun platform alias; ""
// is valid. It is the single check shared by the CLI and the desktop.
func ValidateOrchestratorAlias(store OrchestratorAliasStore, alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil
	}
	_, err := cloudconfig.ResolveERunPlatformAlias(store, alias)
	return err
}

// SetOrchestratorAlias sets the erun platform alias an orchestrator acts as; "" clears it.
func SetOrchestratorAlias(ctx core.Context, store OrchestratorAliasStore, params model.SetOrchestratorAliasParams) (configpkg.OrchestratorConfig, error) {
	if store == nil {
		return configpkg.OrchestratorConfig{}, fmt.Errorf("store is required")
	}
	orchestratorID, err := normalizeOrchestratorAliasParams(params)
	if err != nil {
		return configpkg.OrchestratorConfig{}, err
	}

	config, _, err := store.LoadERunConfig()
	if err != nil {
		return configpkg.OrchestratorConfig{}, err
	}
	orchestratorIndex := findOrchestratorIndex(config, orchestratorID)
	if orchestratorIndex < 0 {
		return configpkg.OrchestratorConfig{}, fmt.Errorf("orchestrator %q not found", orchestratorID)
	}

	// Resolved in dry-run too, so a dry run refuses what a real run refuses.
	alias := strings.TrimSpace(params.Alias)
	if err := ValidateOrchestratorAlias(store, alias); err != nil {
		return configpkg.OrchestratorConfig{}, fmt.Errorf("orchestrator %s: %w -- name an erun platform alias this host has configured (`erun cloud init erun --api-url <url>` adds one), or pass %q to declare none",
			orchestratorID, err, OrchestratorAliasNone)
	}

	orchestrator := config.Orchestrators[orchestratorIndex]
	orchestrator.Alias = alias
	config.Orchestrators[orchestratorIndex] = orchestrator

	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("write orchestrator %s alias", orchestratorID))
		return orchestrator, nil
	}
	if err := store.SaveERunConfig(config); err != nil {
		return configpkg.OrchestratorConfig{}, err
	}
	return orchestrator, nil
}

func normalizeOrchestratorAliasParams(params model.SetOrchestratorAliasParams) (string, error) {
	orchestratorID := strings.TrimSpace(params.OrchestratorID)
	if orchestratorID == "" {
		return "", fmt.Errorf("set orchestrator alias: no orchestrator id given — pass one explicitly (`erun orchestrator set-alias <id> --alias <alias>`)")
	}
	return orchestratorID, nil
}
