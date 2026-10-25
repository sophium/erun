package doctor

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	diagnosispkg "github.com/sophium/erun/erun-common/primitive/deploy/diagnosis"
)

func agentCredentialDiagnosis(state model.RuntimeAgentCredentialState) model.DeployDiagnosisResult {
	status := model.RuntimeAgentCredentialStatus{State: state, Container: config.DevopsComponentName}
	if state == model.RuntimeAgentCredentialMissing || state == model.RuntimeAgentCredentialConfigured {
		status.GatewayBaseURL = "https://openrouter.ai/api"
	}
	if state == model.RuntimeAgentCredentialUnknown {
		status.ReadError = "read the runtime pod template: deployments.apps \"frs-devops\" is forbidden"
	}
	return model.DeployDiagnosisResult{AgentCredentials: status}
}

func renderAgentCredentials(t *testing.T, ctx core.Context, diagnosis model.DeployDiagnosisResult) string {
	t.Helper()
	var out bytes.Buffer
	ctx.Stdout = &out
	result := config.OpenResult{Tenant: "frs", Environment: "build"}
	if err := reportAgentCredentials(ctx, result, diagnosis); err != nil {
		t.Fatalf("reportAgentCredentials: %v", err)
	}
	return out.String()
}

func TestReportAgentCredentialsNamesTheCauseAndTheFix(t *testing.T) {
	rendered := renderAgentCredentials(t, core.Context{}, agentCredentialDiagnosis(model.RuntimeAgentCredentialMissing))

	for _, want := range []string{
		"Agent capability",
		"UNAVAILABLE",
		diagnosispkg.GatewayBaseURLEnv,
		"https://openrouter.ai/api",
		"predates the credential injection",
		"erun deploy frs build",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("report does not contain %q:\n%s", want, rendered)
		}
	}
	// The release is genuinely deployed; this must not read as a deploy failure.
	for _, unwanted := range []string{"roll back", "rollback", "clear pending"} {
		if strings.Contains(strings.ToLower(rendered), unwanted) {
			t.Fatalf("report suggests %q for an environment whose release is deployed:\n%s", unwanted, rendered)
		}
	}
}

func TestReportAgentCredentialsStaysSilentWhenTheCheckDoesNotApply(t *testing.T) {
	ctx := core.Context{}
	rendered := renderAgentCredentials(t, ctx, agentCredentialDiagnosis(model.RuntimeAgentCredentialNotApplicable))
	if rendered != "" {
		t.Fatalf("report printed %q for an environment no gateway routes, want nothing", rendered)
	}
	rendered = renderAgentCredentials(t, ctx, model.DeployDiagnosisResult{})
	if rendered != "" {
		t.Fatalf("report printed %q for an unset status, want nothing", rendered)
	}
}

func TestReportAgentCredentialsSaysSoWhenItCouldNotRead(t *testing.T) {
	rendered := renderAgentCredentials(t, core.Context{}, agentCredentialDiagnosis(model.RuntimeAgentCredentialUnknown))
	if !strings.Contains(rendered, "unknown") || !strings.Contains(rendered, "forbidden") {
		t.Fatalf("report does not name the unreadable template:\n%s", rendered)
	}
	if strings.Contains(rendered, "UNAVAILABLE") {
		t.Fatalf("report calls an unread template unavailable, which it did not establish:\n%s", rendered)
	}
}

func TestReportAgentCredentialsSkipsAnUnreachableCluster(t *testing.T) {
	diagnosis := agentCredentialDiagnosis(model.RuntimeAgentCredentialUnknown)
	diagnosis.ClusterUnreachable = true
	rendered := renderAgentCredentials(t, core.Context{}, diagnosis)
	if !strings.Contains(rendered, "skipped") {
		t.Fatalf("report does not name the skipped check:\n%s", rendered)
	}
}

func TestReportAgentCredentialsIsSilentUnderDryRun(t *testing.T) {
	rendered := renderAgentCredentials(t, core.Context{DryRun: true}, agentCredentialDiagnosis(model.RuntimeAgentCredentialMissing))
	if rendered != "" {
		t.Fatalf("report printed %q under --dry-run, want nothing", rendered)
	}
}

func TestReportAgentCredentialsConfirmsAHealthyEnvironment(t *testing.T) {
	rendered := renderAgentCredentials(t, core.Context{}, agentCredentialDiagnosis(model.RuntimeAgentCredentialConfigured))
	if !strings.Contains(rendered, "available") || !strings.Contains(rendered, diagnosispkg.GatewayBaseURLEnv) {
		t.Fatalf("report does not confirm a routed environment:\n%s", rendered)
	}
}
