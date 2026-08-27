package integration

import (
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

func TestTerraform(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"terraform", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "terraform/help", normalize.Apply(result.Combined))
	})

	t.Run("help_apply", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"terraform", "apply", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "terraform/help_apply", normalize.Apply(result.Combined))
	})

	t.Run("help_init", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"terraform", "init", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "terraform/help_init", normalize.Apply(result.Combined))
	})

	t.Run("refuses_host_environment", func(t *testing.T) {
		// A host env has no pod and no cluster at all, so terraform must refuse
		// it by name instead of resolving a plan directory it cannot apply.
		setup := env.New(t)
		fixture.SeedHostTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		result := erun.Run(t, []string{"terraform", "init", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "terraform/refuses_host_environment", normalize.Apply(result.Combined))
	})

	t.Run("init_dry_run", func(t *testing.T) {
		// init is its own operation now (apply/plan/destroy no longer init). With no
		// committed lock yet, init generates one and records provider hashes for both
		// deploy arches via `providers lock`, so a single committed lock initializes
		// on any env's architecture.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		result := erun.Run(t, []string{"terraform", "init", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "terraform/init_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("init_dry_run_lockfile_readonly", func(t *testing.T) {
		// A committed .terraform.lock.hcl in the (read-only) playbook tree pins
		// providers, so init runs -lockfile=readonly and only refreshes the PVC
		// provider cache — it never rewrites the tree and skips `providers lock`.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		lock := filepath.Join(setup.Cwd, "terraform-team", "dev", ".terraform.lock.hcl")
		if err := os.WriteFile(lock, []byte("# pinned providers\n"), 0o644); err != nil {
			t.Fatalf("write lock: %v", err)
		}
		result := erun.Run(t, []string{"terraform", "init", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "terraform/init_dry_run_lockfile_readonly", normalize.Apply(result.Combined))
	})

	t.Run("init_real_run_via_stub", func(t *testing.T) {
		// The real (non-dry-run) init path locks the writability pre-check, the init
		// step, the cross-arch `providers lock` step, and the success line.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "terraform", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "terraform")...)
		result := erun.Run(t, []string{"terraform", "init", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "terraform/init_real_run_via_stub", normalize.Apply(result.Combined))
	})

	t.Run("apply_dry_run_no_backend_warns", func(t *testing.T) {
		// Without a `backend "local"` block, terraform's -backend-config=path override
		// doesn't persist, so state would land in ./terraform.tfstate inside the
		// (read-only) tree. erun warns so the operator adds the block before apply
		// fails on a read-only tree.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		common := filepath.Join(setup.Cwd, "terraform-team", "common.tf")
		if err := os.WriteFile(common, []byte("terraform {\n  required_version = \">= 1.3\"\n}\n"), 0o644); err != nil {
			t.Fatalf("rewrite common.tf: %v", err)
		}
		result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "terraform/apply_dry_run_no_backend_warns", normalize.Apply(result.Combined))
	})

	t.Run("apply_dry_run", func(t *testing.T) {
		// apply's full plan runs behind a type-the-env-name confirm gate and
		// performs no side effects in dry-run.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "terraform/apply_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("apply_dry_run_default_scope", func(t *testing.T) {
		// With no tenant/environment args, apply resolves the configured default scope.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		result := erun.Run(t, []string{"terraform", "apply", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "terraform/apply_dry_run_default_scope", normalize.Apply(result.Combined))
	})

	t.Run("apply_dry_run_no_tfvars", func(t *testing.T) {
		// A missing <environment>.tfvars makes plan run without it rather than
		// pass a path that doesn't exist.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		if err := os.Remove(setup.Cwd + "/terraform-team/dev/dev.tfvars"); err != nil {
			t.Fatalf("remove tfvars: %v", err)
		}
		result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "terraform/apply_dry_run_no_tfvars", normalize.Apply(result.Combined))
	})

	t.Run("apply_dry_run_with_cloudflare_token", func(t *testing.T) {
		// A Cloudflare token is forwarded to terraform but its value never appears
		// in the trace — it rides in the environment, not argv.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		envVars := append(setup.Env(), "CLOUDFLARE_API_TOKEN=cf-secret-token")
		result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "terraform/apply_dry_run_with_cloudflare_token", normalize.Apply(result.Combined))
	})

	t.Run("apply_dry_run_under_devops", func(t *testing.T) {
		// A tenant that keeps its whole devops footprint under <tenant>-devops/
		// (docker/, k8s/, terraform-<tenant>/) resolves via the -devops convention
		// when no repo-root terraform-<tenant>/ exists — the sourceless-pod layout.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRootAt(t, filepath.Join(setup.Cwd, "team-devops", "terraform-team"), "dev")
		result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "terraform/apply_dry_run_under_devops", normalize.Apply(result.Combined))
	})

	t.Run("apply_dry_run_legacy_state_warning", func(t *testing.T) {
		// A pre-relocation terraform.tfstate left in the tree is ignored (state now
		// lives on the PVC) and surfaced as a warning so old state isn't orphaned silently.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		legacy := filepath.Join(setup.Cwd, "terraform-team", "dev", "terraform.tfstate")
		if err := os.WriteFile(legacy, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write legacy state: %v", err)
		}
		result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "terraform/apply_dry_run_legacy_state_warning", normalize.Apply(result.Combined))
	})

	t.Run("plan_dry_run", func(t *testing.T) {
		// plan is read-only: no fmt mutation and no confirm gate, unlike apply.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		result := erun.Run(t, []string{"terraform", "plan", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "terraform/plan_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("destroy_dry_run", func(t *testing.T) {
		// destroy is gated behind the same type-the-env-name confirm as apply.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		result := erun.Run(t, []string{"terraform", "destroy", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "terraform/destroy_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("plan_dry_run_configured_path", func(t *testing.T) {
		// A paths.terraform override in .erun/config.yaml relocates the Terraform
		// base: erun resolves <base>/<environment> (no terraform-<tenant> dir
		// exists) and traces the configured base as a decision line.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectPathsConfig(t, setup, "", "", "", "infra/tf", "")
		fixture.SeedTerraformEnvRootAt(t, filepath.Join(setup.Cwd, "infra", "tf"), "dev")
		result := erun.Run(t, []string{"terraform", "plan", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "terraform/plan_dry_run_configured_path", normalize.Apply(result.Combined))
	})

	t.Run("configured_path_missing_root", func(t *testing.T) {
		// A paths.terraform override with no resolvable <base>/<environment> dir
		// fails with an error naming the configured base, not the terraform-<tenant>
		// scaffold hint.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectPathsConfig(t, setup, "", "", "", "infra/tf", "")
		result := erun.Run(t, []string{"terraform", "plan", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a missing configured terraform root, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "terraform/configured_path_missing_root", normalize.Apply(result.Combined))
	})

	t.Run("missing_root", func(t *testing.T) {
		// With no terraform root present, fail with an actionable error pointing at
		// the blueprint skill, not an opaque stat error.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without a terraform root, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "terraform/missing_root", normalize.Apply(result.Combined))
	})

	t.Run("apply_dry_run_repo_path_env", func(t *testing.T) {
		// A sourceless runtime pod has no .git: the image-baked release tree is
		// surfaced at ERUN_REPO_PATH. Root resolution must come from that env var
		// (not the cwd .git walk), so terraform runs against
		// <ERUN_REPO_PATH>/terraform-team/dev even though neither the cwd nor the
		// repo dir is a git repository.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		repoDir := filepath.Join(setup.Home, "git", "team")
		fixture.SeedTerraformEnvRootAt(t, filepath.Join(repoDir, "terraform-team"), "dev")
		envVars := append(setup.Env(), "ERUN_REPO_PATH="+repoDir)
		result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// The resolved dir normalizes to <TMP>, so the snapshot alone can't prove
		// resolution came from ERUN_REPO_PATH rather than the cwd. Assert on the
		// un-normalized path (the masked-value case) to lock that the repo-dir
		// tree — distinct from the cwd — drove the plan.
		if terraformDir := filepath.Join(repoDir, "terraform-team", "dev"); !strings.Contains(result.Combined, terraformDir) {
			t.Fatalf("expected terraform to resolve %s from ERUN_REPO_PATH, got:\n%s", terraformDir, result.Combined)
		}
		golden.Equal(t, "terraform/apply_dry_run_repo_path_env", normalize.Apply(result.Combined))
	})

	t.Run("not_in_git_without_repo_path", func(t *testing.T) {
		// The laptop fallback is unchanged: with no .git up the tree and
		// ERUN_REPO_PATH unset, root resolution still fails with "cannot find git
		// project" before any terraform work.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without a git repo or ERUN_REPO_PATH, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "terraform/not_in_git_without_repo_path", normalize.Apply(result.Combined))
	})

	t.Run("apply_real_run_via_stub", func(t *testing.T) {
		// The real (non-dry-run) path locks the execution sequence, the confirm
		// gate, and the success line.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "terraform", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "terraform")...)
		result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--confirm-environment", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "terraform/apply_real_run_via_stub", normalize.Apply(result.Combined))
	})

	t.Run("apply_dry_run_rfc2136_injects_tsig_secret", func(t *testing.T) {
		// A resolved dns01_provider of "powerdns-rfc2136" makes erun read the
		// cluster-edge module's own materialized TSIG Secret back and inject it as
		// TF_VAR_rfc2136_tsig_secret, the same way it already injects the Cloudflare
		// token — so the operator never has to export it by hand. The Secret name is
		// derived from issuer_name (default "erun-cloudflare"), never hardcoded.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		tfvars := filepath.Join(setup.Cwd, "terraform-team", "dev", "dev.tfvars")
		if err := os.WriteFile(tfvars, []byte("base_domain = \"erunpaas.com\"\ndns01_provider = \"powerdns-rfc2136\"\nenv_label = \"team-dev\"\n"), 0o644); err != nil {
			t.Fatalf("write tfvars: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		// base64 of "supersecret", read back from data.tsig-secret.
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: "c3VwZXJzZWNyZXQ="})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "terraform/apply_dry_run_rfc2136_injects_tsig_secret", normalize.Apply(result.Combined))
	})

	t.Run("apply_dry_run_rfc2136_missing_secret_fails_up_front", func(t *testing.T) {
		// terraform itself would only fail this precondition mid-plan, after
		// printing a partial plan whose "N to add" summary omits every module it
		// never reached — reviewing that summary reviews an incomplete plan. erun
		// must fail before tracing or running any terraform command at all, naming
		// the Secret and key that are missing.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		tfvars := filepath.Join(setup.Cwd, "terraform-team", "dev", "dev.tfvars")
		if err := os.WriteFile(tfvars, []byte("base_domain = \"erunpaas.com\"\ndns01_provider = \"powerdns-rfc2136\"\nenv_label = \"team-dev\"\n"), 0o644); err != nil {
			t.Fatalf("write tfvars: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
			Stderr:   `Error from server (NotFound): secrets "erun-cloudflare-rfc2136-tsig" not found`,
			ExitCode: 1,
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"terraform", "plan", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a missing RFC2136 TSIG secret, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "terraform/apply_dry_run_rfc2136_missing_secret_fails_up_front", normalize.Apply(result.Combined))
	})

	t.Run("apply_confirm_mismatch", func(t *testing.T) {
		// A confirm value that doesn't match the target env aborts before the
		// mutating apply.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "terraform", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "terraform")...)
		result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--confirm-environment", "wrong"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit on confirmation mismatch, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "terraform/apply_confirm_mismatch", normalize.Apply(result.Combined))
	})
}
