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

// TestUsageDescriptionAgreesWithTheSizingTheToolReturns is the behaviour-
// anchored guard the description needs, and the one its predecessor was not.
//
// The predecessor pinned the absence of one specific stale sentence ("the
// verdict `erun list` reports"), which is a claim about prose and not about
// behaviour. When `ddecc03a` gave `erun usage` a `sizing` block, the sentence
// that replaced it -- "`erun usage` carries no sizing block at all" -- was
// false the day it was written and nothing in the suite could tell, because
// no test ever compared a claim in the description against what the tool
// returns. This one does: it drives the tool and reads the description, and
// requires the two to agree.
//
// What it decides, exactly: that the description embeds the claim generated
// from usageSizingSurfaces (so prose and enumeration cannot drift apart), and
// that the tool's own entry in that enumeration is earned -- driving the tool
// over retained history really must return a `sizing` block. It cannot judge
// whether the sibling surfaces named in the enumeration are described well,
// only that this tool's own claim is true and that the text was not typed by
// hand.
func TestUsageDescriptionAgreesWithTheSizingTheToolReturns(t *testing.T) {
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

	// The behaviour half of the biconditional. usageSizingSurfaces opens with
	// this tool, so if the tool ever stops returning the block the claim
	// generated from that list is a lie and this is what says so.
	carried := output.Sizing != nil
	if !carried {
		t.Fatalf("usageSizingSurfaces claims %q carries the standing sizing recommendation, but driving the tool over retained history returned none; remove it from that list and say so in the description",
			usageSizingSurfaces[0])
	}

	// The prose half: the description is built from that list, not typed
	// beside it, so the claim cannot be restated wrongly in one place and
	// correctly in the other.
	description := usageToolDescriptionForTest(t)
	claim := UsageSizingClaim()
	if !strings.Contains(description, claim) {
		t.Errorf("usage tool description does not carry the claim generated from usageSizingSurfaces, so its text and the surfaces it names can drift apart.\nwant substring:\n%s\ngot:\n%s", claim, description)
	}
	if !strings.Contains(description, "pod monitor") {
		t.Errorf("usage tool description carries `sizing` without saying the history behind it is retained by the environment's own pod monitor, which is what makes a read that cannot see that history unable to derive one:\n%s", description)
	}

	// The overview page restates the same relationship by hand, so it is
	// checked for the one class of claim this issue was about: denying the
	// block on a surface that reports usage. A page that names where the
	// verdict lives is fine; one that says a usage surface has none is not.
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

// TestUsageToolDisclosesExcludesBuildsOnABuildCapableEnvironment pins the MCP
// half of the excludes-builds caveat: a build-capable environment's reading
// cannot see the erun-dind sidecar an image build actually runs in, so the
// result must carry ExcludesBuilds=true instead of letting the reading imply
// the environment is idle. The Runtime fixture elsewhere in this file cannot
// tell a working field from a missing one -- UsesDindSidecar() is false for
// Runtime either way -- so this is the only case that exercises the disclosure
// at all, on either transport.
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
