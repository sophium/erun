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
	resolvedTarget := OpenResult{Tenant: "frs", Environment: "prod"}
	selected, source := resolveSelectedDeployComponents(nil, nil, ProjectK8sConfig{})
	if len(selected) != 0 || source != deploySelectionSourceDefault {
		t.Fatalf("precondition: got (%v, %q), want the runtime-only default", selected, source)
	}

	err := guardInPodBlindRuntimeOnlySelection(inPodEnvLookup("frs", "prod"), resolvedTarget, DeployTarget{VersionOverride: "1.0.104"}, selected, source)
	if err == nil {
		t.Fatal("in-pod runtime-only fallback was not refused; it would roll one chart and exit 0")
	}
	message := err.Error()
	for _, want := range []string{"frs/prod", "frs-devops", "deploy.components", "--components", "from the host CLI", "1.0.104"} {
		if !strings.Contains(message, want) {
			t.Errorf("refusal does not name %q:\n%s", want, message)
		}
	}
}

func TestInPodRuntimeOnlySelectionLeavesARealSelectionUnchanged(t *testing.T) {
	resolvedTarget := OpenResult{Tenant: "frs", Environment: "prod"}
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
	resolvedTarget := OpenResult{Tenant: "frs", Environment: "prod"}
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

	selected, err := resolveGuardedDeploySelection(Context{}, DeployTarget{}, OpenResult{Tenant: "frs", Environment: "prod"}, ProjectK8sConfig{})
	if err == nil {
		t.Fatal("resolveGuardedDeploySelection returned the runtime-only default in-pod without refusing")
	}
	if selected != nil {
		t.Fatalf("selected = %v, want nil alongside the refusal", selected)
	}
}
