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
	t.Parallel()
	t.Run("help", testTerraformHelp)
	t.Run("help_apply", testTerraformHelpApply)
	t.Run("help_init", testTerraformHelpInit)
	t.Run("refuses_host_environment", testTerraformRefusesHostEnvironment)
	t.Run("runtime_environment_dispatches_from_host", testTerraformRuntimeEnvironmentDispatchesFromHost)
	t.Run("runtime_environment_allowed_in_pod", testTerraformRuntimeEnvironmentAllowedInPod)
	t.Run("runtime_environment_dispatches_from_a_different_environments_pod", testTerraformRuntimeEnvironmentDispatchesFromADifferentEnvironmentsPod)
	t.Run("remote_agent_environment_dispatches_from_host", testTerraformRemoteAgentEnvironmentDispatchesFromHost)
	t.Run("apply_dispatch_from_host_without_confirmation_refuses", testTerraformApplyDispatchFromHostWithoutConfirmationRefuses)
	t.Run("apply_dispatch_dry_run_with_confirmation", testTerraformApplyDispatchDryRunWithConfirmation)
	t.Run("plan_dispatch_real_run_via_stub", testTerraformPlanDispatchRealRunViaStub)
	t.Run("plan_dispatch_real_run_surfaces_remote_failure", testTerraformPlanDispatchRealRunSurfacesRemoteFailure)
	t.Run("local_agent_environment_still_resolves_on_host", testTerraformLocalAgentEnvironmentStillResolvesOnHost)
	t.Run("init_dry_run", testTerraformInitDryRun)
	t.Run("init_dry_run_lockfile_readonly", testTerraformInitDryRunLockfileReadonly)
	t.Run("init_real_run_via_stub", testTerraformInitRealRunViaStub)
	t.Run("apply_dry_run_no_backend_warns", testTerraformApplyDryRunNoBackendWarns)
	t.Run("apply_dry_run", testTerraformApplyDryRun)
	t.Run("apply_dry_run_default_scope", testTerraformApplyDryRunDefaultScope)
	t.Run("apply_dry_run_no_tfvars", testTerraformApplyDryRunNoTfvars)
	t.Run("apply_dry_run_with_cloudflare_token", testTerraformApplyDryRunWithCloudflareToken)
	t.Run("apply_dry_run_under_devops", testTerraformApplyDryRunUnderDevops)
	t.Run("apply_dry_run_legacy_state_warning", testTerraformApplyDryRunLegacyStateWarning)
	t.Run("plan_dry_run", testTerraformPlanDryRun)
	t.Run("destroy_dry_run", testTerraformDestroyDryRun)
	t.Run("plan_dry_run_configured_path", testTerraformPlanDryRunConfiguredPath)
	t.Run("configured_path_missing_root", testTerraformConfiguredPathMissingRoot)
	t.Run("missing_root", testTerraformMissingRoot)
	t.Run("apply_dry_run_repo_path_env", testTerraformApplyDryRunRepoPathEnv)
	t.Run("not_in_git_without_repo_path", testTerraformNotInGitWithoutRepoPath)
	t.Run("apply_real_run_via_stub", testTerraformApplyRealRunViaStub)
	t.Run("apply_dry_run_rfc2136_injects_tsig_secret", testTerraformApplyDryRunRfc2136InjectsTsigSecret)
	t.Run("apply_dry_run_rfc2136_missing_secret_fails_up_front", testTerraformApplyDryRunRfc2136MissingSecretFailsUpFront)
	t.Run("apply_dry_run_rfc2136_forbidden_secret_names_permission_problem", testTerraformApplyDryRunRfc2136ForbiddenSecretNamesPermissionProblem)
	t.Run("apply_confirm_mismatch", testTerraformApplyConfirmMismatch)
}

func testTerraformHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"terraform", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "terraform/help", normalize.Apply(result.Combined))
}

func testTerraformHelpApply(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"terraform", "apply", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "terraform/help_apply", normalize.Apply(result.Combined))
}

func testTerraformHelpInit(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"terraform", "init", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "terraform/help_init", normalize.Apply(result.Combined))
}

