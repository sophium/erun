package terraform

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// terraformTestStore implements TerraformStore so the tool does not fall back to the
// machine's real ~/.config/erun.
type terraformTestStore struct {
	listToolStore
}

func (terraformTestStore) ResolveEffectiveKubernetesContext(_, configured string) string {
	return configured
}

// terraform validates the tenant name itself, so this uses a real temp-dir root.
func TestTerraformAcceptsItsOwnScope(t *testing.T) {
	projectRoot := t.TempDir()
	terraformDir := filepath.Join(projectRoot, "terraform-acme", "dev")
	if err := os.MkdirAll(terraformDir, 0o755); err != nil {
		t.Fatalf("mkdir terraform root: %v", err)
	}

	runtime := runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "acme", Environment: "dev", RepoPath: projectRoot},
		Store: terraformTestStore{listToolStore{
			envConfigs: map[string]config.EnvConfig{
				"acme/dev": {Name: "dev", Type: config.EnvironmentTypeLocalAgent},
			},
		}},
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
		t.Run(tc.name, func(t *testing.T) {
			_, output, err := TerraformTool(runtime)(ctx, nil, TerraformInput{
				Tenant: tc.tenant, Environment: tc.environment, Operation: "plan", Preview: true,
			})
			if err != nil {
				t.Fatalf("terraform refused its own scope: %v", err)
			}
			if output.Executed {
				t.Errorf("expected preview to trace without executing, got %+v", output.CommandOutput)
			}
		})
	}
}
