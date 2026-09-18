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

// usageToolDescriptionForTest returns the `usage` tool's wire description, so
// the cross-references it makes can be checked against the surfaces that
// actually carry what it names.
func usageToolDescriptionForTest(t *testing.T) string {
	t.Helper()
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityRead))
	for _, tool := range listTools(t, session) {
		if tool.Name == "usage" {
			return tool.Description
		}
	}
	t.Fatal("usage tool is not registered")
	return ""
}

// TestUsageDescriptionNamesASurfaceThatCarriesTheVerdict covers the dead end
// this fixes: the description cross-referenced `erun list` as reporting the
// same raise/lower/hold verdict the `sizing` block carries, so a caller who
// took it there instead of making a separate resize call found nothing. `list`
// does read the same retained history, but the history lives in the
// environment's own pod monitor, so a host that has never monitored the
// environment has none and prints no verdict at all. The description has to
// send a caller to a surface that actually carries it, and own that the
// host-side ones are not it -- in the tool description and in the overview
// page's sibling prose alike, since a caller reads whichever they reached.
func TestUsageDescriptionNamesASurfaceThatCarriesTheVerdict(t *testing.T) {
	const falseClaim = "the same raise/lower/hold verdict and evidence window `erun list` reports"
	description := usageToolDescriptionForTest(t)
	if strings.Contains(description, falseClaim) {
		t.Errorf("usage tool description still sends callers to `erun list` for the sizing verdict:\n%s", description)
	}
	if !strings.Contains(description, "pod monitor") {
		t.Errorf("usage tool description carries `sizing` without saying the history behind it is retained by the environment's own pod monitor, which is what makes a host-side read unable to derive one:\n%s", description)
	}

	docPath := filepath.Join(repoRootForOverviewDocTest(t), "erun-docs", "docs", "mcp", "overview.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	page := string(data)
	if strings.Contains(page, "the same verdicts and evidence window `erun list` reports under `runtime-pod:`") {
		t.Errorf("%s still sends callers to `erun list` for the sizing verdict", docPath)
	}
	if !strings.Contains(page, "pod monitor") {
		t.Errorf("%s describes the `sizing` field without saying the history behind it is retained by the environment's own pod monitor", docPath)
	}
}

// TestUsageToolDisclosesExcludesBuildsOnABuildCapableEnvironment pins the MCP
// half of the excludes-builds caveat at the boundary the defect lived on: a
// build-capable environment's reading cannot see the erun-dind sidecar an
// image build actually runs in, so the result must carry ExcludesBuilds=true
// instead of letting the reading imply the environment is idle. The Runtime
// fixture elsewhere in this file cannot tell a working field from a missing
// one -- UsesDindSidecar() is false for Runtime either way -- so this is the
// only case that exercises the disclosure at all.
//
// The unresolved-type case is the defect itself, not a hypothetical: inside a
// runtime pod the on-disk env config is a projection `doctor --sync-config`
// rewrites only when it runs, so an unsynced pod config carries no `type`.
// ResolvedType() then reads empty and UsesDindSidecar() reads the unrecognised
// type as "no sidecar", so the field that exists to disclose the gap went
// missing -- silently, on exactly the environment that has the gap.
func TestUsageToolDisclosesExcludesBuildsOnABuildCapableEnvironment(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	cases := []struct {
		name    string
		envType eruncommon.EnvironmentType
		want    bool
	}{
		{"remote-agent carries the dind sidecar", eruncommon.EnvironmentTypeRemoteAgent, true},
		{"local-agent carries the dind sidecar", eruncommon.EnvironmentTypeLocalAgent, true},
		{"runtime builds nowhere in this container", eruncommon.EnvironmentTypeRuntime, false},
		// "" is not a fourth type: it is the pod-local projection state, where
		// the type is only recoverable from the injected identity (set below).
		{"unresolved type resolves from the pod's injected identity", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A pod injects its own identity; the on-disk config may or may not
			// have been synced to match it yet.
			t.Setenv("ERUN_TENANT", "tenant-a")
			t.Setenv("ERUN_ENVIRONMENT", "dev")
			t.Setenv("ERUN_ENV_TYPE", string(eruncommon.EnvironmentTypeRemoteAgent))

			runtime := RuntimeConfig{
				Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"},
				Store:   usageTestStoreOfType("tenant-a", "dev", tc.envType, t.TempDir()),
			}
			_, output, err := usageTool(runtime)(context.Background(), nil, UsageInput{Preview: true})
			if err != nil {
				t.Fatalf("usageTool returned err: %v", err)
			}
			if output.ExcludesBuilds != tc.want {
				t.Fatalf("ExcludesBuilds = %v, want %v (env type %q)", output.ExcludesBuilds, tc.want, tc.envType)
			}
		})
	}
}

// usageTestStore builds a store that resolves tenant/environment through the
// same OpenResult path `usage`/`resize` use, which needs both LoadEnvConfig
// (envConfigs) and the port-range allocator's ListEnvConfigs (envsByTenant)
// to agree on the one environment.
func usageTestStore(tenant, environment string) listToolStore {
	return usageTestStoreOfType(tenant, environment, eruncommon.EnvironmentTypeRuntime, "/home/erun/work")
}

func usageTestStoreOfType(tenant, environment string, envType eruncommon.EnvironmentType, repoPath string) listToolStore {
	env := eruncommon.EnvConfig{
		Name:                environment,
		Type:                envType,
		KubernetesContext:   "test-context",
		LocalRepoPath:       repoPath,
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
