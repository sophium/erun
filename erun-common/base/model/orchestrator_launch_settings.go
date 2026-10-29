package model

type SetOrchestratorGatewayParams struct {
	OrchestratorID string
	UseGateway     bool
}

// SetOrchestratorModelParams carries an already-parsed Model; "" clears it.
type SetOrchestratorModelParams struct {
	OrchestratorID string
	Model          string
}

// SetOrchestratorEffortParams carries an already-parsed Effort; "" clears it.
type SetOrchestratorEffortParams struct {
	OrchestratorID string
	Effort         string
}
