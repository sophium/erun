package eruncommon

import (
	"fmt"
	"strings"
)

// OrchestratorEnvRoleNone is the CLI-facing token for explicitly declaring an
// environment's role undeclared again. OrchestratorEnvRole's own zero value
// ("") cannot double as a flag value that also means "no flag given", so a
// named sentinel is needed the same way WorktreeStorageNone stands in for
// deploy.go's own zero value.
const OrchestratorEnvRoleNone = "none"

// ParseOrchestratorEnvRoleFlag resolves a CLI/API-facing role token into the
// OrchestratorEnvRole it names, so every surface that lets an operator type a
// role validates against the exact same set IsValid already defines: "code",
// "build", "runtime", or OrchestratorEnvRoleNone for undeclared.
func ParseOrchestratorEnvRoleFlag(value string) (OrchestratorEnvRole, error) {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, OrchestratorEnvRoleNone) {
		return "", nil
	}
	role := OrchestratorEnvRole(trimmed)
	if !role.IsValid() || role == "" {
		return "", fmt.Errorf("invalid role %q: must be %q, %q, %q, or %q (undeclared)",
			value, OrchestratorEnvRoleCode, OrchestratorEnvRoleBuild, OrchestratorEnvRoleRuntime, OrchestratorEnvRoleNone)
	}
	return role, nil
}

// OrchestratorRoleStore is the root-config read/write pair plus the linked
// environment's own config read that SetOrchestratorEnvRole needs to
// re-check OrchestratorEnvRoleAllowed against the target's real type, the
// same gate the desktop's link/edit path enforces. Without LoadEnvConfig, a
// CLI operator could walk straight past that gate on an already-linked
// environment -- setting role=code on a link the desktop would never have
// allowed to be created that way -- since editing an existing link never
// re-derives the type/role pairing on its own.
type OrchestratorRoleStore interface {
	LoadERunConfig() (ERunConfig, string, error)
	SaveERunConfig(ERunConfig) error
	LoadEnvConfig(tenant, environment string) (EnvConfig, string, error)
}

// SetOrchestratorEnvRoleParams identifies one orchestrator-to-environment
// link and the role to set on it.
type SetOrchestratorEnvRoleParams struct {
	OrchestratorID string
	Tenant         string
	Environment    string
	Role           OrchestratorEnvRole
}

// SetOrchestratorEnvRole sets the role an orchestrator uses one of its
// already-linked environments for. It is the CLI's writer for
// OrchestratorEnvConfig.Role, the counterpart to the desktop's
// UpdateOrchestrator. It validates the role itself (OrchestratorEnvRole.IsValid)
// and then, exactly like the desktop's link/edit gate, re-checks the role
// against the linked environment's real type (OrchestratorEnvRoleAllowed) --
// the single decision point both surfaces consult, so neither can drift from
// the other on what a role is allowed to be. A config already carrying an
// invalid pairing from before this gate existed still loads and lists fine
// (see erun list, which reads the persisted role directly and never
// resolves the environment's type to render it); this gate only blocks a new
// write that keeps or reintroduces an invalid pairing, and it blocks that
// write the same way regardless of what was persisted before it ran.
func SetOrchestratorEnvRole(ctx Context, store OrchestratorRoleStore, params SetOrchestratorEnvRoleParams) (OrchestratorConfig, error) {
	if store == nil {
		return OrchestratorConfig{}, fmt.Errorf("store is required")
	}
	orchestratorID, tenant, environment, err := normalizeOrchestratorEnvRoleParams(params)
	if err != nil {
		return OrchestratorConfig{}, err
	}
	if !params.Role.IsValid() {
		return OrchestratorConfig{}, fmt.Errorf("invalid role %q: must be %q, %q, or %q, or empty for undeclared",
			params.Role, OrchestratorEnvRoleCode, OrchestratorEnvRoleBuild, OrchestratorEnvRoleRuntime)
	}

	config, _, err := store.LoadERunConfig()
	if err != nil {
		return OrchestratorConfig{}, err
	}
	orchestratorIndex, envIndex, err := findOrchestratorEnvLink(config, orchestratorID, tenant, environment)
	if err != nil {
		return OrchestratorConfig{}, err
	}
	envConfig, _, err := store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return OrchestratorConfig{}, fmt.Errorf("resolve %s/%s's environment type to validate role %q: %w", tenant, environment, roleLabel(params.Role), err)
	}
	envType := envConfig.ResolvedType()
	if reason := OrchestratorEnvRoleIneligibilityReason(envType, params.Role); reason != "" {
		return OrchestratorConfig{}, fmt.Errorf("orchestrator %s: %s/%s is a %q environment, so it cannot take role %q -- %s",
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
		return OrchestratorConfig{}, err
	}
	return orchestrator, nil
}

// findOrchestratorEnvLink locates orchestratorID's entry in config and, within
// it, the link to tenant/environment, returning both indices so the caller can
// mutate either the orchestrator or one of its environments in place.
func findOrchestratorEnvLink(config ERunConfig, orchestratorID, tenant, environment string) (orchestratorIndex, envIndex int, err error) {
	orchestratorIndex = -1
	for i, orchestrator := range config.Orchestrators {
		if orchestrator.ID == orchestratorID {
			orchestratorIndex = i
			break
		}
	}
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

// roleLabel renders role the way an operator reads it back: the bare value
// for a declared role, "undeclared" for the empty zero value, matching what
// the set-role command itself echoes on a real run.
func roleLabel(role OrchestratorEnvRole) string {
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
