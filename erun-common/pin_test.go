package eruncommon

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedPinnedTenantRepo lays down the shape a real tenant repo has: a Terraform
// module wrapping an erun module by ref, an umbrella chart mixing erun
// dependencies with the tenant's own, and a build-env Dockerfile on the runtime
// image. The drift is deliberate — this is the reported case of one repo pinned
// to three versions at once.
func seedPinnedTenantRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write := func(relative, body string) {
		full := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", relative, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}

	write("terraform-acme/dev/main.tf", `module "edge" {
  source = "git::https://github.com/sophium/erun.git//erun-devops/terraform-erun/modules/terraform-erun-cluster-edge?ref=v1.0.102"
  zone   = var.zone
}

module "tenant_owned" {
  source = "git::https://github.com/acme/infra.git//modules/thing?ref=v9.9.9"
}
`)
	write("acme-api/Chart.yaml", `apiVersion: v2
name: acme-api
version: 0.1.0
dependencies:
  - name: erun-backend-api
    repository: oci://ghcr.io/sophium/charts
    version: 1.0.106
  - name: acme-internal
    repository: oci://ghcr.io/acme/charts
    version: 3.2.1
`)
	write("acme-devops/docker/erun-devops/Dockerfile", `FROM ghcr.io/sophium/erun-devops:1.0.102
RUN apt-get update
`)
	// The reported gap: a tenant's own terraform variables set an erun-published
	// image directly (the cluster-edge module's dns01_webhook_image), not just a
	// module ref — and it names an erun release just as much as the ref above it.
	write("terraform-acme/prod/prod.tfvars", `dns01_webhook_image = "ghcr.io/sophium/erun-dns01-webhook:1.0.102"
broker_url          = "https://api.acme-prod.services.example.com/v1/dns01"
`)
	// The reported corruption: a variable's own description mentions an erun
	// image reference only as documentation prose, with the reference wrapped in
	// an HCL-escaped inner quote. This must not be read as a configured site.
	write("terraform-acme/dev/variables.tf", `variable "dns01_webhook_image" {
  description = "Container image (repository:tag) for the DNS-01 webhook shim, e.g. \"ghcr.io/sophium/erun-dns01-webhook:1.0.150\". Optional: left empty (the default), it resolves automatically."
  type        = string
  default     = ""
}
`)
	// A vendored copy of a pulled chart: a build artifact of a pin, not a pin.
	write("acme-api/charts/erun-backend-api/Chart.yaml", `apiVersion: v2
name: erun-backend-api
version: 1.0.106
dependencies:
  - name: erun-backend-api
    repository: oci://ghcr.io/sophium/charts
    version: 1.0.106
`)
	return root
}

