package shared

// Host-side orchestrator session environment, set by the desktop app at launch. It is
// disjoint from the pod's ERUN_* variables.
const (
	// OrchestratorIDEnvVar is empty for a transient (Investigate) session.
	OrchestratorIDEnvVar = "ERUN_ORCHESTRATOR_ID"
)
