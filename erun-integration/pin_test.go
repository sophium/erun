package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// seedDriftedPins pins a tenant repo to three different versions.
func seedDriftedPins(t *testing.T, root, rootConfigDir string) {
	t.Helper()
	write := func(relative, body string) {
		full := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", relative, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}
	seedUnreachableRuntimeRegistry(t, rootConfigDir)
	write("terraform-team/dev/main.tf", "module \"edge\" {\n  source = \"git::https://github.com/sophium/erun.git//erun-devops/terraform-erun/modules/terraform-erun-cluster-edge?ref=v1.0.102\"\n}\n\nmodule \"own\" {\n  source = \"git::https://github.com/team/infra.git//modules/thing?ref=v9.9.9\"\n}\n")
	write("team-api/Chart.yaml", "apiVersion: v2\nname: team-api\nversion: 0.1.0\ndependencies:\n  - name: erun-backend-api\n    repository: oci://ghcr.io/sophium/charts\n    version: 1.0.106\n  - name: team-internal\n    repository: oci://ghcr.io/team/charts\n    version: 3.2.1\n")
}

// seedUnreachableRuntimeRegistry makes discovery offline; "could not verify" still lets
// an explicit target pin.
func seedUnreachableRuntimeRegistry(t *testing.T, rootConfigDir string) {
	t.Helper()
	rootConfigFile := filepath.Join(rootConfigDir, "config.yaml")
	existing, err := os.ReadFile(rootConfigFile)
	if err != nil || strings.Contains(string(existing), "runtimeregistry") {
		return
	}
	if err := os.WriteFile(rootConfigFile, append(existing, []byte("runtimeregistry:\n  baseurl: http://127.0.0.1:1\n  tokenurl: http://127.0.0.1:1\n")...), 0o644); err != nil {
		t.Fatalf("write root config: %v", err)
	}
}

// seedDns01WebhookImageDrift sets dns01_webhook_image directly in tenant terraform variables.
func seedDns01WebhookImageDrift(t *testing.T, root, rootConfigDir string) {
	t.Helper()
	seedUnreachableRuntimeRegistry(t, rootConfigDir)
	full := filepath.Join(root, "terraform-team", "prod", "prod.tfvars")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "dns01_webhook_image = \"ghcr.io/sophium/erun-dns01-webhook:1.0.102\"\n" +
		"broker_url          = \"https://api.team-prod.services.example.com/v1/dns01\"\n"
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// Registry tags include scratch tags, and the desktop picker shows this list unfiltered.
func assertNoStrayTagOffered(t *testing.T, combined string) {
	t.Helper()
	for _, line := range strings.Split(combined, "\n") {
		switch strings.TrimSpace(line) {
		case "permcheck-tmp", "authprobe", "latest":
			t.Fatalf("a scratch tag must not be offered as a pinnable version:\n%s", combined)
		}
	}
}

func TestPin(t *testing.T) {
	t.Parallel()
	t.Run("help", testPinHelp)

	t.Run("refuses_host_environment", testPinRefusesHostEnvironment)

	t.Run("dry_run_reports_every_site_and_writes_nothing", testPinDryRunReportsEverySiteAndWritesNothing)

	t.Run("skips_a_tenant_runtimeimage_tag_and_says_so", testPinSkipsATenantRuntimeimageTagAndSaysSo)

	t.Run("real_run_leaves_a_tenant_imaged_runtimeversion_alone", testPinRealRunLeavesATenantImagedRuntimeversionAlone)

	t.Run("refuses_a_sourceless_runtime_env_with_no_tenant_checkout", testPinRefusesASourcelessRuntimeEnvWithNoTenantCheckout)

	t.Run("resolves_a_sourceless_runtime_envs_root_from_a_sibling_checkout", testPinResolvesASourcelessRuntimeEnvsRootFromASiblingCheckout)

	t.Run("cwd_fallback_names_the_head_and_warns_when_stale", testPinCwdFallbackNamesTheHeadAndWarnsWhenStale)

	t.Run("names_a_base_that_is_behind_its_remote", testPinNamesABaseThatIsBehindItsRemote)

	t.Run("dry_run_reports_a_stale_dns01_webhook_image_reference", testPinDryRunReportsAStaleDNS01WebhookImageReference)

	t.Run("real_run_pins_the_dns01_webhook_image_reference", testPinRealRunPinsTheDNS01WebhookImageReference)

	t.Run("dry_run_reports_a_stale_runtimechart_reference", testPinDryRunReportsAStaleRuntimechartReference)

	t.Run("real_run_pins_runtimeversion_runtimeimage_and_runtimechart_together", testPinRealRunPinsRuntimeversionRuntimeimageAndRuntimechartTogether)

	t.Run("real_run_leaves_a_tenant_own_runtimechart_line_unchanged", testPinRealRunLeavesATenantOwnRuntimechartLineUnchanged)

	t.Run("skips_an_own_umbrella_envs_runtimeversion_and_says_so", testPinSkipsAnOwnUmbrellaEnvsRuntimeversionAndSaysSo)

	t.Run("list_reports_the_published_versions", testPinListReportsThePublishedVersions)

	t.Run("list_works_without_a_default_tenant_configured", testPinListWorksWithoutADefaultTenantConfigured)

	t.Run("refuses_a_version_the_registry_does_not_carry", testPinRefusesAVersionTheRegistryDoesNotCarry)

	t.Run("real_run_pins_every_reference_then_reverts", testPinRealRunPinsEveryReferenceThenReverts)

	t.Run("revert_without_a_recorded_pin_says_so", testPinRevertWithoutARecordedPinSaysSo)
}

func testPinHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"pin", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "pin/help", normalize.Apply(result.Combined))
}

