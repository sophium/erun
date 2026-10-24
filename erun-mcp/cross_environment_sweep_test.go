package erunmcp

import (
	"context"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-mcp/tools/doctor"
	"github.com/sophium/erun/erun-mcp/tools/expose"
	"github.com/sophium/erun/erun-mcp/tools/observe"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
	"github.com/sophium/erun/erun-mcp/tools/terraform"
	"github.com/sophium/erun/erun-mcp/tools/usage"
)

// Preview keeps calls away from kubectl/helm, so the refusal must come first.
func TestCrossEnvironmentToolsRefuseAForeignEnvironment(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	runtime := runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
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

	runtime := runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
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
