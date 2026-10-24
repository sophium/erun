package deploy

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
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

func TestInPodRuntimeOnlySelectionLeavesARealSelectionUnchanged(t *testing.T) {
	resolvedTarget := config.OpenResult{Tenant: "frs", Environment: "prod", EnvConfig: config.EnvConfig{Type: config.EnvironmentTypeRuntime}}
	inPod := inPodEnvLookup("frs", "prod")
	components := []string{"frs-devops", "frs-backend-api", "frs-docs"}

	for _, tc := range []struct {
		name       string
		flag       []string
		saved      []string
		plan       config.ProjectK8sConfig
		wantSource string
	}{
		{"repo k8s.deployments plan", nil, nil, config.ProjectK8sConfig{Deployments: []config.ProjectK8sDeploymentStep{{Components: components}}}, deploySelectionSourcePlan},
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
	resolvedTarget := config.OpenResult{Tenant: "frs", Environment: "prod", EnvConfig: config.EnvConfig{Type: config.EnvironmentTypeRuntime}}
	selected, source := resolveSelectedDeployComponents(nil, nil, config.ProjectK8sConfig{})

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

func TestInPodRuntimeOnlySelectionIgnoresAnIncompleteInjectedIdentity(t *testing.T) {
	resolvedTarget := config.OpenResult{Tenant: "frs", Environment: "prod", EnvConfig: config.EnvConfig{Type: config.EnvironmentTypeRuntime}}
	selected, source := resolveSelectedDeployComponents(nil, nil, config.ProjectK8sConfig{})
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
