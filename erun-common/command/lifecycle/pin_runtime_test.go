package lifecycle

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

// The reported bug: frs/prod runs a tenant image
// (ghcr.io/sophium/frs-devops) versioned on frs's own release line, not
// erun's. Re-pinning it must leave runtimeversion alone — writing the erun
// target there would name a tag frs's own line never publishes — while every
// erun-owned reference in the repo still moves.
func TestResolvePinPlanLeavesATenantImagedEnvsRuntimeVersionAlone(t *testing.T) {
	plan := resolveTenantImagedRepoPlan(t)
	for _, site := range plan.Sites {
		if site.Kind == PinSiteRuntimeVersion {
			t.Fatalf("a tenant-imaged env's runtimeversion must not be a pin site: %+v", site)
		}
	}
	foundSkipNote := false
	for _, note := range plan.Skipped {
		if strings.Contains(note, "runtimeversion") && strings.Contains(note, "frs-devops") {
			foundSkipNote = true
		}
	}
	if !foundSkipNote {
		t.Fatalf("expected a skipped note naming the tenant image, got %+v", plan.Skipped)
	}
}

// The tenant-imaged guard on runtimeversion must not spill over into the
// repo's own erun-owned references, which still need to move.
func TestResolvePinPlanStillPinsErunOwnedReferencesForATenantImagedEnv(t *testing.T) {
	plan := resolveTenantImagedRepoPlan(t)
	byKind := map[PinSiteKind][]PinSite{}
	for _, site := range plan.Sites {
		byKind[site.Kind] = append(byKind[site.Kind], site)
	}
	if len(byKind[PinSiteTerraformRef]) != 1 || len(byKind[PinSiteHelmDependency]) != 1 {
		t.Fatalf("erun-owned references must still be pinned, got %+v", plan.Sites)
	}
}

