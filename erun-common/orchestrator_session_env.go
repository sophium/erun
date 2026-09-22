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

	// OrchestratorLaunchEnvVar names this launch's nonce, which the session's
	// own hooks stamp onto the conversation id they report, so a restart
	// attaches to the launch that asked for it.
	OrchestratorLaunchEnvVar = "ERUN_ORCHESTRATOR_LAUNCH"

	// DesktopSessionEnvVar marks a process as one the desktop app started rather
	// than a shell the Operator opened by hand. Internal; not a contract.
	DesktopSessionEnvVar = "ERUN_UI_SESSION"
)

// OrchestratorSessionEnvVars returns every ERUN_* name the launcher sets on a
// host-side orchestrator session, and is the code-owned list
// erun-docs/docs/reference/env-vars.md is checked against. RuntimeOutputsDirEnvVar
// is in it because a host-side agent needs a deliverables directory for the same
// convention an in-pod agent follows, with no pod to supply one.
//
// Adding a variable here without documenting it fails
// TestOrchestratorSessionEnvVarsAreDocumented, so the page cannot silently drift
// behind the session it describes. ERUN_DEV_BIN_DIR is deliberately absent: the
// development wrapper erun-cli/run.sh reads it and no launcher sets it, so that
// test takes it from the script instead.
func OrchestratorSessionEnvVars() []string {
	return []string{
		OrchestratorIDEnvVar,
		OrchestratorLaunchEnvVar,
		DesktopSessionEnvVar,
		RuntimeOutputsDirEnvVar,
	}
}
