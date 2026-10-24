package model

// OrchestratorEnvRole is what an orchestrator uses a linked environment for.
type OrchestratorEnvRole string

const (
	// OrchestratorEnvRoleCode writes code and pushes feature branches.
	OrchestratorEnvRoleCode OrchestratorEnvRole = "code"
	// OrchestratorEnvRoleBuild runs gates on pushed branches and cuts releases.
	OrchestratorEnvRoleBuild OrchestratorEnvRole = "build"
	// OrchestratorEnvRoleRuntime is operated (deploy, pin, observe); the only role allowed for runtime envs.
	OrchestratorEnvRoleRuntime OrchestratorEnvRole = "runtime"
)

// IsValid reports whether r is a known role or undeclared (empty).
func (r OrchestratorEnvRole) IsValid() bool {
	switch r {
	case "", OrchestratorEnvRoleCode, OrchestratorEnvRoleBuild, OrchestratorEnvRoleRuntime:
		return true
	default:
		return false
	}
}
