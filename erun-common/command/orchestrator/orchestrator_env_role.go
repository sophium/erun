package orchestrator

import (
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// OrchestratorEnvRoleNone is the CLI token for undeclaring a role, since "" means "no flag given".
const OrchestratorEnvRoleNone = "none"

// ParseOrchestratorEnvRoleFlag resolves a role token: "code", "build", "runtime", or OrchestratorEnvRoleNone.
func ParseOrchestratorEnvRoleFlag(value string) (configpkg.OrchestratorEnvRole, error) {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, OrchestratorEnvRoleNone) {
		return "", nil
	}
	role := configpkg.OrchestratorEnvRole(trimmed)
	if !role.IsValid() || role == "" {
		return "", fmt.Errorf("invalid role %q: must be %q, %q, %q, or %q (undeclared)",
			value, configpkg.OrchestratorEnvRoleCode, configpkg.OrchestratorEnvRoleBuild, configpkg.OrchestratorEnvRoleRuntime, OrchestratorEnvRoleNone)
	}
	return role, nil
}

// OrchestratorRoleStore includes LoadEnvConfig so a role edit re-checks the environment's real type.
type OrchestratorRoleStore interface {
	LoadERunConfig() (configpkg.ERunConfig, string, error)
	SaveERunConfig(configpkg.ERunConfig) error
	LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error)
}

type SetOrchestratorEnvRoleParams struct {
	OrchestratorID string
	Tenant         string
	Environment    string
	Role           configpkg.OrchestratorEnvRole
}

// SetOrchestratorEnvRole sets the role of an already-linked environment, re-checking
// OrchestratorEnvRoleAllowed exactly as the desktop does. Invalid persisted pairings still load.
func SetOrchestratorEnvRole(ctx core.Context, store OrchestratorRoleStore, params SetOrchestratorEnvRoleParams) (configpkg.OrchestratorConfig, error) {
	if store == nil {
		return configpkg.OrchestratorConfig{}, fmt.Errorf("store is required")
	}
	orchestratorID, tenant, environment, err := normalizeOrchestratorEnvRoleParams(params)
	if err != nil {
		return configpkg.OrchestratorConfig{}, err
	}
	if !params.Role.IsValid() {
		return configpkg.OrchestratorConfig{}, fmt.Errorf("invalid role %q: must be %q, %q, or %q, or empty for undeclared",
			params.Role, configpkg.OrchestratorEnvRoleCode, configpkg.OrchestratorEnvRoleBuild, configpkg.OrchestratorEnvRoleRuntime)
	}

	config, _, err := store.LoadERunConfig()
	if err != nil {
		return configpkg.OrchestratorConfig{}, err
	}
	orchestratorIndex, envIndex, err := findOrchestratorEnvLink(config, orchestratorID, tenant, environment)
	if err != nil {
		return configpkg.OrchestratorConfig{}, err
	}
	envConfig, _, err := store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return configpkg.OrchestratorConfig{}, fmt.Errorf("resolve %s/%s's environment type to validate role %q: %w", tenant, environment, roleLabel(params.Role), err)
	}
	envType := envConfig.ResolvedType()
	if reason := configpkg.OrchestratorEnvRoleIneligibilityReason(envType, params.Role); reason != "" {
		return configpkg.OrchestratorConfig{}, fmt.Errorf("orchestrator %s: %s/%s is a %q environment, so it cannot take role %q -- %s",
			orchestratorID, tenant, environment, envType, roleLabel(params.Role), reason)
	}
	orchestrator := config.Orchestrators[orchestratorIndex]
	orchestrator.Environments[envIndex].Role = params.Role
	config.Orchestrators[orchestratorIndex] = orchestrator

	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("write orchestrator %s environment %s/%s role", orchestratorID, tenant, environment))
		return orchestrator, nil
	}
	if err := store.SaveERunConfig(config); err != nil {
		return configpkg.OrchestratorConfig{}, err
	}
	return orchestrator, nil
}

// findOrchestratorIndex returns -1 when no orchestrator carries that id.
func findOrchestratorIndex(config configpkg.ERunConfig, orchestratorID string) int {
	for i, orchestrator := range config.Orchestrators {
		if orchestrator.ID == orchestratorID {
			return i
		}
	}
	return -1
}

func findOrchestratorEnvLink(config configpkg.ERunConfig, orchestratorID, tenant, environment string) (orchestratorIndex, envIndex int, err error) {
	orchestratorIndex = findOrchestratorIndex(config, orchestratorID)
	if orchestratorIndex < 0 {
		return -1, -1, fmt.Errorf("orchestrator %q not found", orchestratorID)
	}
	envIndex = -1
	for i, env := range config.Orchestrators[orchestratorIndex].Environments {
		if env.Tenant == tenant && env.Environment == environment {
			envIndex = i
			break
		}
	}
	if envIndex < 0 {
		return -1, -1, fmt.Errorf("orchestrator %q is not linked to %s/%s", orchestratorID, tenant, environment)
	}
	return orchestratorIndex, envIndex, nil
}

// roleLabel renders "" as "undeclared".
func roleLabel(role configpkg.OrchestratorEnvRole) string {
	if role == "" {
		return "undeclared"
	}
	return string(role)
}

func normalizeOrchestratorEnvRoleParams(params SetOrchestratorEnvRoleParams) (string, string, string, error) {
	orchestratorID := strings.TrimSpace(params.OrchestratorID)
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	switch {
	case orchestratorID == "":
		return "", "", "", fmt.Errorf("set orchestrator environment role: no orchestrator id given — pass one explicitly (`erun orchestrator set-role <id> <tenant> <environment> --role <role>`)")
	case tenant == "":
		return "", "", "", fmt.Errorf("set role for orchestrator %s: no tenant given — pass one explicitly (`erun orchestrator set-role %s <tenant> <environment> --role <role>`)", orchestratorID, orchestratorID)
	case environment == "":
		return "", "", "", fmt.Errorf("set role for orchestrator %s/%s: no environment given — pass one explicitly (`erun orchestrator set-role %s %s <environment> --role <role>`)", orchestratorID, tenant, orchestratorID, tenant)
	default:
		return orchestratorID, tenant, environment, nil
	}
}
