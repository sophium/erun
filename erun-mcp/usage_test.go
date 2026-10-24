package erunmcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/mcptools"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
	"github.com/sophium/erun/erun-mcp/tools/usage"
)

// Preview skips the live cgroup read, so this exercises only the sizing attachment.
func TestUsageToolCarriesTheStandingSizingRecommendation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
	seedUsageHistoryForTest(t, "tenant-a", "dev")

	runtime := runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store:   usageTestStore("tenant-a", "dev"),
	}

	_, output, err := usage.UsageTool(runtime)(context.Background(), nil, usage.UsageInput{Preview: true})
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

// An unwatched environment gets no sizing field rather than a zero-value recommendation.
func TestUsageToolOmitsSizingWithNoHistory(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	runtime := runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store:   usageTestStore("tenant-a", "dev"),
	}

	_, output, err := usage.UsageTool(runtime)(context.Background(), nil, usage.UsageInput{Preview: true})
	if err != nil {
		t.Fatalf("usageTool returned err: %v", err)
	}
	if output.Sizing != nil {
		t.Fatalf("expected no sizing recommendation with no retained history, got %+v", output.Sizing)
	}
}

func usageToolDescriptionForTest(t *testing.T) string {
	t.Helper()
	session := connectWithCapabilities(t, string(mcptools.MCPCapabilityRead))
	for _, tool := range listTools(t, session) {
		if tool.Name == "usage" {
			return tool.Description
		}
	}
	t.Fatal("usage tool is not registered")
	return ""
}

// The description's sizing claim must be generated from UsageSizingSurfaces and
// match what the tool actually returns.
func TestUsageDescriptionAgreesWithTheSizingTheToolReturns(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
	seedUsageHistoryForTest(t, "tenant-a", "dev")

	runtime := runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
		Store:   usageTestStore("tenant-a", "dev"),
	}
	_, output, err := usage.UsageTool(runtime)(context.Background(), nil, usage.UsageInput{Preview: true})
	if err != nil {
		t.Fatalf("usageTool returned err: %v", err)
	}

	carried := output.Sizing != nil
	if !carried {
		t.Fatalf("usageSizingSurfaces claims %q carries the standing sizing recommendation, but driving the tool over retained history returned none; remove it from that list and say so in the description",
			usage.UsageSizingSurfaces[0])
	}

	description := usageToolDescriptionForTest(t)
	claim := usage.UsageSizingClaim()
	if !strings.Contains(description, claim) {
		t.Errorf("usage tool description does not carry the claim generated from usageSizingSurfaces, so its text and the surfaces it names can drift apart.\nwant substring:\n%s\ngot:\n%s", claim, description)
	}
	if !strings.Contains(description, "pod monitor") {
		t.Errorf("usage tool description carries `sizing` without saying the history behind it is retained by the environment's own pod monitor, which is what makes a read that cannot see that history unable to derive one:\n%s", description)
	}

	// The overview page is hand-written, so check it never denies the block on a usage surface.
	docPath := filepath.Join(repoRootForOverviewDocTest(t), "erun-docs", "docs", "mcp", "overview.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	page := string(data)
	for _, denial := range []string{
		"carries no sizing block at all",
		"carries no sizing block",
		"does not carry a `sizing` block",
	} {
		if strings.Contains(page, denial) {
			t.Errorf("%s denies the sizing block on a usage surface (%q) while `erun usage` prints it and this tool returns it; the page has to state where the verdict lives, not that it is absent:\n%s", docPath, denial, page)
		}
	}
	if !strings.Contains(page, "pod monitor") {
		t.Errorf("%s describes the `sizing` field without saying the history behind it is retained by the environment's own pod monitor", docPath)
	}
}

// A build-capable environment cannot see the erun-dind sidecar, so ExcludesBuilds must be true.
func TestUsageToolDisclosesExcludesBuildsOnABuildCapableEnvironment(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	cases := []struct {
		name    string
		envType config.EnvironmentType
		want    bool
	}{
		{"remote-agent carries the dind sidecar", config.EnvironmentTypeRemoteAgent, true},
		{"local-agent carries the dind sidecar", config.EnvironmentTypeLocalAgent, true},
		{"runtime builds nowhere in this container", config.EnvironmentTypeRuntime, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := runtimecfg.RuntimeConfig{
				Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
				Store:   usageTestStoreOfType("tenant-a", "dev", tc.envType, t.TempDir()),
			}
			_, output, err := usage.UsageTool(runtime)(context.Background(), nil, usage.UsageInput{Preview: true})
			if err != nil {
				t.Fatalf("usageTool returned err: %v", err)
			}
			if output.ExcludesBuilds != tc.want {
				t.Fatalf("ExcludesBuilds = %v, want %v (env type %q)", output.ExcludesBuilds, tc.want, tc.envType)
			}
		})
	}
}

// usageTestStore needs envConfigs and envsByTenant to agree for OpenResult resolution.
func usageTestStore(tenant, environment string) listToolStore {
	return usageTestStoreOfType(tenant, environment, config.EnvironmentTypeRuntime, "/home/erun/work")
}

func usageTestStoreOfType(tenant, environment string, envType config.EnvironmentType, repoPath string) listToolStore {
	env := config.EnvConfig{
		Name:                environment,
		Type:                envType,
		KubernetesContext:   "test-context",
		LocalRepoPath:       repoPath,
		LocalPortRangeStart: 17000,
	}
	return listToolStore{
		toolConfig:    config.ERunConfig{DefaultTenant: tenant},
		tenantConfigs: map[string]model.TenantConfig{tenant: {Name: tenant, DefaultEnvironment: environment}},
		envConfigs:    map[string]config.EnvConfig{tenant + "/" + environment: env},
		envsByTenant:  map[string][]config.EnvConfig{tenant: {env}},
	}
}

// seedUsageHistoryForTest seeds a quiet 52%-peak history that resolves to a
// low-confidence lower verdict.
func seedUsageHistoryForTest(t *testing.T, tenant, environment string) {
	t.Helper()
	dir, err := activity.EnvironmentActivityDir(tenant, environment)
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
