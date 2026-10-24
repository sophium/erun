package orchestrator

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

const (

	// DesktopSessionEnvVar marks a desktop-started process. Internal; not a contract.
	DesktopSessionEnvVar = "ERUN_UI_SESSION"
)

// OrchestratorSessionEnvVars returns every ERUN_* name set on a host orchestrator session;
// TestOrchestratorSessionEnvVarsAreDocumented checks it against env-vars.md.
func OrchestratorSessionEnvVars() []string {
	return []string{
		shared.OrchestratorIDEnvVar,
		DesktopSessionEnvVar,
		core.RuntimeOutputsDirEnvVar,
	}
}
