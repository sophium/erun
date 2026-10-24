package orchestrator

import (
	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

const (

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
		shared.OrchestratorIDEnvVar,
		DesktopSessionEnvVar,
		eruncommon.RuntimeOutputsDirEnvVar,
	}
}
