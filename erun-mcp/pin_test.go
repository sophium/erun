package erunmcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

func writePinTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// The reported bug: a project root that cannot be resolved must
// refuse rather than widen to whatever directory this process happens to be
// running in (its own cwd, a pod home directory). Unlike the CLI, there is no
// cwd fallback here at all -- an MCP caller has no cwd to be inside of.
func TestPinToolRefusesWhenNoProjectRootIsResolvable(t *testing.T) {
	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "frs", Environment: "prod"},
		Store: listToolStore{
			envConfigs: map[string]eruncommon.EnvConfig{
				"frs/prod": {Name: "prod", RuntimeVersion: "1.0.100"},
			},
		},
	}
	_, output, err := pinTool(runtime)(context.Background(), nil, PinInput{Preview: true, Version: "1.0.175"})
	if err == nil {
		t.Fatalf("expected a refusal, got %+v", output)
	}
	if !strings.Contains(err.Error(), "project root") {
		t.Fatalf("error should name the missing project root, got: %v", err)
	}
	if !strings.Contains(err.Error(), "projectRoot") {
		t.Fatalf("error should name the input field that fixes it, got: %v", err)
	}
}

// An explicit projectRoot input is exactly the remedy asked for: the
// caller names the repository since it has no cwd to fall back on.
func TestPinToolAcceptsAnExplicitProjectRoot(t *testing.T) {
	root := t.TempDir()
	if err := eruncommon.RecordPinPrevious(root, "frs", "prod", "1.0.100"); err != nil {
		t.Fatalf("seed previous pin: %v", err)
	}
	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "frs", Environment: "prod"},
		Store: listToolStore{
			envConfigs: map[string]eruncommon.EnvConfig{
				"frs/prod": {Name: "prod", RuntimeVersion: "1.0.175"},
			},
		},
	}
	_, output, err := pinTool(runtime)(context.Background(), nil, PinInput{Preview: true, Revert: true, ProjectRoot: root})
	if err != nil {
		t.Fatalf("pin: %v", err)
	}
	if output.Pin == nil || output.Pin.Plan.ProjectRoot != root {
		t.Fatalf("expected a plan resolved against the explicit projectRoot %q, got %+v", root, output.Pin)
	}
}

// The reported symptom: tenant/environment came back as empty strings and the
// plan proceeded anyway. This must refuse instead, the same way every other
// mutating tool in this module already refuses via resolveLocalTarget.
func TestPinToolRefusesWhenTenantAndEnvironmentAreUnresolved(t *testing.T) {
	runtime := RuntimeConfig{
		Context: RuntimeContext{RepoPath: t.TempDir()},
		Store:   listToolStore{},
	}
	_, _, err := pinTool(runtime)(context.Background(), nil, PinInput{Preview: true, Version: "1.0.175"})
	if err == nil {
		t.Fatal("expected a refusal when neither the server nor the caller names a tenant/environment")
	}
	assertNamesMissingAndRecovery(t, err, "tenant/environment")
}

// The reported bug's second half: even once a root resolves, it must never
// widen to a sibling checkout that happens to sit beside the tenant repo (the
// two-sibling-checkouts layout that reproduced it -- a tenant repo and erun's
// own repo checked out next to each other in the same pod).
func TestPinToolNeverScansOutsideTheResolvedProjectRoot(t *testing.T) {
	parent := t.TempDir()
	frsRoot := filepath.Join(parent, "frs")
	erunRoot := filepath.Join(parent, "erun")

	writePinTestFile(t, filepath.Join(frsRoot, "terraform-frs", "prod", "main.tf"), `module "edge" {
  source = "git::https://github.com/sophium/erun.git//erun-devops/terraform-erun/modules/terraform-erun-cluster-edge?ref=v1.0.100"
}
`)
	// erun's own sibling checkout -- must never be scanned when pin's root is
	// scoped to frsRoot, even though it names an erun-dns01-webhook reference.
	writePinTestFile(t, filepath.Join(erunRoot, "erun-devops", "terraform-erun", "modules", "terraform-erun-cluster-edge", "variables.tf"), `variable "dns01_webhook_image" {
  description = "Container image (repository:tag) for the DNS-01 webhook shim, e.g. \"ghcr.io/sophium/erun-dns01-webhook:1.0.150\"."
  type        = string
  default     = ""
}
`)

	if err := eruncommon.RecordPinPrevious(frsRoot, "frs", "prod", "1.0.050"); err != nil {
		t.Fatalf("seed previous pin: %v", err)
	}

	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "frs", Environment: "prod", RepoPath: frsRoot},
		Store: listToolStore{
			envConfigs: map[string]eruncommon.EnvConfig{
				"frs/prod": {Name: "prod", RuntimeVersion: "1.0.175"},
			},
		},
	}
	_, output, err := pinTool(runtime)(context.Background(), nil, PinInput{Preview: true, Revert: true})
	if err != nil {
		t.Fatalf("pin: %v", err)
	}
	assertPlanScopedToFrsOnly(t, output, frsRoot)
}

