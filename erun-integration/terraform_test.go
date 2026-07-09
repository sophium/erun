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
