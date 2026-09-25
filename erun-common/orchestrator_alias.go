package eruncommon

import (
	"fmt"
	"strings"
)

// orchestrator_alias.go is the CLI's writer for OrchestratorConfig.Alias, the
// counterpart of orchestrator_env_role.go's writer for
// OrchestratorEnvConfig.Role and, like it, the answer to a field that would
// otherwise exist only in config.yaml. `erun list` reads the alias back; this
// is what puts a value there.

// OrchestratorAliasNone is the CLI-facing token for declaring that an
// orchestrator holds no alias of its own again. OrchestratorConfig.Alias's own
// zero value ("") is a real state -- "resolve the way this host always has" --
// and so cannot double as a flag value that also means "no flag given", the
// same reason OrchestratorEnvRoleNone exists.
const OrchestratorAliasNone = "none"

// ParseOrchestratorAliasFlag resolves a CLI-facing alias token into the value
// OrchestratorConfig.Alias stores. It validates the *shape* only; whether the
// host actually has that alias configured is decided against the store, by
// SetOrchestratorAlias, so a dry run reports the same answer a real run would.
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

// OrchestratorAliasStore is the root-config read/write pair SetOrchestratorAlias
// needs. Deliberately narrower than OrchestratorRoleStore: an alias is checked
// against the host's own configured cloud providers and against nothing else,
// so no tenant or environment config is read to write it. The store also
// satisfies CloudReadStore, which is what ResolveERunPlatformAlias resolves
// against -- the very same resolution `erun platform --erun-alias` uses, so the
// CLI validates an orchestrator's alias exactly the way it validates its own.
type OrchestratorAliasStore interface {
	LoadERunConfig() (ERunConfig, string, error)
	SaveERunConfig(ERunConfig) error
}

// ValidateOrchestratorAlias reports whether alias names an erun platform alias
// this host has configured. An empty alias is always valid: it declares none.
//
// Validation lives here, shared by every writer, rather than in each of them,
// because an alias nothing resolves is indistinguishable from one that was
// never set: a surface that accepted one would record an attribution that
// silently does not exist. This is the single decision point the CLI writer and
// the desktop's Edit orchestrator dialog both consult, the way
// OrchestratorEnvRoleIneligibilityReason is for Role, so neither can drift from
// the other on which aliases are legal.
func ValidateOrchestratorAlias(store OrchestratorAliasStore, alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil
	}
	_, err := ResolveERunPlatformAlias(store, alias)
	return err
}

// SetOrchestratorAlias sets which erun platform alias an orchestrator acts as.
// An empty Alias clears the field, returning the orchestrator to the host's own
// resolution.
//
// A non-empty alias must name an erun-type cloud provider alias this host has
// configured (ValidateOrchestratorAlias), and that is checked here rather than
// at use because a surface that stored an unresolvable alias would leave the
// operator with a declaration nothing honours. The refusal carries the command
// that configures one, so the dead end is a next action rather than a wall.
func SetOrchestratorAlias(ctx Context, store OrchestratorAliasStore, params SetOrchestratorAliasParams) (OrchestratorConfig, error) {
	if store == nil {
		return OrchestratorConfig{}, fmt.Errorf("store is required")
	}
	orchestratorID, err := normalizeOrchestratorAliasParams(params)
	if err != nil {
		return OrchestratorConfig{}, err
	}

	config, _, err := store.LoadERunConfig()
	if err != nil {
		return OrchestratorConfig{}, err
	}
	orchestratorIndex := findOrchestratorIndex(config, orchestratorID)
	if orchestratorIndex < 0 {
		return OrchestratorConfig{}, fmt.Errorf("orchestrator %q not found", orchestratorID)
	}

	// Resolved in dry-run too: this is a local config lookup that never touches
	// the network, and a dry run that accepted an alias a real run refuses would
	// be reporting a plan it cannot carry out.
	alias := strings.TrimSpace(params.Alias)
	if err := ValidateOrchestratorAlias(store, alias); err != nil {
		return OrchestratorConfig{}, fmt.Errorf("orchestrator %s: %w -- name an erun platform alias this host has configured (`erun cloud init erun --api-url <url>` adds one), or pass %q to declare none",
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
		return OrchestratorConfig{}, err
	}
	return orchestrator, nil
}

// SetOrchestratorAliasParams identifies the orchestrator to write and the alias
// to record on it. Alias is already parsed: "" declares none.
type SetOrchestratorAliasParams struct {
	OrchestratorID string
	Alias          string
}

func normalizeOrchestratorAliasParams(params SetOrchestratorAliasParams) (string, error) {
	orchestratorID := strings.TrimSpace(params.OrchestratorID)
	if orchestratorID == "" {
		return "", fmt.Errorf("set orchestrator alias: no orchestrator id given — pass one explicitly (`erun orchestrator set-alias <id> --alias <alias>`)")
	}
	return orchestratorID, nil
}
