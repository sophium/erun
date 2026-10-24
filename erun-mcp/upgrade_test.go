package erunmcp

import (
	"context"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-mcp/tools/shared"
	"github.com/sophium/erun/erun-mcp/tools/upgrade"
)

func TestUpgradeToolPreviewResolvesPlan(t *testing.T) {
	t.Setenv(lifecycle.UpgradeVersionsOverrideEnv, "stable=3.0.0")
	projectRoot := t.TempDir()
	handler := upgrade.UpgradeTool(normalizeRuntimeConfig(shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "tenant-a", Environment: "dev", RepoPath: projectRoot},
		Store: listToolStore{
			toolConfig:    config.ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]config.TenantConfig{"tenant-a": {Name: "tenant-a", DefaultEnvironment: "dev"}},
			envsByTenant: map[string][]config.EnvConfig{
				"tenant-a": {{Name: "dev", Type: config.EnvironmentTypeRuntime, RuntimeVersion: "3.0.0", AutoUpgrade: true}},
			},
		},
	}))

	_, output, err := handler(context.Background(), nil, upgrade.UpgradeInput{Preview: true})
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