func testPinRefusesHostEnvironment(t *testing.T) {
	// The host check runs after resolvePinTarget, which would otherwise reach the real
	// registry for an explicit --version.
	setup := env.New(t)
	fixture.SeedHostTenantEnv(t, setup, "team", "dev")
	seedUnreachableRuntimeRegistry(t, filepath.Join(setup.ConfigHome, "erun"))
	result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "pin/refuses_host_environment", normalize.Apply(result.Combined))
}

func testPinDryRunReportsEverySiteAndWritesNothing(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))
	before, err := os.ReadFile(filepath.Join(setup.Cwd, "terraform-team", "dev", "main.tf"))
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	for _, want := range []string{
		"terraform-ref",
		"helm-dependency",
		"1.0.175",
		// team/dev has no runtimeimage, so its runtime rides the tenant's own image.
		"skipped: runtimeversion team/dev rides team-devops",
	} {
		if !strings.Contains(result.Combined, want) {
			t.Fatalf("the plan must name %q:\n%s", want, result.Combined)
		}
	}
	after, err := os.ReadFile(filepath.Join(setup.Cwd, "terraform-team", "dev", "main.tf"))
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("a dry run must not write:\n%s", after)
	}
}

// A sourceless runtime env with no tenant checkout here must refuse rather than use the cwd.
func testPinRefusesASourcelessRuntimeEnvWithNoTenantCheckout(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvNoRepoPath(t, setup, "frs", "prod")

	result := erun.Run(t, []string{"pin", "frs", "prod", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a refusal, got exit 0: %s", result.Combined)
	}
	for _, want := range []string{"frs/prod", "no local checkout", "no other frs environment"} {
		if !strings.Contains(result.Combined, want) {
			t.Fatalf("expected %q in the refusal:\n%s", want, result.Combined)
		}
	}
}

// Every env of a tenant shares one repo, so a sibling's checkout is used.
func testPinResolvesASourcelessRuntimeEnvsRootFromASiblingCheckout(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "frs", "dev")
	fixture.SeedRuntimeTenantEnvNoRepoPath(t, setup, "frs", "prod")
	seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))

	result := erun.Run(t, []string{"pin", "frs", "prod", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	for _, want := range []string{"terraform-ref", "helm-dependency"} {
		if !strings.Contains(result.Combined, want) {
			t.Fatalf("expected %q in the plan:\n%s", want, result.Combined)
		}
	}
	if strings.Contains(result.Combined, "caller's working directory") {
		t.Fatalf("a sibling checkout resolved the root; it must not read as a cwd guess:\n%s", result.Combined)
	}
}

// The cwd fallback must name HEAD and warn when its pins disagree with what is deployed.
func testPinCwdFallbackNamesTheHeadAndWarnsWhenStale(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvNoRepoPath(t, setup, "frs", "prod")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "frs", "prod", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	if err := os.WriteFile(envConfigPath, append(existing, []byte("runtimeversion: 1.0.50\n")...), 0o644); err != nil {
		t.Fatalf("write env config: %v", err)
	}
	fixture.SeedGitRepo(t, setup.Cwd)
	seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))

	result := erun.Run(t, []string{"pin", "frs", "prod", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	for _, want := range []string{
		"caller's working directory",
		"HEAD is",
		"disagrees with frs/prod's deployed version (1.0.50)",
	} {
		if !strings.Contains(result.Combined, want) {
			t.Fatalf("expected %q in the plan:\n%s", want, result.Combined)
		}
	}
}

// A plan from a checkout behind its remote looks consistent but would revert missing
// commits, so it must name the divergence and the ref.
func testPinNamesABaseThatIsBehindItsRemote(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "frs", "prod")
	fixture.SeedGitRepoBehindItsRemote(t, setup.Cwd)
	seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))

	result := erun.Run(t, []string{"pin", "frs", "prod", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	for _, want := range []string{
		"1 commit(s) behind origin/main",
		"the plan describes that older base",
	} {
		if !strings.Contains(result.Combined, want) {
			t.Fatalf("expected %q in the plan:\n%s", want, result.Combined)
		}
	}
	// The warning annotates the plan; it must not replace it.
	if !strings.Contains(result.Combined, "change terraform-ref terraform-team/dev/main.tf") {
		t.Fatalf("expected the plan's sites to survive the warning:\n%s", result.Combined)
	}
}

func testPinDryRunReportsAStaleDNS01WebhookImageReference(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedDns01WebhookImageDrift(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))

	result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "pin/dry_run_reports_a_stale_dns01_webhook_image_reference", normalize.Apply(result.Combined))
}

