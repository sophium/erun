package erunmcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	eruncommon "github.com/sophium/erun/erun-common"
)

// TestUsageToolCarriesTheStandingSizingRecommendation covers the fix for the
// "verdict but no evidence" gap: a caller reading `usage` to check on an
// environment's health should not need a separate `resize --preview` call
// just to see why erun currently recommends what it does. Preview mode short-
// circuits the live cgroup read (RunRuntimeUsage returns early under DryRun),
// so this only exercises the new sizing attachment, not the exec path.
func TestUsageToolCarriesTheStandingSizingRecommendation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
	seedUsageHistoryForTest(t, "tenant-a", "dev")

	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store:   usageTestStore("tenant-a", "dev"),
	}

	_, output, err := usageTool(runtime)(context.Background(), nil, UsageInput{Preview: true})
	if err != nil {
		t.Fatalf("usageTool returned err: %v", err)
	}
	if output.Sizing == nil {
		t.Fatal("expected the standing sizing recommendation to be attached")
	}
	if len(output.Sizing.Verdicts) != 2 {
		t.Fatalf("expected a verdict per resource, got %+v", output.Sizing.Verdicts)
	}
	if output.Sizing.Evidence.Samples != 240 {
		t.Fatalf("expected the evidence to carry the retained sample count, got %d", output.Sizing.Evidence.Samples)
	}
}

// TestUsageToolOmitsSizingWithNoHistory mirrors erun list's advisory-safe
// contract: an environment nobody has watched yet gets no sizing field, not
// an error or a zero-value recommendation a caller might mistake for a hold.
func TestUsageToolOmitsSizingWithNoHistory(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store:   usageTestStore("tenant-a", "dev"),
	}

	_, output, err := usageTool(runtime)(context.Background(), nil, UsageInput{Preview: true})
	if err != nil {
		t.Fatalf("usageTool returned err: %v", err)
	}
	if output.Sizing != nil {
		t.Fatalf("expected no sizing recommendation with no retained history, got %+v", output.Sizing)
	}
}

// usageTestStore builds a store that resolves tenant/environment through the
// same OpenResult path `usage`/`resize` use, which needs both LoadEnvConfig
// (envConfigs) and the port-range allocator's ListEnvConfigs (envsByTenant)
// to agree on the one environment.
func usageTestStore(tenant, environment string) listToolStore {
	env := eruncommon.EnvConfig{
		Name:                environment,
		Type:                eruncommon.EnvironmentTypeRuntime,
		KubernetesContext:   "test-context",
		LocalPortRangeStart: 17000,
	}
	return listToolStore{
		toolConfig:    eruncommon.ERunConfig{DefaultTenant: tenant},
		tenantConfigs: map[string]eruncommon.TenantConfig{tenant: {Name: tenant, DefaultEnvironment: environment}},
		envConfigs:    map[string]eruncommon.EnvConfig{tenant + "/" + environment: env},
		envsByTenant:  map[string][]eruncommon.EnvConfig{tenant: {env}},
	}
}

// seedUsageHistoryForTest writes the on-disk retained-usage shape directly,
// mirroring erun-integration's seedUsageHistory: a comfortable peak (52% of
// the limit) over a long, quiet window, which resolves to a low-confidence
// lower verdict on both resources.
func seedUsageHistoryForTest(t *testing.T, tenant, environment string) {
	t.Helper()
	dir, err := eruncommon.EnvironmentActivityDir(tenant, environment)
	if err != nil {
		t.Fatalf("EnvironmentActivityDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	sample := `{"cpu":{"quotaCores":12,"usageUsec":3765560000,"periods":376556,"throttledPeriods":0},"memory":{"limitBytes":24696061952,"currentBytes":6371188736,"peakBytes":12742377472,"oomKills":0}}`
	samples := make([]string, 0, 240)
	for i := 0; i < 240; i++ {
		samples = append(samples, sample)
	}
	body := `{"firstObservedAt":"2026-08-01T00:00:00Z","lastObservedAt":"2026-08-02T07:12:00Z",` +
		`"observedPeakMemoryBytes":12742377472,"observedOomKills":0,"observedPeakCpuMilli":4567,` +
		`"observedPeriods":376556,"observedThrottledPeriods":0,"samples":[` + strings.Join(samples, ",") + `]}`
	if err := os.WriteFile(filepath.Join(dir, "usage-history.json"), []byte(body+"\n"), 0o644); err != nil {
		t.Fatalf("write usage-history.json: %v", err)
	}
}