func testTerraformRefusesHostEnvironment(t *testing.T) {
	setup := env.New(t)
	fixture.SeedHostTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
	result := erun.Run(t, []string{"terraform", "init", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "terraform/refuses_host_environment", normalize.Apply(result.Combined))
}

func testTerraformRuntimeEnvironmentDispatchesFromHost(t *testing.T) {
	// Runtime state lives on the pod's PVC, so a host invocation dispatches into the pod
	// instead of resolving the host's empty home. init is read-only, so no confirm gate.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "team", "prod")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "prod")
	result := erun.Run(t, []string{"terraform", "init", "team", "prod", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "terraform/runtime_environment_dispatches_from_host", normalize.Apply(result.Combined))
}

func testTerraformRuntimeEnvironmentAllowedInPod(t *testing.T) {
	// Inside its own pod (matching injected ERUN_TENANT/ERUN_ENVIRONMENT) it resolves locally.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "team", "prod")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "prod")
	envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=prod")
	result := erun.Run(t, []string{"terraform", "init", "team", "prod", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "terraform/runtime_environment_allowed_in_pod", normalize.Apply(result.Combined))
}

func testTerraformRuntimeEnvironmentDispatchesFromADifferentEnvironmentsPod(t *testing.T) {
	// The injected identity must match the target exactly; otherwise it dispatches.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "team", "prod")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "prod")
	envVars := append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev")
	result := erun.Run(t, []string{"terraform", "init", "team", "prod", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "terraform/runtime_environment_dispatches_from_a_different_environments_pod", normalize.Apply(result.Combined))
}

func testTerraformRemoteAgentEnvironmentDispatchesFromHost(t *testing.T) {
	// Remote-agent state is PVC-backed too, so dispatch covers it, not just runtime.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
	result := erun.Run(t, []string{"terraform", "init", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "terraform/remote_agent_environment_dispatches_from_host", normalize.Apply(result.Combined))
}

func testTerraformApplyDispatchFromHostWithoutConfirmationRefuses(t *testing.T) {
	// Mutating dispatch is opt-in: with no --confirm-environment and no TTY it refuses.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "team", "prod")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "prod")
	result := erun.Run(t, []string{"terraform", "apply", "team", "prod"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an unconfirmed dispatched apply, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "terraform/apply_dispatch_from_host_without_confirmation_refuses", normalize.Apply(result.Combined))
}

func testTerraformApplyDispatchDryRunWithConfirmation(t *testing.T) {
	// The dispatched command embeds --confirm-environment so the in-pod run never re-prompts.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "team", "prod")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "prod")
	result := erun.Run(t, []string{"terraform", "apply", "team", "prod", "--confirm-environment", "prod", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "terraform/apply_dispatch_dry_run_with_confirmation", normalize.Apply(result.Combined))
}

func testTerraformPlanDispatchRealRunViaStub(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "team", "prod")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "prod")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "Plan: 1 to add, 0 to change, 0 to destroy.")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"terraform", "plan", "team", "prod"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "terraform/plan_dispatch_real_run_via_stub", normalize.Apply(result.Combined))
}

func testTerraformPlanDispatchRealRunSurfacesRemoteFailure(t *testing.T) {
	// Orchestrators rely on a remote failure surfacing as a non-zero exit.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "team", "prod")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "prod")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
		Stderr:   "Error: no valid credential sources found",
		ExitCode: 1,
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"terraform", "plan", "team", "prod"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the dispatched command fails, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "terraform/plan_dispatch_real_run_surfaces_remote_failure", normalize.Apply(result.Combined))
}

func testTerraformLocalAgentEnvironmentStillResolvesOnHost(t *testing.T) {
	// Local-agent state is on this host, so it must resolve directly, not dispatch.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
	result := erun.Run(t, []string{"terraform", "init", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "terraform/local_agent_environment_still_resolves_on_host", normalize.Apply(result.Combined))
}

func testTerraformInitDryRun(t *testing.T) {
	// init locks providers for both deploy arches so one committed lock works everywhere.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
	result := erun.Run(t, []string{"terraform", "init", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "terraform/init_dry_run", normalize.Apply(result.Combined))
}

func testTerraformInitDryRunLockfileReadonly(t *testing.T) {
	// A committed lock means -lockfile=readonly and no `providers lock`.
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
}

func testTerraformInitRealRunViaStub(t *testing.T) {
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
}

func testTerraformApplyDryRunNoBackendWarns(t *testing.T) {
	// Without a `backend "local"` block, state would land in the read-only tree.
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
}

func testTerraformApplyDryRun(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
	result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "terraform/apply_dry_run", normalize.Apply(result.Combined))
}

func testTerraformApplyDryRunDefaultScope(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
	result := erun.Run(t, []string{"terraform", "apply", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "terraform/apply_dry_run_default_scope", normalize.Apply(result.Combined))
}

func testTerraformApplyDryRunNoTfvars(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
	if err := os.Remove(setup.Cwd + "/terraform-team/dev/dev.tfvars"); err != nil {
		t.Fatalf("remove tfvars: %v", err)
	}
	result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "terraform/apply_dry_run_no_tfvars", normalize.Apply(result.Combined))
}

func testTerraformApplyDryRunWithCloudflareToken(t *testing.T) {
	// The Cloudflare token rides in the environment, not argv.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
	envVars := append(setup.Env(), "CLOUDFLARE_API_TOKEN=cf-secret-token")
	result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "terraform/apply_dry_run_with_cloudflare_token", normalize.Apply(result.Combined))
}

func testTerraformApplyDryRunUnderDevops(t *testing.T) {
	// The sourceless-pod layout: terraform-<tenant>/ under <tenant>-devops/.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRootAt(t, filepath.Join(setup.Cwd, "team-devops", "terraform-team"), "dev")
	result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "terraform/apply_dry_run_under_devops", normalize.Apply(result.Combined))
}

func testTerraformApplyDryRunLegacyStateWarning(t *testing.T) {
	// Old in-tree state is ignored but warned about so it is not silently orphaned.
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
}

func testTerraformPlanDryRun(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
	result := erun.Run(t, []string{"terraform", "plan", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "terraform/plan_dry_run", normalize.Apply(result.Combined))
}

func testTerraformDestroyDryRun(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
	result := erun.Run(t, []string{"terraform", "destroy", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "terraform/destroy_dry_run", normalize.Apply(result.Combined))
}

func testTerraformPlanDryRunConfiguredPath(t *testing.T) {
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
}

func testTerraformConfiguredPathMissingRoot(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectPathsConfig(t, setup, "", "", "", "infra/tf", "")
	result := erun.Run(t, []string{"terraform", "plan", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a missing configured terraform root, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "terraform/configured_path_missing_root", normalize.Apply(result.Combined))
}

func testTerraformMissingRoot(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without a terraform root, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "terraform/missing_root", normalize.Apply(result.Combined))
}

func testTerraformApplyDryRunRepoPathEnv(t *testing.T) {
	// A sourceless runtime pod has no .git; root resolution comes from ERUN_REPO_PATH.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	repoDir := filepath.Join(setup.Home, "git", "team")
	fixture.SeedTerraformEnvRootAt(t, filepath.Join(repoDir, "terraform-team"), "dev")
	envVars := append(setup.Env(), "ERUN_REPO_PATH="+repoDir)
	result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// The snapshot normalizes both paths to <TMP>, so assert the raw repo-dir path.
	if terraformDir := filepath.Join(repoDir, "terraform-team", "dev"); !strings.Contains(result.Combined, terraformDir) {
		t.Fatalf("expected terraform to resolve %s from ERUN_REPO_PATH, got:\n%s", terraformDir, result.Combined)
	}
	golden.Equal(t, "terraform/apply_dry_run_repo_path_env", normalize.Apply(result.Combined))
}

func testTerraformNotInGitWithoutRepoPath(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without a git repo or ERUN_REPO_PATH, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "terraform/not_in_git_without_repo_path", normalize.Apply(result.Combined))
}

func testTerraformApplyRealRunViaStub(t *testing.T) {
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
}

func testTerraformApplyDryRunRfc2136InjectsTsigSecret(t *testing.T) {
	// powerdns-rfc2136 injects the TSIG Secret as TF_VAR_rfc2136_tsig_secret; the Secret name
	// derives from issuer_name.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTerraformEnvRoot(t, setup, "team", "dev")
	tfvars := filepath.Join(setup.Cwd, "terraform-team", "dev", "dev.tfvars")
	if err := os.WriteFile(tfvars, []byte("base_domain = \"erunpaas.com\"\ndns01_provider = \"powerdns-rfc2136\"\nenv_label = \"team-dev\"\n"), 0o644); err != nil {
		t.Fatalf("write tfvars: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: "c3VwZXJzZWNyZXQ="})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"terraform", "apply", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "terraform/apply_dry_run_rfc2136_injects_tsig_secret", normalize.Apply(result.Combined))
}

func testTerraformApplyDryRunRfc2136MissingSecretFailsUpFront(t *testing.T) {
	// terraform would fail mid-plan with an incomplete "N to add" summary, so erun must fail
	// before running anything.
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
}

func testTerraformApplyDryRunRfc2136ForbiddenSecretNamesPermissionProblem(t *testing.T) {
	// Forbidden needs a different remedy than NotFound; kubectl's stderr names the principal.
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
		Stderr:   `Error from server (Forbidden): secrets "erun-cloudflare-rfc2136-tsig" is forbidden: User "system:serviceaccount:team-dev:default" cannot get resource "secrets" in API group "" in the namespace "team-dev"`,
		ExitCode: 1,
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"terraform", "plan", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an RBAC-denied RFC2136 TSIG secret read, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "terraform/apply_dry_run_rfc2136_forbidden_secret_names_permission_problem", normalize.Apply(result.Combined))
}

func testTerraformApplyConfirmMismatch(t *testing.T) {
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
}
