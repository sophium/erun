package orchestratorrole

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

// OrchestratorEnvRoleAllowed reports whether role may be declared for envType. Shared by
// the CLI and desktop: runtime envs take only the runtime role, which host envs refuse.
func OrchestratorEnvRoleAllowed(envType config.EnvironmentType, role model.OrchestratorEnvRole) bool {
	switch envType {
	case config.EnvironmentTypeLocalAgent, config.EnvironmentTypeRemoteAgent:
		return true
	case config.EnvironmentTypeHost:
		return role != model.OrchestratorEnvRoleRuntime
	case config.EnvironmentTypeRuntime:
		return role == model.OrchestratorEnvRoleRuntime
	default:
		return false
	}
}

// OrchestratorEnvRoleRequiredFor returns the role envType must declare, or "" when undeclared is legal.
func OrchestratorEnvRoleRequiredFor(envType config.EnvironmentType) model.OrchestratorEnvRole {
	if envType == config.EnvironmentTypeRuntime {
		return model.OrchestratorEnvRoleRuntime
	}
	return ""
}

// OrchestratorEnvRoleIneligibilityReason explains why role is not allowed for envType; empty when allowed.
func OrchestratorEnvRoleIneligibilityReason(envType config.EnvironmentType, role model.OrchestratorEnvRole) string {
	if OrchestratorEnvRoleAllowed(envType, role) {
		return ""
	}
	switch envType {
	case config.EnvironmentTypeRuntime:
		return "Runtime environments have no worktree to review and no in-pod agent to delegate to, " +
			"so they can't be linked to an orchestrator with the code or build role. Link with the " +
			"runtime role instead to operate it directly."
	case config.EnvironmentTypeHost:
		return "A host environment is a directory on this machine with no pod, so there is nothing for " +
			"the runtime role to deploy, pin, or observe. Link it with the code or build role, or " +
			"leave the role undeclared."
	case config.EnvironmentTypeLocalAgent, config.EnvironmentTypeRemoteAgent:
	}
	return "This environment's type isn't recognized, so it can't be linked to an orchestrator."
}