func testPinRealRunPinsTheDNS01WebhookImageReference(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedDns01WebhookImageDrift(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))

	result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	tfvars := readPinnedFile(t, setup.Cwd, "terraform-team/prod/prod.tfvars")
	if !strings.Contains(tfvars, "ghcr.io/sophium/erun-dns01-webhook:1.0.175") {
		t.Fatalf("dns01_webhook_image not re-pinned:\n%s", tfvars)
	}
	if !strings.Contains(tfvars, `broker_url          = "https://api.team-prod.services.example.com/v1/dns01"`) {
		t.Fatalf("an unrelated tfvars line was disturbed:\n%s", tfvars)
	}
}

// Served locally: a real registry would measure the host.
func testPinListReportsThePublishedVersions(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"next":"","results":[{"name":"1.0.174"},{"name":"1.0.173"},{"name":"1.0.172-snapshot-20260821151853"},{"name":"permcheck-tmp"},{"name":"authprobe"},{"name":"latest"}]}`)
	}))
	defer server.Close()
	writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n  namespace: acme\n  repository: erun-devops\n  baseurl: "+server.URL+"\n")

	result := erun.Run(t, []string{"pin", "team", "dev", "--list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	for _, want := range []string{
		"latest stable: 1.0.174",
		"1.0.173",
		"1.0.172-snapshot-20260821151853",
	} {
		if !strings.Contains(result.Combined, want) {
			t.Fatalf("expected %q in the listing:\n%s", want, result.Combined)
		}
	}
	assertNoStrayTagOffered(t, result.Combined)
}

// `pin --list` reads erun's own registry, so it needs no default tenant.
func testPinListWorksWithoutADefaultTenantConfigured(t *testing.T) {
	setup := env.New(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"next":"","results":[{"name":"1.0.174"},{"name":"1.0.173"},{"name":"1.0.172-snapshot-20260821151853"},{"name":"permcheck-tmp"},{"name":"authprobe"},{"name":"latest"}]}`)
	}))
	defer server.Close()
	writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n  namespace: acme\n  repository: erun-devops\n  baseurl: "+server.URL+"\n")

	result := erun.Run(t, []string{"pin", "--list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "default tenant is not configured") {
		t.Fatalf("--list must not require a default tenant:\n%s", result.Combined)
	}
	for _, want := range []string{
		"latest stable: 1.0.174",
		"1.0.173",
		"1.0.172-snapshot-20260821151853",
	} {
		if !strings.Contains(result.Combined, want) {
			t.Fatalf("expected %q in the listing:\n%s", want, result.Combined)
		}
	}
	assertNoStrayTagOffered(t, result.Combined)
}

