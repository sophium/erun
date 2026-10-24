package podruntime

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// reportedRuntimePodEnvNames is a pod env with the chart's tuning block but no gateway block.
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

func reportedGateway() *config.OpenRouterConfig {
	return &config.OpenRouterConfig{
		BaseURL: "https://openrouter.ai/api",
		Models:  []config.OpenRouterModel{{ID: "some/model", Context: 200000}},
	}
}

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

func runtimeDeploymentJSON(container string, envNames []string) string {
	entries := make([]string, 0, len(envNames))
	for _, name := range envNames {
		entries = append(entries, fmt.Sprintf(`{"name":%q}`, name))
	}
	return fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":%q,"env":[%s]}]}}}}`,
		container, strings.Join(entries, ","))
}

func gatewayDiagnosisRequest(gateway *config.OpenRouterConfig) config.ShellLaunchParams {
	return config.ShellLaunchParams{
		Tenant:            "frs",
		Environment:       "build",
		Namespace:         "frs-build",
		KubernetesContext: "the-cluster",
		Gateway:           gateway,
	}
}

func TestRunDeployDiagnosisNamesTheMissingGatewayRouting(t *testing.T) {
	writeDoctorHelmStub(t, "NAME: frs-devops\nSTATUS: deployed\nREVISION: 42\n", "", 0)
	stub := writeRuntimeKubectlStub(t, runtimeDeploymentJSON(config.DevopsComponentName, reportedRuntimePodEnvNames()))

	diagnosis := deploy.RunDeployDiagnosis(testTraceContext(false), gatewayDiagnosisRequest(reportedGateway()))

	status := diagnosis.AgentCredentials
	if status.State != deploy.RuntimeAgentCredentialMissing {
		t.Fatalf("AgentCredentials.State = %q, want %q (readError=%q)",
			status.State, deploy.RuntimeAgentCredentialMissing, status.ReadError)
	}
	if status.GatewayBaseURL != "https://openrouter.ai/api" {
		t.Fatalf("AgentCredentials.GatewayBaseURL = %q, want the configured gateway", status.GatewayBaseURL)
	}
	if status.Container != config.DevopsComponentName {
		t.Fatalf("AgentCredentials.Container = %q, want %q", status.Container, config.DevopsComponentName)
	}
	if status.ReadError != "" {
		t.Fatalf("AgentCredentials.ReadError = %q, want empty: the template was read and genuinely carries nothing", status.ReadError)
	}
	if !stub.askedFor(t, "get deployment") {
		t.Fatalf("the diagnosis never read the deployed pod template; invocations: %v", stub.invocations(t))
	}
	// The release is genuinely deployed; missing agent routing does not make it unhealthy.
	if action, ok := deploy.RecommendedDeployRecovery(diagnosis); ok {
		t.Fatalf("RecommendedDeployRecovery = %q, want no action for a release that is deployed", action)
	}
}

func TestRunDeployDiagnosisConfirmsGatewayRoutingWhenTheTemplateCarriesIt(t *testing.T) {
	writeDoctorHelmStub(t, "NAME: frs-devops\nSTATUS: deployed\nREVISION: 42\n", "", 0)
	carrying := append([]string{deploy.GatewayBaseURLEnv, "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL"}, reportedRuntimePodEnvNames()...)
	writeRuntimeKubectlStub(t, runtimeDeploymentJSON(config.DevopsComponentName, carrying))

	diagnosis := deploy.RunDeployDiagnosis(testTraceContext(false), gatewayDiagnosisRequest(reportedGateway()))

	if got := diagnosis.AgentCredentials.State; got != deploy.RuntimeAgentCredentialConfigured {
		t.Fatalf("AgentCredentials.State = %q, want %q", got, deploy.RuntimeAgentCredentialConfigured)
	}
}

func TestRunDeployDiagnosisMakesNoAgentClaimWithoutAGateway(t *testing.T) {
	writeDoctorHelmStub(t, "NAME: frs-devops\nSTATUS: deployed\nREVISION: 42\n", "", 0)
	stub := writeRuntimeKubectlStub(t, runtimeDeploymentJSON(config.DevopsComponentName, reportedRuntimePodEnvNames()))

	diagnosis := deploy.RunDeployDiagnosis(testTraceContext(false), gatewayDiagnosisRequest(nil))

	if got := diagnosis.AgentCredentials.State; got != deploy.RuntimeAgentCredentialNotApplicable {
		t.Fatalf("AgentCredentials.State = %q, want %q", got, deploy.RuntimeAgentCredentialNotApplicable)
	}
	if stub.askedFor(t, "get deployment") {
		t.Fatalf("an install with no gateway paid for a pod-template read anyway; invocations: %v", stub.invocations(t))
	}
}