func TestResolvePinPlanFindsEveryErunReferenceAndLeavesTheTenantsOwnAlone(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	env := EnvConfig{Name: "dev", RuntimeVersion: "1.0.115"}

	plan, err := ResolvePinPlan(root, "acme", "dev", env, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	byKind := map[PinSiteKind][]PinSite{}
	for _, site := range plan.Sites {
		byKind[site.Kind] = append(byKind[site.Kind], site)
	}
	if len(byKind[PinSiteTerraformRef]) != 1 {
		t.Fatalf("expected exactly the erun terraform ref, got %+v", byKind[PinSiteTerraformRef])
	}
	if got := byKind[PinSiteTerraformRef][0].Current; got != "1.0.102" {
		t.Fatalf("terraform ref current = %q", got)
	}
	if len(byKind[PinSiteHelmDependency]) != 1 {
		t.Fatalf("expected only the erun chart dependency, got %+v", byKind[PinSiteHelmDependency])
	}
	if got := byKind[PinSiteHelmDependency][0].Detail; got != "erun-backend-api" {
		t.Fatalf("helm dependency = %q, the tenant's own chart must not be a pin site", got)
	}
	if len(byKind[PinSiteRuntimeVersion]) != 1 || byKind[PinSiteRuntimeVersion][0].Current != "1.0.115" {
		t.Fatalf("expected the env's own runtimeversion as a site, got %+v", byKind[PinSiteRuntimeVersion])
	}
}

// The reported gap: dns01_webhook_image, set directly in a tenant's own
// terraform variables, is an erun-published image reference just like the
// module ref above it, and must surface as a site of its own.
func TestResolvePinPlanFindsAnErunImageReferenceInTerraformVariables(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	plan, err := ResolvePinPlan(root, "acme", "dev", EnvConfig{Name: "dev"}, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var found []PinSite
	for _, site := range plan.Sites {
		if site.Kind == PinSiteImageReference {
			found = append(found, site)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected the dns01_webhook_image reference in the tfvars file, got %+v", found)
	}
	if found[0].Detail != "erun-dns01-webhook" || found[0].Current != "1.0.102" {
		t.Fatalf("image reference = %+v, want erun-dns01-webhook at 1.0.102", found[0])
	}
}

// The reported corruption: an erun image reference mentioned only as
// an example inside a variable's description prose is not a configured site,
// and must never be reported or rewritten — reporting it as changed on an
// already-aligned tree contradicts the "aligned tree reports no changes"
// contract, and rewriting it would eat the HCL escape backslash right before
// it and break the surrounding string.
func TestResolvePinPlanDoesNotTreatDescriptionProseAsAnImageReferenceSite(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	before := readPinFile(t, root, "terraform-acme/dev/variables.tf")

	plan, err := ResolvePinPlan(root, "acme", "dev", EnvConfig{Name: "dev"}, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, site := range plan.Sites {
		if site.Path == "terraform-acme/dev/variables.tf" {
			t.Fatalf("a description-only mention must not be reported as a pin site: %+v", site)
		}
	}

	if err := ApplyPinPlan(plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	after := readPinFile(t, root, "terraform-acme/dev/variables.tf")
	if after != before {
		t.Fatalf("description prose must stay byte-identical:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// A vendored chart under charts/ is a build artifact of a pin, not a pin —
// rewriting it would edit what the next `helm dependency update` regenerates.
func TestResolvePinPlanSkipsVendoredCharts(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	plan, err := ResolvePinPlan(root, "acme", "dev", EnvConfig{Name: "dev"}, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, site := range plan.Sites {
		if strings.Contains(site.Path, "/charts/") {
			t.Fatalf("a vendored chart must not be a pin site: %+v", site)
		}
	}
}

// applyPinnedRepo re-pins the seeded repo and hands back its root, so each
// rewrite contract can be checked on its own rather than in one long test.
func applyPinnedRepo(t *testing.T) string {
	t.Helper()
	root := seedPinnedTenantRepo(t)
	plan, err := ResolvePinPlan(root, "acme", "dev", EnvConfig{Name: "dev", RuntimeVersion: "1.0.115"}, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if plan.Aligned() {
		t.Fatal("a drifted repo must report changes")
	}
	if err := ApplyPinPlan(plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return root
}

// A re-pin edits erun's references. The tenant's own module ref is not one of
// them, and rewriting it would silently retarget their infrastructure.
func TestApplyPinPlanRewritesErunTerraformRefsOnly(t *testing.T) {
	terraform := readPinFile(t, applyPinnedRepo(t), "terraform-acme/dev/main.tf")
	if !strings.Contains(terraform, "terraform-erun-cluster-edge?ref=v1.0.175") {
		t.Fatalf("erun terraform ref not re-pinned:\n%s", terraform)
	}
	if !strings.Contains(terraform, "github.com/acme/infra.git//modules/thing?ref=v9.9.9") {
		t.Fatalf("the tenant's own module ref was rewritten:\n%s", terraform)
	}
}

// The chart's own version and the tenant's own dependencies are versioned
// independently of erun, so only the erun dependency moves.
func TestApplyPinPlanRewritesErunChartDependenciesOnly(t *testing.T) {
	chart := readPinFile(t, applyPinnedRepo(t), "acme-api/Chart.yaml")
	if !strings.Contains(chart, "version: 1.0.175") {
		t.Fatalf("erun chart dependency not re-pinned:\n%s", chart)
	}
	if !strings.Contains(chart, "version: 3.2.1") || !strings.Contains(chart, "version: 0.1.0") {
		t.Fatalf("the chart's own version or the tenant dependency was rewritten:\n%s", chart)
	}
}

func TestApplyPinPlanRewritesTheBuildEnvImageWithoutDisturbingTheDockerfile(t *testing.T) {
	dockerfile := readPinFile(t, applyPinnedRepo(t), "acme-devops/docker/erun-devops/Dockerfile")
	if !strings.Contains(dockerfile, "erun-devops:1.0.175") {
		t.Fatalf("build-env image not re-pinned:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "RUN apt-get update") {
		t.Fatalf("the Dockerfile's own content was disturbed:\n%s", dockerfile)
	}
}

// The reported bug: a re-pin moved every other reference but left the
// dns01_webhook_image line in a tenant's own tfvars untouched, creating exactly
// the skew between the cluster-edge module and the shim it ships beside that
// pin exists to prevent.
func TestApplyPinPlanRewritesErunImageReferencesInTerraformVariableFiles(t *testing.T) {
	tfvars := readPinFile(t, applyPinnedRepo(t), "terraform-acme/prod/prod.tfvars")
	if !strings.Contains(tfvars, "ghcr.io/sophium/erun-dns01-webhook:1.0.175") {
		t.Fatalf("dns01_webhook_image not re-pinned:\n%s", tfvars)
	}
	if !strings.Contains(tfvars, `broker_url          = "https://api.acme-prod.services.example.com/v1/dns01"`) {
		t.Fatalf("an unrelated tfvars line was disturbed:\n%s", tfvars)
	}
}

// erun-devops already has its own dedicated runtime-image site; a
// registry-qualified reference to it in a tfvars file must not also be
// reported as a second, redundant image-reference site.
func TestResolvePinPlanDoesNotDoubleCountAnErunDevopsImageReferenceInTerraformVariables(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "terraform-acme", "prod", "prod.tfvars")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(`devops_image = "ghcr.io/sophium/erun-devops:1.0.102"`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	plan, err := ResolvePinPlan(root, "acme", "dev", EnvConfig{}, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, site := range plan.Sites {
		if site.Kind == PinSiteImageReference {
			t.Fatalf("erun-devops must not also surface as an image-reference site: %+v", site)
		}
	}
}

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
	env := EnvConfig{Name: "prod", RuntimeVersion: "1.0.76", RuntimeImage: "ghcr.io/sophium/frs-devops:1.0.76"}
	plan, err := ResolvePinPlan(root, "frs", "prod", env, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return plan
}

// Idempotent: re-running finds nothing left to do, which is what makes a re-pin
// safe against a partially aligned tree.
func TestApplyPinPlanIsANoOpOnceAligned(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	env := EnvConfig{Name: "dev", RuntimeVersion: "1.0.115"}

	plan, err := ResolvePinPlan(root, "acme", "dev", env, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := ApplyPinPlan(plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	env.RuntimeVersion = "1.0.175"
	again, err := ResolvePinPlan(root, "acme", "dev", env, "1.0.175")
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if !again.Aligned() {
		t.Fatalf("expected an aligned repo to report no changes, got %+v", again.Changes())
	}
}

// A rewritten Chart.yaml and the lock beside it must agree: `helm dependency
// build`, which deploy runs, refuses a lock that disagrees with its chart. So
// every chart the re-pin moved gets refreshed — once, however many of its
// dependencies changed.
func TestRefreshPinnedChartLocksVisitsEachMovedChartOnce(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	// A second erun dependency in the same chart must not mean two refreshes.
	chart := filepath.Join(root, "acme-api", "Chart.yaml")
	existing, err := os.ReadFile(chart)
	if err != nil {
		t.Fatalf("read chart: %v", err)
	}
	extra := string(existing) + "  - name: erun-backend-db\n    repository: oci://ghcr.io/sophium/charts\n    version: 1.0.106\n"
	if err := os.WriteFile(chart, []byte(extra), 0o644); err != nil {
		t.Fatalf("write chart: %v", err)
	}

	plan, err := ResolvePinPlan(root, "acme", "dev", EnvConfig{Name: "dev"}, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var visited []string
	err = RefreshPinnedChartLocks(Context{Logger: NewLoggerWithWriters(0, io.Discard, io.Discard)}, plan, func(_ Context, dir string) error {
		visited = append(visited, dir)
		return nil
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(visited) != 1 {
		t.Fatalf("expected one refresh for the one chart that moved, got %v", visited)
	}
	if filepath.Base(visited[0]) != "acme-api" {
		t.Fatalf("refreshed %q, want the chart directory", visited[0])
	}
}

// A tree with some locks refreshed and others stale is worse than the one we
// started from, so a failure names the chart and stops rather than carrying on.
func TestRefreshPinnedChartLocksStopsAtTheFailingChart(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	plan, err := ResolvePinPlan(root, "acme", "dev", EnvConfig{Name: "dev"}, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	err = RefreshPinnedChartLocks(Context{Logger: NewLoggerWithWriters(0, io.Discard, io.Discard)}, plan, func(_ Context, dir string) error {
		return fmt.Errorf("registry unreachable for %s", filepath.Base(dir))
	})
	if err == nil || !strings.Contains(err.Error(), "acme-api") {
		t.Fatalf("the failure must name the chart, got %v", err)
	}
}

// Nothing to refresh when no chart dependency moved — a terraform-only re-pin
// must not shell out to helm at all.
func TestRefreshPinnedChartLocksSkipsWhenNoChartMoved(t *testing.T) {
	root := t.TempDir()
	plan := PinPlan{ProjectRoot: root, Target: "1.0.175", Sites: []PinSite{
		{Kind: PinSiteTerraformRef, Path: "terraform-acme/dev/main.tf", Current: "1.0.102", Target: "1.0.175"},
	}}
	called := 0
	if err := RefreshPinnedChartLocks(Context{Logger: NewLoggerWithWriters(0, io.Discard, io.Discard)}, plan, func(Context, string) error {
		called++
		return nil
	}); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if called != 0 {
		t.Fatalf("expected no helm run when no chart moved, got %d", called)
	}
}

// Reverting has to reach the version you came from. Recording on a no-op re-pin
// would overwrite that with the version you are already on, stranding you.
func TestPinPreviousSurvivesARepeatedRePin(t *testing.T) {
	root := seedPinnedTenantRepo(t)

	if err := RecordPinPrevious(root, "acme", "dev", "1.0.115"); err != nil {
		t.Fatalf("record: %v", err)
	}
	previous, ok := PinPrevious(root, "acme", "dev")
	if !ok || previous != "1.0.115" {
		t.Fatalf("previous = %q %v", previous, ok)
	}

	if err := RecordPinPrevious(root, "acme", "dev", ""); err != nil {
		t.Fatalf("record empty: %v", err)
	}
	if previous, _ := PinPrevious(root, "acme", "dev"); previous != "1.0.115" {
		t.Fatalf("an empty previous must not clobber the recorded one, got %q", previous)
	}

	if _, ok := PinPrevious(root, "acme", "other"); ok {
		t.Fatal("another environment must not inherit this one's history")
	}
}

func TestResolvePinPlanRefusesWithoutATarget(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	if _, err := ResolvePinPlan(root, "acme", "dev", EnvConfig{}, "  "); err == nil {
		t.Fatal("a re-pin needs a target version")
	}
	if _, err := ResolvePinPlan("", "acme", "dev", EnvConfig{}, "1.0.175"); err == nil {
		t.Fatal("a re-pin needs a project root")
	}
	// A leading v is how the tags are written; the pins are not all written that
	// way, so the target is normalised once rather than at each site.
	plan, err := ResolvePinPlan(root, "acme", "dev", EnvConfig{}, "v1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if plan.Target != "1.0.175" {
		t.Fatalf("target = %q, want it normalised", plan.Target)
	}
}

// The reported bug (#1711): an unresolved tenant/environment must never reach
// a plan. Before this guard, ResolvePinPlan happily built one with tenant and
// environment both "", which then emitted a runtime-version site with
// current: "" and detail: "/" -- an unresolved reading dressed up as a real
// row a caller could apply.
func TestResolvePinPlanRefusesAnUnresolvedTenantOrEnvironment(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	for _, tc := range []struct {
		name        string
		tenant      string
		environment string
	}{
		{"both empty", "", ""},
		{"tenant empty", "", "dev"},
		{"environment empty", "acme", ""},
		{"both whitespace", "  ", "  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := ResolvePinPlan(root, tc.tenant, tc.environment, EnvConfig{}, "1.0.175")
			if err == nil {
				t.Fatalf("expected a refusal, got a plan: %+v", plan)
			}
			if !strings.Contains(err.Error(), "tenant") || !strings.Contains(err.Error(), "environment") {
				t.Fatalf("error should name what is unresolved, got: %v", err)
			}
		})
	}
}

func readPinFile(t *testing.T, root, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(data)
}
