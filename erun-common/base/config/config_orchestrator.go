package config

import (
	"github.com/sophium/erun/erun-common/base/model"
)

// OrchestratorConfig is a persisted host-side AI orchestrator definition; it needs linked
// environments, directories of its own, or both.
type OrchestratorConfig struct {
	ID           string                        `yaml:"id" json:"id"`
	Name         string                        `yaml:"name" json:"name"`
	Environments []OrchestratorEnvConfig       `yaml:"environments,omitempty" json:"environments,omitempty"`
	Directories  []OrchestratorDirectoryConfig `yaml:"directories,omitempty" json:"directories,omitempty"`
	// Alias selects which configured erun alias this orchestrator acts as; empty follows the
	// machine's alias. Nothing resolves through it yet.
	Alias string `yaml:"alias,omitempty" json:"alias,omitempty"`
	// UseGateway opts this orchestrator into the gateway catalog. Opt-in rather than tri-state
	// so an environment catalog never moves the operator's own session.
	UseGateway bool `yaml:"usegateway,omitempty" json:"useGateway,omitempty"`
	// Model is the launch model; empty resolves via ResolveOrchestratorLaunch.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
	// Effort is one of ClaudeEffortLevels; empty uses the AI tab's default.
	Effort string `yaml:"effort,omitempty" json:"effort,omitempty"`
}

// OrchestratorDirectoryConfig is a directory the orchestrator works in directly, belonging to no environment.
type OrchestratorDirectoryConfig struct {
	Directory string `yaml:"directory" json:"directory"`
}

// OrchestratorEnvConfig links an environment to the orchestrator's host directory for it.
type OrchestratorEnvConfig struct {
	Tenant      string `yaml:"tenant" json:"tenant"`
	Environment string `yaml:"environment" json:"environment"`
	Directory   string `yaml:"directory" json:"directory"`
	// Role is what this orchestrator uses the environment for, independent of its type; empty is undeclared.
	Role model.OrchestratorEnvRole `yaml:"role,omitempty" json:"role,omitempty"`
}

// OrchestratorEnvRoleAllowed reports whether role may be declared for envType. Shared by
// the CLI and desktop: runtime envs take only the runtime role, which host envs refuse.
func OrchestratorEnvRoleAllowed(envType EnvironmentType, role model.OrchestratorEnvRole) bool {
	switch envType {
	case EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent:
		return true
	case EnvironmentTypeHost:
		return role != model.OrchestratorEnvRoleRuntime
	case EnvironmentTypeRuntime:
		return role == model.OrchestratorEnvRoleRuntime
	default:
		return false
	}
}

// OrchestratorEnvRoleRequiredFor returns the role envType must declare, or "" when undeclared is legal.
func OrchestratorEnvRoleRequiredFor(envType EnvironmentType) model.OrchestratorEnvRole {
	if envType == EnvironmentTypeRuntime {
		return model.OrchestratorEnvRoleRuntime
	}
	return ""
}

// OrchestratorEnvRoleIneligibilityReason explains why role is not allowed for envType; empty when allowed.
func OrchestratorEnvRoleIneligibilityReason(envType EnvironmentType, role model.OrchestratorEnvRole) string {
	if OrchestratorEnvRoleAllowed(envType, role) {
		return ""
	}
	switch envType {
	case EnvironmentTypeRuntime:
		return "Runtime environments have no worktree to review and no in-pod agent to delegate to, " +
			"so they can't be linked to an orchestrator with the code or build role. Link with the " +
			"runtime role instead to operate it directly."
	case EnvironmentTypeHost:
		return "A host environment is a directory on this machine with no pod, so there is nothing for " +
			"the runtime role to deploy, pin, or observe. Link it with the code or build role, or " +
			"leave the role undeclared."
	case EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent:
	}
	return "This environment's type isn't recognized, so it can't be linked to an orchestrator."
}