func TestRunDeployDiagnosisDoesNotCallAnUnreadTemplateMissing(t *testing.T) {
	writeDoctorHelmStub(t, "NAME: frs-devops\nSTATUS: deployed\nREVISION: 42\n", "", 0)
	writeKubectlStub(t, `Error from server (Forbidden): deployments.apps "frs-devops" is forbidden`, 1)

	diagnosis := deploy.RunDeployDiagnosis(testTraceContext(false), gatewayDiagnosisRequest(reportedGateway()))

	status := diagnosis.AgentCredentials
	if status.State != deploy.RuntimeAgentCredentialUnknown {
		t.Fatalf("AgentCredentials.State = %q, want %q", status.State, deploy.RuntimeAgentCredentialUnknown)
	}
	if status.ReadError == "" {
		t.Fatalf("AgentCredentials.ReadError is empty, want the reason the template could not be read")
	}
	if strings.Contains(status.ReadError, "ANTHROPIC") {
		t.Fatalf("AgentCredentials.ReadError = %q, want the read failure rather than a credential claim", status.ReadError)
	}
}

func TestResolveRuntimeAgentCredentialStatus(t *testing.T) {
	optedOut := false
	cases := []struct {
		name   string
		params deploy.RuntimeAgentCredentialParams
		want   deploy.RuntimeAgentCredentialState
	}{
		{
			name:   "no catalog configured",
			params: deploy.RuntimeAgentCredentialParams{Container: config.DevopsComponentName, ContainerEnvObserved: true},
			want:   deploy.RuntimeAgentCredentialNotApplicable,
		},
		{
			name: "the environment opted out of the catalog",
			params: deploy.RuntimeAgentCredentialParams{
				Claude:               config.EnvironmentClaudeConfig{UseGateway: &optedOut},
				Gateway:              reportedGateway(),
				Container:            config.DevopsComponentName,
				ContainerEnvObserved: true,
			},
			want: deploy.RuntimeAgentCredentialNotApplicable,
		},
		{
			name: "the template carries the routing variable",
			params: deploy.RuntimeAgentCredentialParams{
				Gateway:              reportedGateway(),
				Container:            config.DevopsComponentName,
				ContainerEnvObserved: true,
				ContainerEnvNames:    []string{"HOME", deploy.GatewayBaseURLEnv},
			},
			want: deploy.RuntimeAgentCredentialConfigured,
		},
		{
			name: "a padded routing variable still counts",
			params: deploy.RuntimeAgentCredentialParams{
				Gateway:              reportedGateway(),
				Container:            config.DevopsComponentName,
				ContainerEnvObserved: true,
				ContainerEnvNames:    []string{" " + deploy.GatewayBaseURLEnv + " "},
			},
			want: deploy.RuntimeAgentCredentialConfigured,
		},
		{
			name: "the template was read and carries nothing",
			params: deploy.RuntimeAgentCredentialParams{
				Gateway:              reportedGateway(),
				Container:            config.DevopsComponentName,
				ContainerEnvObserved: true,
				ContainerEnvNames:    reportedRuntimePodEnvNames(),
			},
			want: deploy.RuntimeAgentCredentialMissing,
		},
		{
			name: "the template was never read",
			params: deploy.RuntimeAgentCredentialParams{
				Gateway:   reportedGateway(),
				Container: config.DevopsComponentName,
			},
			want: deploy.RuntimeAgentCredentialUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deploy.ResolveRuntimeAgentCredentialStatus(tc.params)
			if got.State != tc.want {
				t.Fatalf("State = %q, want %q", got.State, tc.want)
			}
			if tc.want == deploy.RuntimeAgentCredentialMissing && got.GatewayBaseURL == "" {
				t.Fatalf("a missing-routing verdict must still name the gateway that is being dropped")
			}
		})
	}
}

func TestRuntimeDeploymentContainerEnvNames(t *testing.T) {
	if _, found := deploy.RuntimeDeploymentContainerEnvNames([]byte("not json"), config.DevopsComponentName); found {
		t.Fatalf("an unparsable document reported a container")
	}
	other := runtimeDeploymentJSON("some-other-container", reportedRuntimePodEnvNames())
	if _, found := deploy.RuntimeDeploymentContainerEnvNames([]byte(other), config.DevopsComponentName); found {
		t.Fatalf("a template with no %q container reported one", config.DevopsComponentName)
	}
	names, found := deploy.RuntimeDeploymentContainerEnvNames(
		[]byte(runtimeDeploymentJSON(config.DevopsComponentName, []string{"HOME", "", "  ", "ERUN_TENANT"})),
		config.DevopsComponentName,
	)
	if !found {
		t.Fatalf("the named container was not found")
	}
	if strings.Join(names, ",") != "HOME,ERUN_TENANT" {
		t.Fatalf("names = %v, want blank entries dropped", names)
	}
}
