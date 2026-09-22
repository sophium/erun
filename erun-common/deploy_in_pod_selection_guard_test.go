package eruncommon

import (
	"strings"
	"testing"
)

// inPodEnvLookup matches what the runtime chart injects into every container.
func inPodEnvLookup(tenant, environment string) func(string) string {
	return func(key string) string {
		switch key {
		case "ERUN_TENANT":
			return tenant
		case "ERUN_ENVIRONMENT":
			return environment
		}
		return ""
	}
}

func TestInPodRuntimeOnlySelectionRefusesRatherThanRollingOneChartSilently(t *testing.T) {
	resolvedTarget := OpenResult{Tenant: "frs", Environment: "prod", EnvConfig: EnvConfig{Type: EnvironmentTypeRuntime}}
	selected, source := resolveSelectedDeployComponents(nil, nil, ProjectK8sConfig{})
	if len(selected) != 0 || source != deploySelectionSourceDefault {
		t.Fatalf("precondition: got (%v, %q), want the runtime-only default", selected, source)
	}

	err := guardInPodBlindRuntimeOnlySelection(inPodEnvLookup("frs", "prod"), resolvedTarget, DeployTarget{VersionOverride: "1.0.104"}, selected, source)
	if err == nil {
		t.Fatal("in-pod runtime-only fallback was not refused; it would roll one chart and exit 0")
	}
	message := err.Error()
	for _, want := range []string{"frs/prod", "frs-devops", "deploy.components", "from the host CLI", "1.0.104"} {
		if !strings.Contains(message, want) {
			t.Errorf("refusal does not name %q:\n%s", want, message)
		}
	}
	// The runtime chart cannot be rolled from this pod by any route: naming it
	// with --components reaches the in-pod runtime-chart guard one layer on
	// (guardInPodRuntimeDeploy), so offering that here would name a command this
	// same deploy refuses.
	if strings.Contains(message, "--components") {
		t.Errorf("refusal offers an in-pod --components route that the runtime-chart guard refuses:\n%s", message)
	}
}

func TestInPodRuntimeOnlySelectionLeavesARealSelectionUnchanged(t *testing.T) {
	resolvedTarget := OpenResult{Tenant: "frs", Environment: "prod", EnvConfig: EnvConfig{Type: EnvironmentTypeRuntime}}
	inPod := inPodEnvLookup("frs", "prod")
	components := []string{"frs-devops", "frs-backend-api", "frs-docs"}

	for _, tc := range []struct {
		name       string
		flag       []string
		saved      []string
		plan       ProjectK8sConfig
		wantSource string
	}{
		{"explicit flag", components, nil, ProjectK8sConfig{}, deploySelectionSourceFlag},
		{"saved deploy.components", nil, components, ProjectK8sConfig{}, deploySelectionSourceSaved},
		{"repo k8s.deployments plan", nil, nil, ProjectK8sConfig{Deployments: []ProjectK8sDeploymentStep{{Components: components}}}, deploySelectionSourcePlan},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, source := resolveSelectedDeployComponents(tc.flag, tc.saved, tc.plan)
			if source != tc.wantSource {
				t.Fatalf("source = %q, want %q", source, tc.wantSource)
			}
			if got := strings.Join(selected, ","); got != strings.Join(components, ",") {
				t.Fatalf("selection = %v, want %v", selected, components)
			}
			if err := guardInPodBlindRuntimeOnlySelection(inPod, resolvedTarget, DeployTarget{}, selected, source); err != nil {
				t.Fatalf("guard refused a real selection: %v", err)
			}
		})
	}
}

func TestInPodRuntimeOnlySelectionLeavesTheOffPodFallbackAlone(t *testing.T) {
	resolvedTarget := OpenResult{Tenant: "frs", Environment: "prod", EnvConfig: EnvConfig{Type: EnvironmentTypeRuntime}}
	selected, source := resolveSelectedDeployComponents(nil, nil, ProjectK8sConfig{})

	for _, tc := range []struct {
		name string
		env  func(string) string
	}{
		{"no injected identity", func(string) string { return "" }},
		{"nil env lookup", nil},
		{"runtime pod of another environment", inPodEnvLookup("frs", "dev")},
		{"runtime pod of another tenant", inPodEnvLookup("erun", "prod")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := guardInPodBlindRuntimeOnlySelection(tc.env, resolvedTarget, DeployTarget{}, selected, source); err != nil {
				t.Fatalf("off-pod fallback was refused: %v", err)
			}
		})
	}
}

// The guard is reached through the shared resolver both the local-repo and
// sourceless deploy paths call, before the selection is traced. It refuses
// before touching ctx, so the wiring is exercised with the injected identity the
// runtime chart sets rather than a live pod.
func TestResolveGuardedDeploySelectionRefusesTheBlindRuntimeOnlyFallbackInPod(t *testing.T) {
	t.Setenv("ERUN_TENANT", "frs")
	t.Setenv("ERUN_ENVIRONMENT", "prod")

	selected, err := resolveGuardedDeploySelection(Context{}, DeployTarget{}, OpenResult{Tenant: "frs", Environment: "prod", EnvConfig: EnvConfig{Type: EnvironmentTypeRuntime}}, ProjectK8sConfig{})
	if err == nil {
		t.Fatal("resolveGuardedDeploySelection returned the runtime-only default in-pod without refusing")
	}
	if selected != nil {
		t.Fatalf("selected = %v, want nil alongside the refusal", selected)
	}
}

// The guard is scoped to runtime environments, so the empty-selection fallback
// gets this diagnosis only where the runtime chart is the whole environment.
// The other types reach the same fallback and are refused for it by the in-pod
// runtime-chart guard one layer on, not here — this narrowing decides which
// refusal speaks, not what is permitted.
func TestInPodRuntimeOnlySelectionIsScopedToRuntimeEnvironments(t *testing.T) {
	for _, tc := range []struct {
		envType EnvironmentType
		refused bool
	}{
		{EnvironmentTypeRuntime, true},
		{EnvironmentTypeLocalAgent, false},
		{EnvironmentTypeRemoteAgent, false},
	} {
		t.Run(string(tc.envType), func(t *testing.T) {
			resolvedTarget := OpenResult{
				Tenant:      "frs",
				Environment: "prod",
				EnvConfig:   EnvConfig{Type: tc.envType},
			}
			selected, source := resolveSelectedDeployComponents(nil, nil, ProjectK8sConfig{})
			err := guardInPodBlindRuntimeOnlySelection(inPodEnvLookup("frs", "prod"), resolvedTarget, DeployTarget{}, selected, source)
			if tc.refused && err == nil {
				t.Fatalf("%s: in-pod runtime-only fallback was not refused", tc.envType)
			}
			if !tc.refused && err != nil {
				t.Fatalf("%s: guard fired outside its scope: %v", tc.envType, err)
			}
		})
	}
}

func TestInPodRuntimeOnlySelectionIgnoresAnIncompleteInjectedIdentity(t *testing.T) {
	resolvedTarget := OpenResult{Tenant: "frs", Environment: "prod", EnvConfig: EnvConfig{Type: EnvironmentTypeRuntime}}
	selected, source := resolveSelectedDeployComponents(nil, nil, ProjectK8sConfig{})
	env := func(key string) string {
		if key == "ERUN_TENANT" {
			return "frs"
		}
		return ""
	}
	if err := guardInPodBlindRuntimeOnlySelection(env, resolvedTarget, DeployTarget{}, selected, source); err != nil {
		t.Fatalf("a half-set injected identity must not be treated as in-pod: %v", err)
	}
}
