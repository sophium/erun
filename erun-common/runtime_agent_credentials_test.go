package eruncommon

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// reportedRuntimePodEnvNames is the `erun-devops` container's environment
// exactly as it was read off the deployed pod template of the environment that
// reported this defect: a tenant whose umbrella chart predated the credential
// injection. Every name here is load-bearing to the reproduction, including the
// two Claude Code tuning knobs at the end — they render unconditionally in the
// chart, so their presence beside a total absence of ANTHROPIC_* is precisely
// the signature of a template that has the tuning block and not the gateway
// block.
func reportedRuntimePodEnvNames() []string {
	return []string{
		"HOME",
		"KUBECONFIG",
		"DOCKER_HOST",
		"DOCKER_BUILDKIT",
		"ERUN_DIND_CPU_LIMIT",
		"ERUN_DIND_MEMORY_LIMIT_MIB",
		"ERUN_REPO_PATH",
		"ERUN_OUTPUTS_DIR",
		"ERUN_REPO_REMOTE",
		"ERUN_ENV_TYPE",
		"ERUN_CLOUD_ENVIRONMENT",
		"ERUN_CLOUD_CONTEXT_NAME",
		"ERUN_CLOUD_PROVIDER",
		"ERUN_CLOUD_PROVIDER_ALIAS",
		"ERUN_CLOUD_REGION",
		"ERUN_CLOUD_INSTANCE_ID",
		"ERUN_RUNTIME_REGISTRY",
		"ERUN_CONTAINER_REGISTRIES",
		"ERUN_DISABLE_BUILD_SCRIPT",
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS",
		"MAX_THINKING_TOKENS",
		"ERUN_KUBERNETES_CONTEXT",
		"ERUN_TENANT",
		"ERUN_ENVIRONMENT",
		"ERUN_SSHD_ENABLED",
	}
}

// reportedGateway returns the erun-level catalog the environment above was
// configured against: the operator had a gateway, which is why the absence of
// its routing variable on the pod is a defect rather than a configuration the
// operator never asked for.
func reportedGateway() *OpenRouterConfig {
	return &OpenRouterConfig{
		BaseURL: "https://openrouter.ai/api",
		Models:  []OpenRouterModel{{ID: "some/model", Context: 200000}},
	}
}

// kubectlStubLog records every invocation a stubbed kubectl received, so a test
// can assert on what was *not* asked as well as on what was.
type kubectlStubLog struct {
	path string
}

