package doctor

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// reportAgentCredentials reports whether the runtime pod carries the model-provider
// credentials erun configured. Silent when no gateway routes the env, and under --dry-run.
func reportAgentCredentials(ctx core.Context, result config.OpenResult, diagnosis model.DeployDiagnosisResult) error {
	status := diagnosis.AgentCredentials
	if status.State == "" || status.State == model.RuntimeAgentCredentialNotApplicable {
		return nil
	}
	if diagnosis.ClusterUnreachable {
		return reportPodSkippedUnreachable(ctx, agentCredentialSectionHeader)
	}
	if ctx.DryRun {
		return nil
	}
	_, err := fmt.Fprintf(ctx.Stdout, "== %s ==\n%s\n\n", agentCredentialSectionHeader, agentCredentialStatusLine(status, result))
	return err
}

const agentCredentialSectionHeader = "Agent capability"

func agentCredentialStatusLine(status model.RuntimeAgentCredentialStatus, result config.OpenResult) string {
	switch status.State {
	case model.RuntimeAgentCredentialConfigured:
		return fmt.Sprintf("available — the runtime pod's %s container carries %s (%s), so Claude Code in this environment is routed at the gateway erun configured.",
			status.Container, deploy.GatewayBaseURLEnv, status.GatewayBaseURL)
	case model.RuntimeAgentCredentialMissing:
		return fmt.Sprintf("UNAVAILABLE — the runtime pod's %s container carries no %s, so no agent job in this environment has a provider or a credential to serve a turn with and every one of them will fail.\n"+
			"erun configured the gateway %s for this environment and passes it to its chart on every deploy, so the deployed pod template is what dropped it: that chart predates the credential injection.\n"+
			"Fix: deploy this environment (%s/%s) on a runtime chart that renders it — `erun deploy %s %s` after advancing the environment's runtime chart version.",
			status.Container, deploy.GatewayBaseURLEnv, status.GatewayBaseURL, result.Tenant, result.Environment, result.Tenant, result.Environment)
	case model.RuntimeAgentCredentialNotApplicable, model.RuntimeAgentCredentialUnknown:
	}
	return fmt.Sprintf("unknown — could not read the runtime pod template's environment (%s), so this check has no answer either way.", status.ReadError)
}
