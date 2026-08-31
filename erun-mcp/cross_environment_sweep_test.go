package erunmcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	eruncommon "github.com/sophium/erun/erun-common"
)

// TestCrossEnvironmentToolsRefuseAForeignEnvironment sweeps every remaining
// MCP tool that accepts tenant/environment but never checked them against
// this server's own bound environment: doctor, observe, usage,
// expose, unexpose, and terraform each resolved (or silently defaulted) a
// caller-supplied tenant/environment on their own, independent of the
// resolveLocalTarget/scopedTenantEnv guard every other tool in this module
// already applied. Preview:true keeps each call from reaching kubectl/helm --
// the refusal must happen before any of that, which this also proves, since a
// downstream error would be a different message than assertRefusedForeignTarget
// checks for.
func TestCrossEnvironmentToolsRefuseAForeignEnvironment(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store:   usageTestStore("tenant-a", "dev"),
	}
	ctx := context.Background()

	_, _, err := doctorTool(runtime)(ctx, nil, DoctorInput{Tenant: "tenant-b", Environment: "prod", Preview: true})
	assertRefusedForeignTarget(t, "doctor", err)

	_, _, err = observeTool(runtime)(ctx, nil, ObserveInput{Tenant: "tenant-b", Environment: "prod", Preview: true})
	assertRefusedForeignTarget(t, "observe", err)

	_, _, err = usageTool(runtime)(ctx, nil, UsageInput{Tenant: "tenant-b", Environment: "prod", Preview: true})
	assertRefusedForeignTarget(t, "usage", err)

	_, _, err = exposeTool(runtime)(ctx, nil, ExposeInput{
		Tenant: "tenant-b", Environment: "prod", Service: "api", IP: "127.0.0.1", Preview: true,
	})
	assertRefusedForeignTarget(t, "expose", err)

	_, _, err = unexposeTool(runtime)(ctx, nil, UnexposeInput{Tenant: "tenant-b", Environment: "prod", Preview: true})
	assertRefusedForeignTarget(t, "unexpose", err)

	_, _, err = terraformTool(runtime)(ctx, nil, TerraformInput{
		Tenant: "tenant-b", Environment: "prod", Operation: "plan", Preview: true,
	})
	assertRefusedForeignTarget(t, "terraform", err)
}

// TestDoctorObserveUsageAcceptTheirOwnScope is the flip side: restating or
// omitting tenant/environment must keep working exactly as before, since
// plenty of existing callers pass them redundantly.
func TestDoctorObserveUsageAcceptTheirOwnScope(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
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
			if _, _, err := doctorTool(runtime)(ctx, nil, DoctorInput{Tenant: tc.tenant, Environment: tc.environment, Preview: true}); err != nil {
				t.Errorf("doctor refused its own scope: %v", err)
			}
		})
		t.Run("observe/"+tc.name, func(t *testing.T) {
			if _, _, err := observeTool(runtime)(ctx, nil, ObserveInput{Tenant: tc.tenant, Environment: tc.environment, Preview: true}); err != nil {
				t.Errorf("observe refused its own scope: %v", err)
			}
		})
		t.Run("usage/"+tc.name, func(t *testing.T) {
			if _, _, err := usageTool(runtime)(ctx, nil, UsageInput{Tenant: tc.tenant, Environment: tc.environment, Preview: true}); err != nil {
				t.Errorf("usage refused its own scope: %v", err)
			}
		})
	}
}

// TestExposeAndUnexposeAcceptTheirOwnScope mirrors the doctor/observe/usage
// coverage above for the two tools that resolve their target via
// resolveLocalTarget directly rather than through an OpenResult. ServicesZone
// and PlatformNamespace are supplied explicitly (mirroring the CLI's
// --services-zone/--platform-namespace) so the call needs no project on disk.
func TestExposeAndUnexposeAcceptTheirOwnScope(t *testing.T) {
	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "acme", Environment: "dev"},
		Store: listToolStore{
			envConfigs: map[string]eruncommon.EnvConfig{
				"acme/dev": {Name: "dev", Type: eruncommon.EnvironmentTypeRuntime, KubernetesContext: "test-context"},
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
			_, _, err := exposeTool(runtime)(ctx, nil, ExposeInput{
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
			_, _, err := unexposeTool(runtime)(ctx, nil, UnexposeInput{
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

// terraformTestStore adds ResolveEffectiveKubernetesContext to listToolStore,
// the one extra method eruncommon.TerraformStore needs beyond OpenStore; the
// terraform tool falls back to a real eruncommon.ConfigStore (reading this
// machine's actual ~/.config/erun) when the wired store does not implement
// TerraformStore, which is not what a hermetic test wants.
type terraformTestStore struct {
	listToolStore
}

func (terraformTestStore) ResolveEffectiveKubernetesContext(_, configured string) string {
	return configured
}

// TestTerraformAcceptsItsOwnScope exercises terraform's full resolveLocalTarget
// -> RunTerraform round trip against a real (temp-dir) terraform root, since
// ValidateTenantName rejects the hyphenated placeholder tenants used above --
// terraform is the one swept tool that validates the tenant name itself.
func TestTerraformAcceptsItsOwnScope(t *testing.T) {
	projectRoot := t.TempDir()
	terraformDir := filepath.Join(projectRoot, "terraform-acme", "dev")
	if err := os.MkdirAll(terraformDir, 0o755); err != nil {
		t.Fatalf("mkdir terraform root: %v", err)
	}

	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "acme", Environment: "dev", RepoPath: projectRoot},
		Store: terraformTestStore{listToolStore{
			envConfigs: map[string]eruncommon.EnvConfig{
				"acme/dev": {Name: "dev", Type: eruncommon.EnvironmentTypeLocalAgent},
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
			_, output, err := terraformTool(runtime)(ctx, nil, TerraformInput{
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
