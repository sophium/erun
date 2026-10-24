package config

// OrchestratorConfig is a persisted host-side AI orchestrator definition. An
// orchestrator drives agent environments from the operator's machine — it reviews
// a pod-backed environment in a host directory read-only, and works a host
// environment in its own directory directly, since no pod owns it — and may
// instead, or as well, name directories of its own, which belong to no environment
// at all. A definition needs at least one of the two; neither alone is required.
type OrchestratorConfig struct {
	ID           string                        `yaml:"id" json:"id"`
	Name         string                        `yaml:"name" json:"name"`
	Environments []OrchestratorEnvConfig       `yaml:"environments,omitempty" json:"environments,omitempty"`
	Directories  []OrchestratorDirectoryConfig `yaml:"directories,omitempty" json:"directories,omitempty"`
	// Alias names the erun platform alias this orchestrator declares as its
	// own: which configured erun-type cloud alias it acts as. Empty -- the
	// default -- declares none of its own and leaves it following this
	// machine's own alias.
	//
	// It is the orchestrator-scoped counterpart of
	// TenantConfig.PrimaryCloudProviderAlias, and a *selection* among aliases
	// this host already has, never a credential: an orchestrator session has no
	// pod and inherits the host's own config and secret store through its PTY,
	// so naming an alias here changes whose identity that is not at all.
	//
	// Nothing resolves through it yet. An orchestrator's platform calls still
	// resolve this machine's alias the way they always have; this field is the
	// mapping that work needs in place first, and giving an orchestrator a
	// credential of its own remains a separate decision.
	Alias string `yaml:"alias,omitempty" json:"alias,omitempty"`
	// UseGateway routes this orchestrator's Claude session through the
	// erun-level gateway catalog (ERunConfig.OpenRouter). False -- the default
	// -- leaves it on this machine's own Claude Code configuration, which is
	// what every orchestrator ran on before the field existed. It is an opt-in
	// rather than the environment-style tri-state on purpose: an orchestrator
	// runs on the operator's own machine under their own sign-in, so a catalog
	// configured for environments must not silently move it.
	UseGateway bool `yaml:"usegateway,omitempty" json:"useGateway,omitempty"`
	// Model is the model the session and its subagents launch on. Empty
	// resolves to the gateway catalog's default under the gateway, and to
	// DefaultOrchestratorModel otherwise (ResolveOrchestratorLaunch).
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
	// Effort is one of ClaudeEffortLevels. Empty resolves to the same default
	// an environment's AI tab launches with.
	Effort string `yaml:"effort,omitempty" json:"effort,omitempty"`
}

// OrchestratorDirectoryConfig is one directory the orchestrator operates in,
// named by path alone. It is deliberately not an environment: it carries no
// tenant, no environment name, no runtime version or image, and no role, because
// none of those describe a directory with no pod, no cluster, and no lifecycle
// behind it. It exists so an orchestrator can be pointed at a directory on this
// machine without registering an environment for it first; the orchestrator
// authors and builds there directly, the same way it does in a host
// environment's own directory.
type OrchestratorDirectoryConfig struct {
	Directory string `yaml:"directory" json:"directory"`
}

// OrchestratorEnvConfig links one agent environment to the orchestrator's window
// on the host: the directory a remote-agent env's workspace sync mirrors into
// (read-only), a local-agent env's own worktree, which is already on this machine
// because its pod hostPath-mounts it (also read-only), or a host env's own
// directory, which no pod owns and the orchestrator authors in directly.
type OrchestratorEnvConfig struct {
	Tenant      string `yaml:"tenant" json:"tenant"`
	Environment string `yaml:"environment" json:"environment"`
	Directory   string `yaml:"directory" json:"directory"`
	// Role states what this orchestrator uses the environment for. It is
	// orthogonal to EnvironmentType: type is where the worktree lives, role is
	// what this orchestrator asks the environment to do, and several
	// orchestrators can link the same environment with different roles. Empty
	// means undeclared, never a default of either OrchestratorEnvRoleCode,
	// OrchestratorEnvRoleBuild, or OrchestratorEnvRoleRuntime. The two are
	// independent even where they share a spelling: a runtime-*type*
	// environment linked with role "code" is still refused (see
	// OrchestratorEnvRoleAllowed) -- nothing here derives one from the other.
	Role OrchestratorEnvRole `yaml:"role,omitempty" json:"role,omitempty"`
}

