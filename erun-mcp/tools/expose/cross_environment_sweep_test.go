package expose

import (
	"context"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// Explicit ServicesZone/PlatformNamespace avoid needing a project on disk.
func TestExposeAndUnexposeAcceptTheirOwnScope(t *testing.T) {
	runtime := runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "acme", Environment: "dev"},
		Store: listToolStore{
			envConfigs: map[string]config.EnvConfig{
				"acme/dev": {Name: "dev", Type: config.EnvironmentTypeRuntime, KubernetesContext: "test-context"},
			},
		},
	}
	ctx := context.Background()

	for _, tc := range []struct {
		name        string
		tenant      string
		environment string
	}{
		{"omitted", "", ""},
		{"restated", "acme", "dev"},
	} {
		t.Run("expose/"+tc.name, func(t *testing.T) {
			_, _, err := ExposeTool(runtime)(ctx, nil, ExposeInput{
				Tenant: tc.tenant, Environment: tc.environment,
				Service: "api", IP: "127.0.0.1",
				ServicesZone: "zone.test", PlatformNamespace: "ns-test",
				Preview: true,
			})
			if err != nil {
				t.Errorf("expose refused its own scope: %v", err)
			}
		})
		t.Run("unexpose/"+tc.name, func(t *testing.T) {
			_, _, err := UnexposeTool(runtime)(ctx, nil, UnexposeInput{
				Tenant: tc.tenant, Environment: tc.environment,
				ServicesZone: "zone.test", PlatformNamespace: "ns-test",
				Preview: true,
			})
			if err != nil {
				t.Errorf("unexpose refused its own scope: %v", err)
			}
		})
	}
}
