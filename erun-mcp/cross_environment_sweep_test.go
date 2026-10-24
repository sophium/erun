package erunmcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-mcp/tools/doctor"
	"github.com/sophium/erun/erun-mcp/tools/expose"
	"github.com/sophium/erun/erun-mcp/tools/observe"
	"github.com/sophium/erun/erun-mcp/tools/shared"
	"github.com/sophium/erun/erun-mcp/tools/terraform"
	"github.com/sophium/erun/erun-mcp/tools/usage"
)

// Preview keeps calls away from kubectl/helm, so the refusal must come first.
func TestCrossEnvironmentToolsRefuseAForeignEnvironment(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	runtime := shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store:   usageTestStore("tenant-a", "dev"),
	}
	ctx := context.Background()

	_, _, err := doctor.DoctorTool(runtime)(ctx, nil, doctor.DoctorInput{Tenant: "tenant-b", Environment: "prod", Preview: true})
	assertRefusedForeignTarget(t, "doctor", err)

	_, _, err = observe.ObserveTool(runtime)(ctx, nil, observe.ObserveInput{Tenant: "tenant-b", Environment: "prod", Preview: true})
	assertRefusedForeignTarget(t, "observe", err)

	_, _, err = usage.UsageTool(runtime)(ctx, nil, usage.UsageInput{Tenant: "tenant-b", Environment: "prod", Preview: true})
	assertRefusedForeignTarget(t, "usage", err)

	_, _, err = expose.ExposeTool(runtime)(ctx, nil, expose.ExposeInput{
		Tenant: "tenant-b", Environment: "prod", Service: "api", IP: "127.0.0.1", Preview: true,
	})
	assertRefusedForeignTarget(t, "expose", err)

	_, _, err = expose.UnexposeTool(runtime)(ctx, nil, expose.UnexposeInput{Tenant: "tenant-b", Environment: "prod", Preview: true})
	assertRefusedForeignTarget(t, "unexpose", err)

	_, _, err = terraform.TerraformTool(runtime)(ctx, nil, terraform.TerraformInput{
		Tenant: "tenant-b", Environment: "prod", Operation: "plan", Preview: true,
	})
	assertRefusedForeignTarget(t, "terraform", err)
}

// Many callers restate tenant/environment redundantly.
func TestDoctorObserveUsageAcceptTheirOwnScope(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	runtime := shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store:   usageTestStore("tenant-a", "dev"),
	}
	ctx := context.Background()

	for _, tc := range []struct {
		name        string
		tenant      string
		environment string
	}{
		{"omitted", "", ""},
		{"restated", "tenant-a", "dev"},
	} {
		t.Run("doctor/"+tc.name, func(t *testing.T) {
			if _, _, err := doctor.DoctorTool(runtime)(ctx, nil, doctor.DoctorInput{Tenant: tc.tenant, Environment: tc.environment, Preview: true}); err != nil {
				t.Errorf("doctor refused its own scope: %v", err)
			}
		})
		t.Run("observe/"+tc.name, func(t *testing.T) {
			if _, _, err := observe.ObserveTool(runtime)(ctx, nil, observe.ObserveInput{Tenant: tc.tenant, Environment: tc.environment, Preview: true}); err != nil {
				t.Errorf("observe refused its own scope: %v", err)
			}
		})
		t.Run("usage/"+tc.name, func(t *testing.T) {
			if _, _, err := usage.UsageTool(runtime)(ctx, nil, usage.UsageInput{Tenant: tc.tenant, Environment: tc.environment, Preview: true}); err != nil {
				t.Errorf("usage refused its own scope: %v", err)
			}
		})
	}
}

// Explicit ServicesZone/PlatformNamespace avoid needing a project on disk.
func TestExposeAndUnexposeAcceptTheirOwnScope(t *testing.T) {
	runtime := shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "acme", Environment: "dev"},
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
			_, _, err := expose.ExposeTool(runtime)(ctx, nil, expose.ExposeInput{
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
			_, _, err := expose.UnexposeTool(runtime)(ctx, nil, expose.UnexposeInput{
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

	runtime := shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "acme", Environment: "dev", RepoPath: projectRoot},
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
			_, output, err := terraform.TerraformTool(runtime)(ctx, nil, terraform.TerraformInput{
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
