package model

type SetOrchestratorEnvRoleParams struct {
	OrchestratorID string
	Tenant         string
	Environment    string
	Role           OrchestratorEnvRole
}
