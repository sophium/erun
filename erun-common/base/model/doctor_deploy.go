package model

// DeployDiagnosisResult holds the deploy-failure diagnosis. A caller recommending
// a mutating recovery must check HelmReadError first: it is set when the read
// itself could not be trusted, unlike a confirmed "no release" or "failed".
type DeployDiagnosisResult struct {
	HelmStatus    string
	HelmReadError string
	Pods          string
	// ClusterUnreachable lets later doctor sections skip re-probing an unreachable
	// API server and its multi-minute timeout.
	ClusterUnreachable bool
	// AgentCredentials reports whether the pod template carries the model-provider
	// wiring, which helm status and readiness cannot show.
	AgentCredentials RuntimeAgentCredentialStatus
}

// DeployRecoveryAction is a mutating recovery `erun doctor` can run; callers gate it.
type DeployRecoveryAction string

const (
	DeployRecoveryClearPendingHelm DeployRecoveryAction = "clear_pending_helm"
	// DeployRecoveryRollback rolls back to the last successfully deployed revision.
	DeployRecoveryRollback DeployRecoveryAction = "rollback"
)
