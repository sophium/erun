package model

// SetOrchestratorAliasParams carries an already-parsed Alias; "" declares none.
type SetOrchestratorAliasParams struct {
	OrchestratorID string
	Alias          string
}
