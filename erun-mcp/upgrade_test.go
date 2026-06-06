package erunmcp

import (
	"context"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestUpgradeToolPreviewResolvesPlan exercises the MCP upgrade tool's
// preview/plan path: with the runtime's env opted in and already at its
// channel target (supplied via the version-override test seam so no registry
// call happens), preview resolves the plan and reports the env up to date,
// without attempting a deploy.
func TestUpgradeToolPreviewResolvesPlan(t *testing.T) {
	t.Setenv(eruncommon.UpgradeVersionsOverrideEnv, "stable=3.0.0")
	projectRoot := t.TempDir()
	handler := upgradeTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev", RepoPath: projectRoot},
		Store: listToolStore{
			toolConfig:    eruncommon.ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]eruncommon.TenantConfig{"tenant-a": {Name: "tenant-a", ProjectRoot: projectRoot, DefaultEnvironment: "dev"}},
			envsByTenant: map[string][]eruncommon.EnvConfig{
				"tenant-a": {{Name: "dev", Type: eruncommon.EnvironmentTypeRuntime, RuntimeVersion: "3.0.0", AutoUpgrade: true}},
			},
		},
	}))

	_, output, err := handler(context.Background(), nil, UpgradeInput{Preview: true})
	if err != nil {
		t.Fatalf("upgradeTool preview failed: %v", err)
	}
	trace := strings.Join(output.Trace, "\n")
	if !strings.Contains(trace, "tenant-a/dev opted in, channel=stable") {
		t.Fatalf("expected opted-in plan trace, got:\n%s", trace)
	}
	if !strings.Contains(trace, "up to date") {
		t.Fatalf("expected up-to-date decision, got:\n%s", trace)
	}
}