// OrchestratorEnvRole is what an orchestrator uses a linked environment for.
type OrchestratorEnvRole string

const (
	// OrchestratorEnvRoleCode is an environment that writes code, iterates
	// fast, and pushes feature branches. It does not run full regressions.
	OrchestratorEnvRoleCode OrchestratorEnvRole = "code"
	// OrchestratorEnvRoleBuild is an environment that checks out pushed
	// feature branches, runs the gates, fixes what the gates surface, and cuts
	// releases.
	OrchestratorEnvRoleBuild OrchestratorEnvRole = "build"
	// OrchestratorEnvRoleRuntime is an environment this orchestrator
	// operates -- deploy, pin, observe -- rather than reviews or delegates
	// to. A runtime environment has no worktree and no in-pod agent, so this
	// is the only role that may be declared for one; see
	// OrchestratorEnvRoleAllowed.
	OrchestratorEnvRoleRuntime OrchestratorEnvRole = "runtime"
)

// IsValid reports whether r is either undeclared (empty) or a known role.
func (r OrchestratorEnvRole) IsValid() bool {
	switch r {
	case "", OrchestratorEnvRoleCode, OrchestratorEnvRoleBuild, OrchestratorEnvRoleRuntime:
		return true
	default:
		return false
	}
}

// OrchestratorEnvRoleAllowed reports whether role may be declared for a link
// to an environment of envType -- the single decision the CLI's
// SetOrchestratorEnvRole and the desktop's link/edit gate both consult, so
// neither can drift from the other on what a role is allowed to be. A
// local-agent or remote-agent environment has a worktree to review and an
// in-pod agent to delegate to, so any role -- including undeclared -- is fine.
// A host environment has a real worktree on this machine too, so code, build,
// and undeclared are equally fine -- but not the runtime role: that one means
// operate the environment directly (deploy, pin, observe), and every one of
// those refuses a host environment, which has no pod for them to act on. A
// runtime environment is the mirror image -- no worktree to review and no
// in-pod agent to delegate to -- so only OrchestratorEnvRoleRuntime may be
// declared for it; code and build, which both presuppose what it lacks, are
// refused, the same as any type this function does not recognize.
func OrchestratorEnvRoleAllowed(envType EnvironmentType, role OrchestratorEnvRole) bool {
	switch envType {
	case EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent:
		return true
	case EnvironmentTypeHost:
		return role != OrchestratorEnvRoleRuntime
	case EnvironmentTypeRuntime:
		return role == OrchestratorEnvRoleRuntime
	default:
		return false
	}
}

// OrchestratorEnvRoleRequiredFor returns the one role a link to an
// environment of envType must declare for OrchestratorEnvRoleAllowed to
// accept it, or "" when nothing must be declared -- which is not the same as
// every role being allowed: a host environment requires no role while still
// refusing one, so "" here means "undeclared is legal", not "anything goes".
// Only a runtime environment constrains this today.
func OrchestratorEnvRoleRequiredFor(envType EnvironmentType) OrchestratorEnvRole {
	if envType == EnvironmentTypeRuntime {
		return OrchestratorEnvRoleRuntime
	}
	return ""
}

// OrchestratorEnvRoleIneligibilityReason explains, in the operator's words,
// why role may not be declared for envType -- the shared reason both the
// CLI's SetOrchestratorEnvRole and the desktop's link/edit gate surface, so
// an operator sees the same explanation regardless of which surface refused
// them. Empty when OrchestratorEnvRoleAllowed(envType, role) is true.
func OrchestratorEnvRoleIneligibilityReason(envType EnvironmentType, role OrchestratorEnvRole) string {
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
