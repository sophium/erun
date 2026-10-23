package erunmcp

import (
	"context"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/config"
)

// TestUpgradeToolPreviewResolvesPlan verifies the upgrade preview path resolves
// the plan without deploying, so callers can inspect the work with no side effects.
func TestUpgradeToolPreviewResolvesPlan(t *testing.T) {
	t.Setenv(eruncommon.UpgradeVersionsOverrideEnv, "stable=3.0.0")
	projectRoot := t.TempDir()
	handler := upgradeTool(normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev", RepoPath: projectRoot},
		Store: listToolStore{
			toolConfig:    config.ERunConfig{DefaultTenant: "tenant-a"},
			tenantConfigs: map[string]config.TenantConfig{"tenant-a": {Name: "tenant-a", DefaultEnvironment: "dev"}},
			envsByTenant: map[string][]config.EnvConfig{
				"tenant-a": {{Name: "dev", Type: config.EnvironmentTypeRuntime, RuntimeVersion: "3.0.0", AutoUpgrade: true}},
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