// The gap found while fixing erun#1771: nothing in this tool ever wrote the
// env-config half of a pin back to the store at all, so a real (non-preview)
// MCP re-pin left even runtimeversion unmoved -- the CLI transport's own
// applyPinCommand does this via saveEnvConfig, but the MCP path had no
// equivalent call. A real run must persist runtimeversion, a stated-stock
// runtimeimage, and runtimechart together, the same coordinate the CLI's
// integration suite locks.
func TestPinToolRealRunPersistsTheEnvConfigCoordinate(t *testing.T) {
	root := t.TempDir()
	store := listToolStore{
		envConfigs: map[string]eruncommon.EnvConfig{
			"frs/prod": {
				Name:           "prod",
				RuntimeVersion: "1.0.201",
				RuntimeImage:   "ghcr.io/sophium/erun-devops:1.0.201",
				RuntimeChart:   "oci://ghcr.io/sophium/charts/erun-devops:1.0.201",
			},
		},
	}
	runtime := RuntimeConfig{
		Context: RuntimeContext{Tenant: "frs", Environment: "prod", RepoPath: root},
		Store:   store,
	}
	_, output, err := pinTool(runtime)(context.Background(), nil, PinInput{Version: "1.0.228"})
	if err != nil {
		t.Fatalf("pin: %v", err)
	}
	if output.Pin == nil || !output.Pin.Applied {
		t.Fatalf("expected the plan to have applied, got %+v", output.Pin)
	}
	saved := store.envConfigs["frs/prod"]
	if saved.RuntimeVersion != "1.0.228" {
		t.Fatalf("runtimeversion = %q, want 1.0.228", saved.RuntimeVersion)
	}
	if saved.RuntimeImage != "ghcr.io/sophium/erun-devops:1.0.228" {
		t.Fatalf("runtimeimage = %q", saved.RuntimeImage)
	}
	if saved.RuntimeChart != "oci://ghcr.io/sophium/charts/erun-devops:1.0.228" {
		t.Fatalf("runtimechart = %q", saved.RuntimeChart)
	}
}

func assertPlanScopedToFrsOnly(t *testing.T, output JobEnvelopeOutput, frsRoot string) {
	t.Helper()
	if output.Pin == nil {
		t.Fatal("expected a resolved plan")
	}
	if output.Pin.Plan.ProjectRoot != frsRoot {
		t.Fatalf("projectRoot = %q, want %q", output.Pin.Plan.ProjectRoot, frsRoot)
	}
	if output.Pin.Plan.Tenant != "frs" || output.Pin.Plan.Environment != "prod" {
		t.Fatalf("plan lost its tenant/environment: %+v", output.Pin.Plan)
	}
	for _, site := range output.Pin.Plan.Sites {
		if strings.Contains(site.Path, "erun-devops") || strings.Contains(site.Detail, "erun-dns01-webhook") {
			t.Fatalf("a site from the sibling erun checkout leaked into frs's plan: %+v", site)
		}
		if strings.TrimSpace(site.Current) == "" {
			t.Fatalf("no site may report an empty current: %+v", site)
		}
	}
}