// An unpublished version would fail only later at terraform init or chart pull.
func testPinRefusesAVersionTheRegistryDoesNotCarry(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"next":"","results":[{"name":"1.0.174"}]}`)
	}))
	defer server.Close()
	writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n  namespace: acme\n  repository: erun-devops\n  baseurl: "+server.URL+"\n")

	result := erun.Run(t, []string{"pin", "team", "dev", "--version", "9.9.9"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a refusal, got exit 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "not published") {
		t.Fatalf("the refusal must say why:\n%s", result.Combined)
	}
}

func testPinRealRunPinsEveryReferenceThenReverts(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "helm", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm")...)

	apply := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.174"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if apply.ExitCode != 0 {
		t.Fatalf("exit %d: %s", apply.ExitCode, apply.Combined)
	}
	terraform := readPinnedFile(t, setup.Cwd, "terraform-team/dev/main.tf")
	if !strings.Contains(terraform, "?ref=v1.0.174") {
		t.Fatalf("terraform ref not re-pinned:\n%s", terraform)
	}
	// A tenant's own module ref is not an erun pin.
	if !strings.Contains(terraform, "github.com/team/infra.git//modules/thing?ref=v9.9.9") {
		t.Fatalf("the tenant's own ref was rewritten:\n%s", terraform)
	}
	chart := readPinnedFile(t, setup.Cwd, "team-api/Chart.yaml")
	if !strings.Contains(chart, "version: 1.0.174") || !strings.Contains(chart, "version: 3.2.1") {
		t.Fatalf("chart dependencies wrong:\n%s", chart)
	}

	again := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.174"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if again.ExitCode != 0 {
		t.Fatalf("exit %d: %s", again.ExitCode, again.Combined)
	}
	if !strings.Contains(again.Combined, "already pinned") {
		t.Fatalf("a re-run must report the no-op:\n%s", again.Combined)
	}

	revert := erun.Run(t, []string{"pin", "team", "dev", "--revert"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if revert.ExitCode != 0 {
		t.Fatalf("exit %d: %s", revert.ExitCode, revert.Combined)
	}
	reverted := readPinnedFile(t, setup.Cwd, "terraform-team/dev/main.tf")
	if strings.Contains(reverted, "?ref=v1.0.174") {
		t.Fatalf("revert did not move the terraform ref back:\n%s", reverted)
	}
}

func testPinRevertWithoutARecordedPinSaysSo(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))

	result := erun.Run(t, []string{"pin", "team", "dev", "--revert"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a refusal, got exit 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "nothing to revert to") {
		t.Fatalf("the refusal must say why:\n%s", result.Combined)
	}
}

func readPinnedFile(t *testing.T, root, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(data)
}
