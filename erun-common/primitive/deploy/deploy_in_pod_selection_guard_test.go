package deploy

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
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
			if err := guardInPodBlindRuntimeOnlySelection(inPod, resolvedTarget, model.DeployTarget{}, selected, source); err != nil {
				t.Fatalf("guard refused a real selection: %v", err)
			}
		})
	}
}
