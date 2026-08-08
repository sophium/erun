package eruncommon

import (
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

func readPinFile(t *testing.T, root, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(data)
}
