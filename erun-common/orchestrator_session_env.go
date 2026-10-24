package eruncommon

// Host-side orchestrator session environment.
//
// An orchestrator session has no pod, so none of the environment-scoped ERUN_*
// variables the helm chart sets apply to it. The desktop app sets this smaller,
// disjoint set on the session process at launch, and the shared orchestrator
// contract resolves the session's scope from OrchestratorIDEnvVar: matching that
// id against the orchestrators: list in erun's config.yaml is what says which
// environments are the session's own. An identity variable an orchestrator
// cannot look up is the defect these names being in one place exists to prevent.
const (
	// OrchestratorIDEnvVar names the orchestrator's own id. Empty for a
	// transient (Investigate) session, which has no id and no linked envs.
	OrchestratorIDEnvVar = "ERUN_ORCHESTRATOR_ID"
)
