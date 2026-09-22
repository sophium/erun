package cmd

import (
	"bytes"
	"strings"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

func agentCredentialDiagnosis(state common.RuntimeAgentCredentialState) common.DeployDiagnosisResult {
	status := common.RuntimeAgentCredentialStatus{State: state, Container: common.DevopsComponentName}
	if state == common.RuntimeAgentCredentialMissing || state == common.RuntimeAgentCredentialConfigured {
		status.GatewayBaseURL = "https://openrouter.ai/api"
	}
	if state == common.RuntimeAgentCredentialUnknown {
		status.ReadError = "read the runtime pod template: deployments.apps \"frs-devops\" is forbidden"
	}
	return common.DeployDiagnosisResult{AgentCredentials: status}
}

func renderAgentCredentials(t *testing.T, ctx common.Context, diagnosis common.DeployDiagnosisResult) string {
	t.Helper()
	var out bytes.Buffer
	ctx.Stdout = &out
	result := common.OpenResult{Tenant: "frs", Environment: "build"}
	if err := reportAgentCredentials(ctx, result, diagnosis); err != nil {
		t.Fatalf("reportAgentCredentials: %v", err)
	}
	return out.String()
}

// TestReportAgentCredentialsNamesTheCauseAndTheFix is the operator-facing half
// of the defect's reproduction: the state the report described, printed as a
// finding that names what is actually wrong rather than leaving the next error
// to be read as a quota problem. Every assertion here is about what an operator
// can act on — the missing variable, the reason it is missing, and the command
// that resolves it.
func TestReportAgentCredentialsNamesTheCauseAndTheFix(t *testing.T) {
	rendered := renderAgentCredentials(t, common.Context{}, agentCredentialDiagnosis(common.RuntimeAgentCredentialMissing))

	for _, want := range []string{
		"Agent capability",
		"UNAVAILABLE",
		common.GatewayBaseURLEnv,
		"https://openrouter.ai/api",
		"predates the credential injection",
		"erun deploy frs build",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("report does not contain %q:\n%s", want, rendered)
		}
	}
	// The finding must not read as a deploy failure: the release in this state
	// is genuinely deployed, and saying otherwise would send the operator after
	// the wrong recovery.
	for _, unwanted := range []string{"roll back", "rollback", "clear pending"} {
		if strings.Contains(strings.ToLower(rendered), unwanted) {
			t.Fatalf("report suggests %q for an environment whose release is deployed:\n%s", unwanted, rendered)
		}
	}
}

// TestReportAgentCredentialsStaysSilentWhenTheCheckDoesNotApply locks the
// behaviour that keeps this from becoming noise, and from becoming a false
// accusation: an environment no gateway routes can still reach a provider by
// other means, so there is nothing to print and nothing to infer.
func TestReportAgentCredentialsStaysSilentWhenTheCheckDoesNotApply(t *testing.T) {
	ctx := common.Context{}
	rendered := renderAgentCredentials(t, ctx, agentCredentialDiagnosis(common.RuntimeAgentCredentialNotApplicable))
	if rendered != "" {
		t.Fatalf("report printed %q for an environment no gateway routes, want nothing", rendered)
	}
	rendered = renderAgentCredentials(t, ctx, common.DeployDiagnosisResult{})
	if rendered != "" {
		t.Fatalf("report printed %q for an unset status, want nothing", rendered)
	}
}

// TestReportAgentCredentialsSaysSoWhenItCouldNotRead is the "no silent success"
// half: a check that could not run must say that, rather than printing nothing
// and leaving the operator to read silence as health.
func TestReportAgentCredentialsSaysSoWhenItCouldNotRead(t *testing.T) {
	rendered := renderAgentCredentials(t, common.Context{}, agentCredentialDiagnosis(common.RuntimeAgentCredentialUnknown))
	if !strings.Contains(rendered, "unknown") || !strings.Contains(rendered, "forbidden") {
		t.Fatalf("report does not name the unreadable template:\n%s", rendered)
	}
	if strings.Contains(rendered, "UNAVAILABLE") {
		t.Fatalf("report calls an unread template unavailable, which it did not establish:\n%s", rendered)
	}
}

// TestReportAgentCredentialsSkipsAnUnreachableCluster mirrors the neighbouring
// pod-backed sections: once the diagnosis has established the cluster is
// unreachable, this says it skipped rather than paying its own kubectl timeout
// to rediscover it — and it does not dress the skip up as a missing credential.
func TestReportAgentCredentialsSkipsAnUnreachableCluster(t *testing.T) {
	diagnosis := agentCredentialDiagnosis(common.RuntimeAgentCredentialUnknown)
	diagnosis.ClusterUnreachable = true
	rendered := renderAgentCredentials(t, common.Context{}, diagnosis)
	if !strings.Contains(rendered, "skipped") {
		t.Fatalf("report does not name the skipped check:\n%s", rendered)
	}
}

// TestReportAgentCredentialsIsSilentUnderDryRun locks the section to the same
// dry-run contract its pod-backed neighbours follow.
func TestReportAgentCredentialsIsSilentUnderDryRun(t *testing.T) {
	rendered := renderAgentCredentials(t, common.Context{DryRun: true}, agentCredentialDiagnosis(common.RuntimeAgentCredentialMissing))
	if rendered != "" {
		t.Fatalf("report printed %q under --dry-run, want nothing", rendered)
	}
}

// TestReportAgentCredentialsConfirmsAHealthyEnvironment pins the positive arm,
// so the section cannot degenerate into one that only ever prints problems.
func TestReportAgentCredentialsConfirmsAHealthyEnvironment(t *testing.T) {
	rendered := renderAgentCredentials(t, common.Context{}, agentCredentialDiagnosis(common.RuntimeAgentCredentialConfigured))
	if !strings.Contains(rendered, "available") || !strings.Contains(rendered, common.GatewayBaseURLEnv) {
		t.Fatalf("report does not confirm a routed environment:\n%s", rendered)
	}
}
