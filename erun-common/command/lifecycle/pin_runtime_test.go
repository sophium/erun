package lifecycle

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

// The last confirmed running image wins over a stale persisted stock runtimeimage.
func TestResolvePinPlanLeavesAnEnvsRuntimeVersionAloneWhenItsPodRanItsOwnImage(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	env := config.EnvConfig{
		Name:                "build",
		RuntimeVersion:      "1.0.134",
		RuntimeImage:        "ghcr.io/sophium/erun-devops:1.0.284",
		RuntimeRunningImage: "ghcr.io/sophium/frs-devops:1.0.134",
	}

	plan, err := ResolvePinPlan(root, "frs", "build", env, "1.0.287")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, site := range plan.Sites {
		if site.Kind == PinSiteRuntimeVersion {
			t.Fatalf("runtimeversion must follow the image the pod actually ran, not the stale pin: %+v", site)
		}
	}
	note := skipNoteNaming(t, plan, "runtimeversion", "ghcr.io/sophium/frs-devops:1.0.134")
	if !strings.Contains(note, "runtimerunningimage") {
		t.Fatalf("the skipped note must name runtimerunningimage as what it read, got %q", note)
	}
}

// Stock-image environments, stated or defaulted for erun's own tenant, are still re-pinned.
func TestResolvePinPlanStillPinsAStockEnvsRuntimeVersion(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tenant      string
		environment string
		env         config.EnvConfig
	}{
		{
			name:        "erun's own tenant, nothing stated",
			tenant:      "erun",
			environment: "dev",
			env:         config.EnvConfig{Name: "dev", RuntimeVersion: "1.0.115"},
		},
		{
			name:        "stock image and stock chart stated",
			tenant:      "acme",
			environment: "dev",
			env: config.EnvConfig{
				Name:                "dev",
				RuntimeVersion:      "1.0.115",
				RuntimeImage:        "ghcr.io/sophium/erun-devops:1.0.115",
				RuntimeRunningImage: "ghcr.io/sophium/erun-devops:1.0.115",
				RuntimeChart:        "oci://ghcr.io/sophium/charts/erun-devops:1.0.115",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := seedPinnedTenantRepo(t)
			plan, err := ResolvePinPlan(root, tc.tenant, tc.environment, tc.env, "1.0.175")
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if len(plan.Changes()) == 0 {
				t.Fatal("expected the repo's drifted erun references to move")
			}
			for _, site := range plan.Sites {
				if site.Kind == PinSiteRuntimeVersion {
					if site.Target != "1.0.175" {
						t.Fatalf("runtimeversion site = %+v", site)
					}
					return
				}
			}
			t.Fatalf("a stock env's runtimeversion must still be a pin site, skipped notes: %+v", plan.Skipped)
		})
	}
}

// skipNoteNaming returns the one skipped note containing every fragment.
func skipNoteNaming(t *testing.T, plan PinPlan, fragments ...string) string {
	t.Helper()
	matched := ""
	for _, note := range plan.Skipped {
		hit := true
		for _, fragment := range fragments {
			if !strings.Contains(note, fragment) {
				hit = false
			}
		}
		if !hit {
			continue
		}
		if matched != "" {
			t.Fatalf("more than one skipped note names %v:\n%s\n%s", fragments, matched, note)
		}
		matched = note
	}
	if matched == "" {
		t.Fatalf("expected a skipped note naming %v, got %+v", fragments, plan.Skipped)
	}
	return matched
}

// A tagless stock runtimechart already follows the deploy version.
func TestResolvePinPlanFindsNoSiteForAFloatingStockRuntimeChart(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	env := config.EnvConfig{Name: "dev", RuntimeChart: "oci://ghcr.io/sophium/charts/erun-devops"}

	plan, err := ResolvePinPlan(root, "acme", "dev", env, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, site := range plan.Sites {
		if site.Kind == PinSiteRuntimeChart {
			t.Fatalf("a floating stock runtimechart must not be a pin site: %+v", site)
		}
	}
	for _, note := range plan.Skipped {
		if strings.Contains(note, "runtimechart") {
			t.Fatalf("a floating stock runtimechart needs no skip note either, got %+v", note)
		}
	}
}

// An unclassifiable runtimechart is skipped with a note, never blocking.
func TestResolvePinPlanLeavesAnUnreadableRuntimeChartAloneAndSaysSo(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	env := config.EnvConfig{Name: "dev", RuntimeChart: "oci://"}

	plan, err := ResolvePinPlan(root, "acme", "dev", env, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, site := range plan.Sites {
		if site.Kind == PinSiteRuntimeChart {
			t.Fatalf("an unreadable runtimechart must not be a pin site: %+v", site)
		}
	}
	foundSkipNote := false
	for _, note := range plan.Skipped {
		if strings.Contains(note, "runtimechart") {
			foundSkipNote = true
		}
	}
	if !foundSkipNote {
		t.Fatalf("expected a skipped note explaining the unreadable chart, got %+v", plan.Skipped)
	}
}

// ResolvePinPlan never writes, even when reporting a split image/chart pairing.
func TestResolvePinPlanReportsASplitImageChartPairingWithoutMutating(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	env := config.EnvConfig{
		Name:         "prod",
		RuntimeImage: "ghcr.io/sophium/petios-devops",
		RuntimeChart: "oci://ghcr.io/sophium/charts/erun-devops:1.0.201",
	}
	before := readPinFile(t, root, "acme-devops/docker/erun-devops/Dockerfile")

	plan, err := ResolvePinPlan(root, "petios", "prod", env, "1.0.228")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	changes := plan.Changes()
	var chartChange PinSite
	found := false
	for _, site := range changes {
		if site.Kind == PinSiteRuntimeChart {
			chartChange = site
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the split pairing's runtimechart to be reported as a change, got %+v", changes)
	}
	if chartChange.Current != "1.0.201" || chartChange.Target != "1.0.228" {
		t.Fatalf("runtime-chart change = %+v", chartChange)
	}
	after := readPinFile(t, root, "acme-devops/docker/erun-devops/Dockerfile")
	if before != after {
		t.Fatalf("resolving a plan must never write to the repo:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
