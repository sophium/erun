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

// One erun version is recorded in several places and they only work when they
// agree. These scenarios pin the contract of the motion that realigns them:
// which references it recognises, which it leaves alone, and that it never
// writes on a dry run.

// seedDriftedPins writes the shape a real tenant repo has, deliberately pinned
// to three different versions — the reported drift this command exists for.
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

// seedUnreachableRuntimeRegistry points the root config's registry lookup at a
// closed port, so version discovery is deterministic and offline. An
// unreachable registry means "could not verify", which is what lets an
// explicit target still pin.
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

// seedDns01WebhookImageDrift writes the shape the reported issue hit: a
// tenant's own terraform variables set the cluster-edge module's
// dns01_webhook_image directly, pinned to an old erun release — the one
// reference a repin left behind because nothing recognised it.
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

func TestPin(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"pin", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "pin/help", normalize.Apply(result.Combined))
	})

	t.Run("refuses_host_environment", func(t *testing.T) {
		// A host env has no pod and no runtime version at all, so pin must
		// refuse it by name instead of resolving a plan with nothing to pin.
		setup := env.New(t)
		fixture.SeedHostTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "pin/refuses_host_environment", normalize.Apply(result.Combined))
	})

	// A dry run resolves the whole plan — every site, old and new — and leaves
	// the tree exactly as it found it. A re-pin edits files across a repo, so
	// being able to see it first is the difference between a safe motion and a
	// leap.
	t.Run("dry_run_reports_every_site_and_writes_nothing", func(t *testing.T) {
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
			"runtime-version",
			"1.0.175",
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
	})

	// A tenant's own runtimeimage tag rides the tenant's own release
	// line, not erun's. Rewriting it to the erun target version would name a
	// tag that line never publishes, guaranteeing an ImagePullBackOff on the
	// very next deploy — produced by the command whose whole purpose is
	// keeping version references consistent. pin must leave it alone and say
	// so in the plan, so the operator is not left assuming it was covered.
	t.Run("skips_a_tenant_runtimeimage_tag_and_says_so", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		if err := os.WriteFile(envConfigPath, append(existing, []byte("runtimeimage: ghcr.io/sophium/team-devops:old-tag\n")...), 0o644); err != nil {
			t.Fatalf("write env config: %v", err)
		}

		result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "pin/skips_a_tenant_runtimeimage_tag_and_says_so", normalize.Apply(result.Combined))
	})

	// The reported bug, for real: applying a re-pin for a tenant-imaged
	// env must not overwrite its persisted runtimeversion — that would name a
	// tag the tenant's own image line never publishes, guaranteeing an
	// ImagePullBackOff on the next deploy — while the rest of the repo's
	// erun-owned references still move.
	t.Run("real_run_leaves_a_tenant_imaged_runtimeversion_alone", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		if err := os.WriteFile(envConfigPath, append(existing, []byte("runtimeimage: ghcr.io/sophium/team-devops:1.0.76\n")...), 0o644); err != nil {
			t.Fatalf("write env config: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "helm", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm")...)

		result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		terraform := readPinnedFile(t, setup.Cwd, "terraform-team/dev/main.tf")
		if !strings.Contains(terraform, "?ref=v1.0.175") {
			t.Fatalf("erun terraform ref not re-pinned:\n%s", terraform)
		}
		after, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config after: %v", err)
		}
		if !strings.Contains(string(after), "runtimeversion: 1.0.0") {
			t.Fatalf("a tenant-imaged env's runtimeversion must be left alone:\n%s", after)
		}
	})

	// The reported bug, half one: a sourceless runtime env (no
	// MountSource, no repopath at all — the shape the backend's provisioning
	// Jobs seed) has no local checkout of its own, and with no sibling
	// environment of the tenant checked out here either, pin must refuse
	// cleanly by naming the environment and the tenant rather than falling
	// back to guessing at the caller's unrelated working directory.
	t.Run("refuses_a_sourceless_runtime_env_with_no_tenant_checkout", func(t *testing.T) {
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
	})

	// The reported bug, half one continued: every environment of a
	// tenant shares one repo, so when a sibling environment ("dev", a
	// local-agent env) does have a checkout on this machine, the sourceless
	// runtime env's re-pin resolves to it — deliberately, not by falling back
	// to whatever the caller's cwd happens to be.
	t.Run("resolves_a_sourceless_runtime_envs_root_from_a_sibling_checkout", func(t *testing.T) {
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
	})

	// The reported bug, half two: when no known checkout exists at all
	// and pin falls back to the caller's own working directory, the plan must
	// say so, name that tree's HEAD, and warn when the tree's own pins disagree
	// with what the environment has deployed — so the plan is never mistaken
	// for a statement about the environment's real drift.
	t.Run("cwd_fallback_names_the_head_and_warns_when_stale", func(t *testing.T) {
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
	})

	// The reported gap: dns01_webhook_image, set directly in a tenant's own
	// terraform variables, is an erun-published image reference just like the
	// module ref above it, and pin's dry-run must name it as a site to move
	// rather than reporting a plan that looks complete while leaving it behind.
	t.Run("dry_run_reports_a_stale_dns01_webhook_image_reference", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedDns01WebhookImageDrift(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))

		result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "pin/dry_run_reports_a_stale_dns01_webhook_image_reference", normalize.Apply(result.Combined))
	})

	// The real run must actually move it, not just name it.
	t.Run("real_run_pins_the_dns01_webhook_image_reference", func(t *testing.T) {
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
	})

	// The reported bug, for real: a runtimechart naming erun's own stock
	// chart is versioned on erun's own release line, just like runtimeversion
	// and a stated runtimeimage — a re-pin's dry-run plan must name it as a
	// site to move rather than reporting a plan that looks complete while
	// leaving it behind.
	t.Run("dry_run_reports_a_stale_runtimechart_reference", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedUnreachableRuntimeRegistry(t, filepath.Join(setup.ConfigHome, "erun"))
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		if err := os.WriteFile(envConfigPath, append(existing, []byte("runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.201\n")...), 0o644); err != nil {
			t.Fatalf("write env config: %v", err)
		}

		result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "pin/dry_run_reports_a_stale_runtimechart_reference", normalize.Apply(result.Combined))
	})

	// The whole coordinate, applied for real: a stated-stock runtimeimage and
	// runtimechart move to the target alongside runtimeversion, in the one
	// write that realigns the environment's own config. Before this fix, only
	// runtimeversion moved — runtimechart (this report) and a stated
	// runtimeimage (a latent instance of the same gap, found while fixing
	// this) were both left behind.
	t.Run("real_run_pins_runtimeversion_runtimeimage_and_runtimechart_together", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedUnreachableRuntimeRegistry(t, filepath.Join(setup.ConfigHome, "erun"))
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		body := string(existing) +
			"runtimeimage: ghcr.io/sophium/erun-devops:1.0.201\n" +
			"runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.201\n"
		if err := os.WriteFile(envConfigPath, []byte(body), 0o644); err != nil {
			t.Fatalf("write env config: %v", err)
		}

		result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		after, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config after: %v", err)
		}
		for _, want := range []string{
			"runtimeversion: 1.0.175",
			"runtimeimage: ghcr.io/sophium/erun-devops:1.0.175",
			"runtimechart: oci://ghcr.io/sophium/charts/erun-devops:1.0.175",
		} {
			if !strings.Contains(string(after), want) {
				t.Fatalf("expected %q in the env config after pinning, got:\n%s", want, after)
			}
		}
	})

	// A tenant's own runtimechart line is a real, deliberate configuration —
	// --runtime-chart exists precisely so the chart can be versioned
	// separately from the image and the erun release. A re-pin must leave it
	// exactly as stated, even while the rest of the coordinate moves.
	t.Run("real_run_leaves_a_tenant_own_runtimechart_line_unchanged", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedUnreachableRuntimeRegistry(t, filepath.Join(setup.ConfigHome, "erun"))
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		if err := os.WriteFile(envConfigPath, append(existing, []byte("runtimechart: oci://ghcr.io/sophium/charts/team-devops:1.0.76\n")...), 0o644); err != nil {
			t.Fatalf("write env config: %v", err)
		}

		result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		after, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config after: %v", err)
		}
		if !strings.Contains(string(after), "runtimechart: oci://ghcr.io/sophium/charts/team-devops:1.0.76") {
			t.Fatalf("a tenant's own runtimechart line must be left alone, got:\n%s", after)
		}
		if !strings.Contains(string(after), "runtimeversion: 1.0.175") {
			t.Fatalf("runtimeversion should still move, got:\n%s", after)
		}
	})

	// Discovery answers "what can I pin to" from the registry, so choosing a
	// version is recognition rather than recall. Served locally: a scenario that
	// asks a real registry measures the host.
	t.Run("list_reports_the_published_versions", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"next":"","results":[{"name":"1.0.174"},{"name":"1.0.173"},{"name":"latest"}]}`)
		}))
		defer server.Close()
		writeRuntimeRegistryConfig(t, setup, "runtimeregistry:\n  namespace: acme\n  repository: erun-devops\n  baseurl: "+server.URL+"\n")

		result := erun.Run(t, []string{"pin", "team", "dev", "--list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{"latest stable: 1.0.174", "1.0.173"} {
			if !strings.Contains(result.Combined, want) {
				t.Fatalf("expected %q in the listing:\n%s", want, result.Combined)
			}
		}
	})

	// The reported nit: `erun pin --list` is the literal example in this
	// command's own --help, and it reads erun's own published registry, which
	// is the same for every tenant — it must work on a host with no default
	// tenant configured at all, not just when a tenant happens to be seeded.
	t.Run("list_works_without_a_default_tenant_configured", func(t *testing.T) {
		setup := env.New(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"next":"","results":[{"name":"1.0.174"},{"name":"1.0.173"},{"name":"latest"}]}`)
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
		for _, want := range []string{"latest stable: 1.0.174", "1.0.173"} {
			if !strings.Contains(result.Combined, want) {
				t.Fatalf("expected %q in the listing:\n%s", want, result.Combined)
			}
		}
	})

	// Pinning to a version the registry does not carry produces a tree that only
	// fails much later, at a terraform init or a chart pull. It is refused here,
	// where the cause is still visible.
	t.Run("refuses_a_version_the_registry_does_not_carry", func(t *testing.T) {
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
	})

	// The whole motion, for real: every reference moves, the env's own runtime
	// version moves with them, re-running is a no-op, and the version left behind
	// is recoverable in one step.
	t.Run("real_run_pins_every_reference_then_reverts", func(t *testing.T) {
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

		// Idempotent: the second run has nothing left to do.
		again := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.174"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if again.ExitCode != 0 {
			t.Fatalf("exit %d: %s", again.ExitCode, again.Combined)
		}
		if !strings.Contains(again.Combined, "already pinned") {
			t.Fatalf("a re-run must report the no-op:\n%s", again.Combined)
		}

		// And the version left behind is one motion away.
		revert := erun.Run(t, []string{"pin", "team", "dev", "--revert"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if revert.ExitCode != 0 {
			t.Fatalf("exit %d: %s", revert.ExitCode, revert.Combined)
		}
		reverted := readPinnedFile(t, setup.Cwd, "terraform-team/dev/main.tf")
		if strings.Contains(reverted, "?ref=v1.0.174") {
			t.Fatalf("revert did not move the terraform ref back:\n%s", reverted)
		}
	})

	// Reverting needs somewhere to go. Asking for one before any re-pin has
	// happened must say so rather than silently doing nothing.
	t.Run("revert_without_a_recorded_pin_says_so", func(t *testing.T) {
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
	})
}

func readPinnedFile(t *testing.T, root, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(data)
}
