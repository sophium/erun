package lifecycle

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
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
		if site.Kind == model.PinSiteRuntimeVersion {
			t.Fatalf("runtimeversion must follow the image the pod actually ran, not the stale pin: %+v", site)
		}
	}
	note := skipNoteNaming(t, plan, "runtimeversion", "ghcr.io/sophium/frs-devops:1.0.134")
	if !strings.Contains(note, "runtimerunningimage") {
		t.Fatalf("the skipped note must name runtimerunningimage as what it read, got %q", note)
	}
}

// skipNoteNaming returns the one skipped note containing every fragment.
func skipNoteNaming(t *testing.T, plan model.PinPlan, fragments ...string) string {
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
		if site.Kind == model.PinSiteRuntimeChart {
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
		if site.Kind == model.PinSiteRuntimeChart {
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