func (l kubectlStubLog) invocations(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read kubectl stub log: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func (l kubectlStubLog) askedFor(t *testing.T, fragment string) bool {
	t.Helper()
	for _, line := range l.invocations(t) {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

// writeRuntimeKubectlStub points ERUN_KUBECTL_BIN at a script that answers the
// diagnosis's pod listing with an empty list and its `get deployment` read with
// deploymentJSON, logging every invocation so a caller can tell a read that was
// skipped from one that was never needed.
func writeRuntimeKubectlStub(t *testing.T, deploymentJSON string) kubectlStubLog {
	t.Helper()
	dir := t.TempDir()
	logPath := dir + "/kubectl-invocations"
	path := dir + "/kubectl-stub"
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + logPath + "\n" +
		"case \"$*\" in\n" +
		"  *get\\ deployment*)\n" +
		"    cat <<'ERUN_TEST_DEPLOYMENT_JSON'\n" + deploymentJSON + "\nERUN_TEST_DEPLOYMENT_JSON\n" +
		"    ;;\n" +
		"  *)\n" +
		"    printf '%s' '{\"items\":[]}'\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", path)
	return kubectlStubLog{path: logPath}
}

// runtimeDeploymentJSON renders the deployed pod template the diagnosis reads,
// carrying exactly the environment variable names a stub is given.
func runtimeDeploymentJSON(container string, envNames []string) string {
	entries := make([]string, 0, len(envNames))
	for _, name := range envNames {
		entries = append(entries, fmt.Sprintf(`{"name":%q}`, name))
	}
	return fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":%q,"env":[%s]}]}}}}`,
		container, strings.Join(entries, ","))
}

// gatewayDiagnosisRequest is the request `erun doctor` and `erun environment`
// build for a gateway-routed environment.
func gatewayDiagnosisRequest(gateway *OpenRouterConfig) ShellLaunchParams {
	return ShellLaunchParams{
		Tenant:            "frs",
		Environment:       "build",
		Namespace:         "frs-build",
		KubernetesContext: "the-cluster",
		Gateway:           gateway,
	}
}

// TestRunDeployDiagnosisNamesTheMissingGatewayRouting is the reproduction of
// the reported defect.
//
// The reported environment deployed cleanly, reported healthy everywhere, and
// could run no agent at all — and that last fact had no surface, so an operator
// met it only as the model client's own error. The state under test is exactly
// the reported one on both sides: the erun-level gateway the operator
// configured, and the deployed pod template of a chart too old to consume it.
// Before this change the diagnosis had no field for the answer at all, so the
// release came back healthy and the environment came back healthy with it.
func TestRunDeployDiagnosisNamesTheMissingGatewayRouting(t *testing.T) {
	writeDoctorHelmStub(t, "NAME: frs-devops\nSTATUS: deployed\nREVISION: 42\n", "", 0)
	stub := writeRuntimeKubectlStub(t, runtimeDeploymentJSON(DevopsComponentName, reportedRuntimePodEnvNames()))

	diagnosis := RunDeployDiagnosis(testTraceContext(false), gatewayDiagnosisRequest(reportedGateway()))

	status := diagnosis.AgentCredentials
	if status.State != RuntimeAgentCredentialMissing {
		t.Fatalf("AgentCredentials.State = %q, want %q (readError=%q)",
			status.State, RuntimeAgentCredentialMissing, status.ReadError)
	}
	if status.GatewayBaseURL != "https://openrouter.ai/api" {
		t.Fatalf("AgentCredentials.GatewayBaseURL = %q, want the configured gateway", status.GatewayBaseURL)
	}
	if status.Container != DevopsComponentName {
		t.Fatalf("AgentCredentials.Container = %q, want %q", status.Container, DevopsComponentName)
	}
	if status.ReadError != "" {
		t.Fatalf("AgentCredentials.ReadError = %q, want empty: the template was read and genuinely carries nothing", status.ReadError)
	}
	if !stub.askedFor(t, "get deployment") {
		t.Fatalf("the diagnosis never read the deployed pod template; invocations: %v", stub.invocations(t))
	}
	// The defect's whole shape: the release is genuinely deployed, so the
	// recovery classifier is right to find nothing wrong with it. The agent
	// answer is the one fact that was missing, and it does not make the release
	// unhealthy.
	if action, ok := RecommendedDeployRecovery(diagnosis); ok {
		t.Fatalf("RecommendedDeployRecovery = %q, want no action for a release that is deployed", action)
	}
}

// TestRunDeployDiagnosisConfirmsGatewayRoutingWhenTheTemplateCarriesIt is the
// other half of the same disagreement: the identical environment on a template
// that *does* consume the gateway values. Without this the check would be
// indistinguishable from one that reports "missing" for everything.
func TestRunDeployDiagnosisConfirmsGatewayRoutingWhenTheTemplateCarriesIt(t *testing.T) {
	writeDoctorHelmStub(t, "NAME: frs-devops\nSTATUS: deployed\nREVISION: 42\n", "", 0)
	carrying := append([]string{GatewayBaseURLEnv, "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL"}, reportedRuntimePodEnvNames()...)
	writeRuntimeKubectlStub(t, runtimeDeploymentJSON(DevopsComponentName, carrying))

	diagnosis := RunDeployDiagnosis(testTraceContext(false), gatewayDiagnosisRequest(reportedGateway()))

	if got := diagnosis.AgentCredentials.State; got != RuntimeAgentCredentialConfigured {
		t.Fatalf("AgentCredentials.State = %q, want %q", got, RuntimeAgentCredentialConfigured)
	}
}

// TestRunDeployDiagnosisMakesNoAgentClaimWithoutAGateway is the guard against
// the false positive this check would otherwise be: the same credential-less
// pod template on an install that configured no gateway. Such an environment
// may reach a provider through Bedrock, injected host credentials, or a sign-in
// in its own home volume, and asserting it cannot run an agent would be erun
// inventing a defect — and paying a kubectl read to do it.
func TestRunDeployDiagnosisMakesNoAgentClaimWithoutAGateway(t *testing.T) {
	writeDoctorHelmStub(t, "NAME: frs-devops\nSTATUS: deployed\nREVISION: 42\n", "", 0)
	stub := writeRuntimeKubectlStub(t, runtimeDeploymentJSON(DevopsComponentName, reportedRuntimePodEnvNames()))

	diagnosis := RunDeployDiagnosis(testTraceContext(false), gatewayDiagnosisRequest(nil))

	if got := diagnosis.AgentCredentials.State; got != RuntimeAgentCredentialNotApplicable {
		t.Fatalf("AgentCredentials.State = %q, want %q", got, RuntimeAgentCredentialNotApplicable)
	}
	if stub.askedFor(t, "get deployment") {
		t.Fatalf("an install with no gateway paid for a pod-template read anyway; invocations: %v", stub.invocations(t))
	}
}

// TestRunDeployDiagnosisDoesNotCallAnUnreadTemplateMissing locks the distinction
// the whole check rests on: "could not read" is not "carries nothing". A
// diagnosis that reported Missing here would accuse an environment of a defect
// it never established.
func TestRunDeployDiagnosisDoesNotCallAnUnreadTemplateMissing(t *testing.T) {
	writeDoctorHelmStub(t, "NAME: frs-devops\nSTATUS: deployed\nREVISION: 42\n", "", 0)
	writeKubectlStub(t, `Error from server (Forbidden): deployments.apps "frs-devops" is forbidden`, 1)

	diagnosis := RunDeployDiagnosis(testTraceContext(false), gatewayDiagnosisRequest(reportedGateway()))

	status := diagnosis.AgentCredentials
	if status.State != RuntimeAgentCredentialUnknown {
		t.Fatalf("AgentCredentials.State = %q, want %q", status.State, RuntimeAgentCredentialUnknown)
	}
	if status.ReadError == "" {
		t.Fatalf("AgentCredentials.ReadError is empty, want the reason the template could not be read")
	}
	if strings.Contains(status.ReadError, "ANTHROPIC") {
		t.Fatalf("AgentCredentials.ReadError = %q, want the read failure rather than a credential claim", status.ReadError)
	}
}

// TestResolveRuntimeAgentCredentialStatus covers the pure resolver directly,
// including the two cases the diagnosis-level tests cannot reach: an
// environment that explicitly opted out of the catalog, and a template read
// that returned no such container.
func TestResolveRuntimeAgentCredentialStatus(t *testing.T) {
	optedOut := false
	cases := []struct {
		name   string
		params RuntimeAgentCredentialParams
		want   RuntimeAgentCredentialState
	}{
		{
			name:   "no catalog configured",
			params: RuntimeAgentCredentialParams{Container: DevopsComponentName, ContainerEnvObserved: true},
			want:   RuntimeAgentCredentialNotApplicable,
		},
		{
			name: "the environment opted out of the catalog",
			params: RuntimeAgentCredentialParams{
				Claude:               EnvironmentClaudeConfig{UseGateway: &optedOut},
				Gateway:              reportedGateway(),
				Container:            DevopsComponentName,
				ContainerEnvObserved: true,
			},
			want: RuntimeAgentCredentialNotApplicable,
		},
		{
			name: "the template carries the routing variable",
			params: RuntimeAgentCredentialParams{
				Gateway:              reportedGateway(),
				Container:            DevopsComponentName,
				ContainerEnvObserved: true,
				ContainerEnvNames:    []string{"HOME", GatewayBaseURLEnv},
			},
			want: RuntimeAgentCredentialConfigured,
		},
		{
			// A padded name is still the variable the container sets; the
			// comparison normalizes rather than trusting the read's spacing.
			name: "a padded routing variable still counts",
			params: RuntimeAgentCredentialParams{
				Gateway:              reportedGateway(),
				Container:            DevopsComponentName,
				ContainerEnvObserved: true,
				ContainerEnvNames:    []string{" " + GatewayBaseURLEnv + " "},
			},
			want: RuntimeAgentCredentialConfigured,
		},
		{
			name: "the template was read and carries nothing",
			params: RuntimeAgentCredentialParams{
				Gateway:              reportedGateway(),
				Container:            DevopsComponentName,
				ContainerEnvObserved: true,
				ContainerEnvNames:    reportedRuntimePodEnvNames(),
			},
			want: RuntimeAgentCredentialMissing,
		},
		{
			name: "the template was never read",
			params: RuntimeAgentCredentialParams{
				Gateway:   reportedGateway(),
				Container: DevopsComponentName,
			},
			want: RuntimeAgentCredentialUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveRuntimeAgentCredentialStatus(tc.params)
			if got.State != tc.want {
				t.Fatalf("State = %q, want %q", got.State, tc.want)
			}
			if tc.want == RuntimeAgentCredentialMissing && got.GatewayBaseURL == "" {
				t.Fatalf("a missing-routing verdict must still name the gateway that is being dropped")
			}
		})
	}
}

// TestRuntimeDeploymentContainerEnvNames locks the reader's two refusals: an
// unparsable document and a template with no such container are both "could not
// observe", never an empty environment that would read as a missing credential.
func TestRuntimeDeploymentContainerEnvNames(t *testing.T) {
	if _, found := runtimeDeploymentContainerEnvNames([]byte("not json"), DevopsComponentName); found {
		t.Fatalf("an unparsable document reported a container")
	}
	other := runtimeDeploymentJSON("some-other-container", reportedRuntimePodEnvNames())
	if _, found := runtimeDeploymentContainerEnvNames([]byte(other), DevopsComponentName); found {
		t.Fatalf("a template with no %q container reported one", DevopsComponentName)
	}
	names, found := runtimeDeploymentContainerEnvNames(
		[]byte(runtimeDeploymentJSON(DevopsComponentName, []string{"HOME", "", "  ", "ERUN_TENANT"})),
		DevopsComponentName,
	)
	if !found {
		t.Fatalf("the named container was not found")
	}
	if strings.Join(names, ",") != "HOME,ERUN_TENANT" {
		t.Fatalf("names = %v, want blank entries dropped", names)
	}
}
