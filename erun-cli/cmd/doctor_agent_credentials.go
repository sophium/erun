package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
)

// reportAgentCredentials tells the operator whether this environment's runtime
// pod can actually reach the model provider erun configured for it.
//
// It exists because every other surface answers a neighbouring question. The
// helm release says the release is deployed; the pod listing says the pod is
// Running and Ready; neither reads the container's environment, and an
// environment whose chart predates the credential injection satisfies both
// while being unable to serve a single agent turn. Without this section that
// state is reportable only by the model client, which knows nothing about the
// chart and names whatever it guessed at.
//
// The section stays silent for an environment no gateway routes: such an
// environment may reach a provider through Bedrock, injected host credentials,
// or a sign-in in its own home volume, and a diagnosis has no standing to call
// it broken. Like the neighbouring host-credentials section, it also stays
// silent under --dry-run: the read is still traced by the diagnosis that owns
// it, but the section is a live report.
func reportAgentCredentials(ctx common.Context, result common.OpenResult, diagnosis common.DeployDiagnosisResult) error {
	status := diagnosis.AgentCredentials
	switch status.State {
	case "", common.RuntimeAgentCredentialNotApplicable:
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

// agentCredentialStatusLine is the whole finding in one sentence, with the next
// action attached rather than left to be inferred. Each arm says what was read,
// what it means for an agent run, and what to do about it.
func agentCredentialStatusLine(status common.RuntimeAgentCredentialStatus, result common.OpenResult) string {
	switch status.State {
	case common.RuntimeAgentCredentialConfigured:
		return fmt.Sprintf("available — the runtime pod's %s container carries %s (%s), so Claude Code in this environment is routed at the gateway erun configured.",
			status.Container, common.GatewayBaseURLEnv, status.GatewayBaseURL)
	case common.RuntimeAgentCredentialMissing:
		return fmt.Sprintf("UNAVAILABLE — the runtime pod's %s container carries no %s, so no agent job in this environment has a provider or a credential to serve a turn with and every one of them will fail.\n"+
			"erun configured the gateway %s for this environment and passes it to its chart on every deploy, so the deployed pod template is what dropped it: that chart predates the credential injection.\n"+
			"Fix: deploy this environment (%s/%s) on a runtime chart that renders it — `erun deploy %s %s` after advancing the environment's runtime chart version.",
			status.Container, common.GatewayBaseURLEnv, status.GatewayBaseURL, result.Tenant, result.Environment, result.Tenant, result.Environment)
	default:
		return fmt.Sprintf("unknown — could not read the runtime pod template's environment (%s), so this check has no answer either way.", status.ReadError)
	}
}
