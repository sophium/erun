package pin

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// seedPinnedTenantRepo lays down a tenant repo pinned to three erun versions at once.
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
	// Tenant terraform variables can set an erun image directly.
	write("terraform-acme/prod/prod.tfvars", `dns01_webhook_image = "ghcr.io/sophium/erun-dns01-webhook:1.0.102"
broker_url          = "https://api.acme-prod.services.example.com/v1/dns01"
`)
	// Description prose with an HCL-escaped reference must not be read as a site.
	write("terraform-acme/dev/variables.tf", `variable "dns01_webhook_image" {
  description = "Container image (repository:tag) for the DNS-01 webhook shim, e.g. \"ghcr.io/sophium/erun-dns01-webhook:1.0.150\". Optional: left empty (the default), it resolves automatically."
  type        = string
  default     = ""
}
`)
	// A vendored chart: a build artifact, not a pin.
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

// Rewriting the prose would also eat the HCL escape backslash before it.
func TestResolvePinPlanDoesNotTreatDescriptionProseAsAnImageReferenceSite(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	before := readPinFile(t, root, "terraform-acme/dev/variables.tf")

	plan, err := ResolvePinPlan(root, "acme", "dev", config.EnvConfig{Name: "dev"}, "1.0.175")
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

func TestResolvePinPlanSkipsVendoredCharts(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	plan, err := ResolvePinPlan(root, "acme", "dev", config.EnvConfig{Name: "dev"}, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, site := range plan.Sites {
		if strings.Contains(site.Path, "/charts/") {
			t.Fatalf("a vendored chart must not be a pin site: %+v", site)
		}
	}
}

func applyPinnedRepo(t *testing.T) string {
	t.Helper()
	root := seedPinnedTenantRepo(t)
	plan, err := ResolvePinPlan(root, "acme", "dev", config.EnvConfig{Name: "dev", RuntimeVersion: "1.0.115"}, "1.0.175")
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

func TestApplyPinPlanRewritesTheBuildEnvImageWithoutDisturbingTheDockerfile(t *testing.T) {
	dockerfile := readPinFile(t, applyPinnedRepo(t), "acme-devops/docker/erun-devops/Dockerfile")
	if !strings.Contains(dockerfile, "erun-devops:1.0.175") {
		t.Fatalf("build-env image not re-pinned:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "RUN apt-get update") {
		t.Fatalf("the Dockerfile's own content was disturbed:\n%s", dockerfile)
	}
}

// erun-devops already has its own runtime-image site.
func TestResolvePinPlanDoesNotDoubleCountAnErunDevopsImageReferenceInTerraformVariables(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "terraform-acme", "prod", "prod.tfvars")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(`devops_image = "ghcr.io/sophium/erun-devops:1.0.102"`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	plan, err := ResolvePinPlan(root, "acme", "dev", config.EnvConfig{}, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, site := range plan.Sites {
		if site.Kind == model.PinSiteImageReference {
			t.Fatalf("erun-devops must not also surface as an image-reference site: %+v", site)
		}
	}
}

// `helm dependency build` refuses a lock that disagrees with its chart.
func TestRefreshPinnedChartLocksVisitsEachMovedChartOnce(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	chart := filepath.Join(root, "acme-api", "Chart.yaml")
	existing, err := os.ReadFile(chart)
	if err != nil {
		t.Fatalf("read chart: %v", err)
	}
	extra := string(existing) + "  - name: erun-backend-db\n    repository: oci://ghcr.io/sophium/charts\n    version: 1.0.106\n"
	if err := os.WriteFile(chart, []byte(extra), 0o644); err != nil {
		t.Fatalf("write chart: %v", err)
	}

	plan, err := ResolvePinPlan(root, "acme", "dev", config.EnvConfig{Name: "dev"}, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var visited []string
	err = RefreshPinnedChartLocks(core.Context{Logger: core.NewLoggerWithWriters(0, io.Discard, io.Discard)}, plan, func(_ core.Context, dir string) error {
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

func TestRefreshPinnedChartLocksStopsAtTheFailingChart(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	plan, err := ResolvePinPlan(root, "acme", "dev", config.EnvConfig{Name: "dev"}, "1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	err = RefreshPinnedChartLocks(core.Context{Logger: core.NewLoggerWithWriters(0, io.Discard, io.Discard)}, plan, func(_ core.Context, dir string) error {
		return fmt.Errorf("registry unreachable for %s", filepath.Base(dir))
	})
	if err == nil || !strings.Contains(err.Error(), "acme-api") {
		t.Fatalf("the failure must name the chart, got %v", err)
	}
}

func TestResolvePinPlanRefusesWithoutATarget(t *testing.T) {
	root := seedPinnedTenantRepo(t)
	if _, err := ResolvePinPlan(root, "acme", "dev", config.EnvConfig{}, "  "); err == nil {
		t.Fatal("a re-pin needs a target version")
	}
	if _, err := ResolvePinPlan("", "acme", "dev", config.EnvConfig{}, "1.0.175"); err == nil {
		t.Fatal("a re-pin needs a project root")
	}
	plan, err := ResolvePinPlan(root, "acme", "dev", config.EnvConfig{}, "v1.0.175")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if plan.Target != "1.0.175" {
		t.Fatalf("target = %q, want it normalised", plan.Target)
	}
}

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
			plan, err := ResolvePinPlan(root, tc.tenant, tc.environment, config.EnvConfig{}, "1.0.175")
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