// resolveTenantImagedRepoPlan resolves the reported case: frs/prod
// runs a tenant image (ghcr.io/sophium/frs-devops) versioned on frs's own
// release line, not erun's.
func resolveTenantImagedRepoPlan(t *testing.T) PinPlan {
	t.Helper()
	root := seedPinnedTenantRepo(t)
	env := config.EnvConfig{Name: "prod", RuntimeVersion: "1.0.76", RuntimeImage: "ghcr.io/sophium/frs-devops:1.0.76"}
	plan, err := ResolvePinPlan(root, "frs", "prod", env, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return plan
}

// The reported case, exactly as it reproduced: frs/build has no runtimeimage
// recorded at all, so the operative-pin guard cannot see which release line the
// environment's runtime pod is on. Its own umbrella is the statement it does
// make: an env with no runtimeimage runs the chart's own image
// (defaultDeployRuntimeImageBareName), and the frs-devops umbrella is frs's
// line, not erun's. Re-pinning must leave runtimeversion alone — the erun target
// is not this environment's number to hold — and say which statement it read.
func TestResolvePinPlanLeavesAnOwnUmbrellaEnvsRuntimeVersionAlone(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	env := config.EnvConfig{Name: "build", RuntimeChart: "oci://ghcr.io/sophium/charts/frs-devops:1.0.134"}

	plan, err := ResolvePinPlan(root, "frs", "build", env, "1.0.287")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, site := range plan.Sites {
		if site.Kind == PinSiteRuntimeVersion {
			t.Fatalf("an env riding its own umbrella's release line must not have runtimeversion re-pinned: %+v", site)
		}
	}
	note := skipNoteNaming(t, plan, "runtimeversion", "frs-devops")
	if !strings.Contains(note, "runtimechart") {
		t.Fatalf("the skipped note must name runtimechart as what it read, got %q", note)
	}
}

// The same environment after a deploy that healed only half of the coordinate:
// a persisted runtimeimage still naming erun's stock image while the pod
// actually runs the tenant's own. The last image a deploy confirmed running is
// the observed truth, so runtimeversion must follow it rather than the stale
// pin — and the note must say where it read it.
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

// The reported case at its narrowest: the config says nothing at all about the
// environment's runtime image, which is how frs/build was configured. Silence is
// not undetermined here — a deploy of this environment installs
// ghcr.io/sophium/frs-devops (the tenant's own line's image, the default
// resolveDeployRuntimeImage falls back to), and records that deploy's version as
// runtimeversion. Re-pinning must leave the field alone and name the statement
// the decision rested on.
func TestResolvePinPlanLeavesAnEnvsRuntimeVersionAloneWhenNothingIsStated(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	env := config.EnvConfig{Name: "build"}

	plan, err := ResolvePinPlan(root, "frs", "build", env, "1.0.287")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, site := range plan.Sites {
		if site.Kind == PinSiteRuntimeVersion {
			t.Fatalf("an env whose deploy installs its tenant's own image must not have runtimeversion re-pinned: %+v", site)
		}
	}
	note := skipNoteNaming(t, plan, "runtimeversion", "frs-devops")
	if !strings.Contains(note, "by default") {
		t.Fatalf("the skipped note must say where that image came from, got %q", note)
	}
}

// The other half of the pair: an environment whose runtime image is erun's own is
// still re-pinned, runtimeversion included — either because it states the stock
// image, or because it is an environment of erun's own tenant, whose deploys
// install the stock image with nothing stated at all. Skipping those would leave
// a real drift behind while the plan read as complete.
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

// skipNoteNaming returns the one skipped note mentioning every fragment, so a
// test asserting on a note's wording does not silently pass on a different one.
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

// The reported bug: erun's own runtimechart, versioned on erun's own release
// line just like runtimeversion and a stated runtimeimage, was not a pin site
// at all — a re-pin could move every other reference and still leave the
// chart an environment actually installs pointed at the old version.
func TestResolvePinPlanMovesTheStockRuntimeChartVersion(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	env := config.EnvConfig{Name: "dev", RuntimeChart: "oci://ghcr.io/sophium/charts/erun-devops:1.0.201"}

	plan, err := ResolvePinPlan(root, "acme", "dev", env, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var found []PinSite
	for _, site := range plan.Sites {
		if site.Kind == PinSiteRuntimeChart {
			found = append(found, site)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one runtime-chart site, got %+v", plan.Sites)
	}
	if found[0].Current != "1.0.201" || found[0].Target != "1.0.175" {
		t.Fatalf("runtime-chart site = %+v, want current 1.0.201 target 1.0.175", found[0])
	}
}

// The reported case: an environment that deliberately runs the chart on a
// different release line than erun's own — the tenant's own umbrella, exactly what
// --runtime-chart exists to let an operator state — is a real configuration,
// not drift. A re-pin must leave it alone, the same way it leaves a
// tenant-imaged runtimeimage alone.
func TestResolvePinPlanLeavesATenantOwnRuntimeChartAlone(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	env := config.EnvConfig{Name: "dev", RuntimeChart: "oci://ghcr.io/sophium/charts/acme-devops:1.0.76"}

	plan, err := ResolvePinPlan(root, "acme", "dev", env, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, site := range plan.Sites {
		if site.Kind == PinSiteRuntimeChart {
			t.Fatalf("a tenant's own runtimechart must not be a pin site: %+v", site)
		}
	}
	foundSkipNote := false
	for _, note := range plan.Skipped {
		if strings.Contains(note, "runtimechart") && strings.Contains(note, "acme-devops") {
			foundSkipNote = true
		}
	}
	if !foundSkipNote {
		t.Fatalf("expected a skipped note naming the tenant chart, got %+v", plan.Skipped)
	}
}

// A runtimechart naming the stock chart with no version stated already rides
// the deploy version, exactly like a tagless runtimeimage — there is nothing
// for pin to move, and nothing to warn about either.
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

// An unclassifiable runtimechart (one pin cannot even read a chart name out
// of) must proceed rather than block a plan it merely could not read — the
// same "unknown is not fine, but unknown is not wrong either" rule
// established for the runtime-image line guard.
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

// The reported shape, exactly: a tenant-imaged env (runtimeimage on the
// tenant's own line) whose runtimechart still names erun's own stock chart at
// an old version. Resolving the plan must report the split pairing — and,
// since ResolvePinPlan never writes, it must do so without mutating anything.
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

// The whole coordinate, applied: a stated-stock runtimeimage, runtimechart,
// and runtimeversion all move to the target in the one write
// ApplyPinnedEnvConfig computes.
func TestApplyPinnedEnvConfigMovesRuntimeVersionImageAndChartTogether(t *testing.T) {
	env := config.EnvConfig{
		Name:           "dev",
		RuntimeVersion: "1.0.201",
		RuntimeImage:   "ghcr.io/sophium/erun-devops:1.0.201",
		RuntimeChart:   "oci://ghcr.io/sophium/charts/erun-devops:1.0.201",
	}
	plan, err := ResolvePinPlan(t.TempDir(), "acme", "dev", env, "1.0.228")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	updated, changed := ApplyPinnedEnvConfig(env, plan)
	if !changed {
		t.Fatal("expected the coordinate to move")
	}
	if updated.RuntimeVersion != "1.0.228" {
		t.Fatalf("runtimeversion = %q", updated.RuntimeVersion)
	}
	if updated.RuntimeImage != "ghcr.io/sophium/erun-devops:1.0.228" {
		t.Fatalf("runtimeimage = %q", updated.RuntimeImage)
	}
	if updated.RuntimeChart != "oci://ghcr.io/sophium/charts/erun-devops:1.0.228" {
		t.Fatalf("runtimechart = %q", updated.RuntimeChart)
	}
}

// A deliberately-separate chart line (the tenant's own umbrella) is the whole
// coordinate, not just the chart half: an env running that umbrella runs the
// image it publishes, so its runtimeversion belongs to that line and must
// survive a re-pin too. Writing the erun target into runtimeversion here is what
// left an environment holding a number its own release line never publishes.
func TestApplyPinnedEnvConfigLeavesAnOwnUmbrellaEnvsWholeCoordinateUnchanged(t *testing.T) {
	env := config.EnvConfig{
		Name:           "dev",
		RuntimeVersion: "1.0.76",
		RuntimeChart:   "oci://ghcr.io/sophium/charts/acme-devops:1.0.76",
	}
	plan, err := ResolvePinPlan(t.TempDir(), "acme", "dev", env, "1.0.228")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	updated, changed := ApplyPinnedEnvConfig(env, plan)
	if updated.RuntimeChart != "oci://ghcr.io/sophium/charts/acme-devops:1.0.76" {
		t.Fatalf("a tenant's own chart line must survive unchanged, got %q", updated.RuntimeChart)
	}
	if updated.RuntimeVersion != "1.0.76" {
		t.Fatalf("an own-umbrella env's runtimeversion must survive unchanged, got %q", updated.RuntimeVersion)
	}
	if changed {
		t.Fatalf("nothing in this env's own config is erun's to move, yet a change was reported: %+v", updated)
	}
}
