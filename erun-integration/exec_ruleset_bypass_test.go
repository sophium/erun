package integration

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testExecReconcileBypassHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "reconcile-bypass", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/reconcile_bypass_help", normalize.Apply(result.Combined))
}

func testExecReconcileBypassDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"exec", "reconcile-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--ruleset-id", "11081432", "--target-branch", "main",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/reconcile_bypass_dry_run", normalize.Apply(result.Combined))
}

func testExecReconcileBypassDryRunWithSince(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"exec", "reconcile-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--ruleset-id", "11081432", "--target-branch", "main", "--since", "week",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/reconcile_bypass_dry_run_with_since", normalize.Apply(result.Combined))
}

func testExecReconcileBypassDryRunMissingRulesetIdTracesThenRefuses(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"exec", "reconcile-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--target-branch", "main",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a missing ruleset id, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/reconcile_bypass_dry_run_missing_ruleset_id_traces_then_refuses", normalize.Apply(result.Combined))
}

func testExecReconcileBypassRealRunFailsCleanlyWithoutAToken(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"exec", "reconcile-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--ruleset-id", "11081432", "--target-branch", "main",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit with no token available, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/reconcile_bypass_real_run_fails_cleanly_without_a_token", normalize.Apply(result.Combined))
}

// TestExecRulesetBypass drives the ruleset-bypass commands against stub GitHub and platform servers.
func TestExecRulesetBypass(t *testing.T) {
	t.Parallel()
	t.Run("reconcile_bypass_real_run_accounts_for_a_gated_merge_and_a_release_push", testExecRulesetBypassReconcileBypassRealRunAccountsForAGatedMergeAndAReleasePush)
	t.Run("reconcile_bypass_real_run_flags_a_bypass_by_an_unexpected_identity", testExecRulesetBypassReconcileBypassRealRunFlagsABypassByAnUnexpectedIdentity)
	t.Run("reconcile_bypass_real_run_surfaces_a_github_failure_response", testExecRulesetBypassReconcileBypassRealRunSurfacesAGithubFailureResponse)
	t.Run("plan_ruleset_bypass_help", testExecRulesetBypassPlanRulesetBypassHelp)
	t.Run("plan_ruleset_bypass_dry_run", testExecRulesetBypassPlanRulesetBypassDryRun)
	t.Run("plan_ruleset_bypass_dry_run_missing_queue_actor_traces_then_refuses", testExecRulesetBypassPlanRulesetBypassDryRunMissingQueueActorTracesThenRefuses)
	t.Run("plan_ruleset_bypass_dry_run_rejects_an_unsupported_actor_type", testExecRulesetBypassPlanRulesetBypassDryRunRejectsAnUnsupportedActorType)
	t.Run("plan_ruleset_bypass_real_run_writes_both_stages_and_the_rollback", testExecRulesetBypassPlanRulesetBypassRealRunWritesBothStagesAndTheRollback)
	t.Run("plan_ruleset_bypass_real_run_refuses_when_the_queue_identity_cannot_push", testExecRulesetBypassPlanRulesetBypassRealRunRefusesWhenTheQueueIdentityCannotPush)
	t.Run("plan_ruleset_bypass_real_run_refuses_when_the_ruleset_does_not_govern_the_branch", testExecRulesetBypassPlanRulesetBypassRealRunRefusesWhenTheRulesetDoesNotGovernTheBranch)
	t.Run("plan_ruleset_bypass_real_run_refuses_when_github_hides_the_bypass_actors", testExecRulesetBypassPlanRulesetBypassRealRunRefusesWhenGithubHidesTheBypassActors)
}

func testExecRulesetBypassReconcileBypassRealRunAccountsForAGatedMergeAndAReleasePush(t *testing.T) {
	setup := env.New(t)
	github := githubRulesetStubServer(t, githubRulesetStubOptions{})
	platform := successfulBuildsStubServer(t)
	platformAlias(t, setup, platform)
	envVars := append(setup.Env(), githubStubEnv(github)...)
	result := erun.Run(t, []string{
		"exec", "reconcile-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--ruleset-id", "11081432", "--target-branch", "main",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit while one push is unaccounted for, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/reconcile_bypass_real_run_accounts_for_a_gated_merge_and_a_release_push",
		normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>"), stubServerRule(platform, "<PLATFORM_API>")))
}

func testExecRulesetBypassReconcileBypassRealRunFlagsABypassByAnUnexpectedIdentity(t *testing.T) {
	// Naming the expected identity flags a push by anyone else as UNEXPECTED_ACTOR.
	setup := env.New(t)
	github := githubRulesetStubServer(t, githubRulesetStubOptions{})
	platform := successfulBuildsStubServer(t)
	platformAlias(t, setup, platform)
	envVars := append(setup.Env(), githubStubEnv(github)...)
	result := erun.Run(t, []string{
		"exec", "reconcile-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--ruleset-id", "11081432", "--target-branch", "main",
		"--expected-actor", "erun-merge-queue",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an unexpected bypass identity, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/reconcile_bypass_real_run_flags_a_bypass_by_an_unexpected_identity",
		normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>"), stubServerRule(platform, "<PLATFORM_API>")))
}

func testExecRulesetBypassReconcileBypassRealRunSurfacesAGithubFailureResponse(t *testing.T) {
	setup := env.New(t)
	github := githubRulesetStubServer(t, githubRulesetStubOptions{RuleSuitesStatus: http.StatusForbidden})
	platform := successfulBuildsStubServer(t)
	platformAlias(t, setup, platform)
	envVars := append(setup.Env(), githubStubEnv(github)...)
	result := erun.Run(t, []string{
		"exec", "reconcile-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--ruleset-id", "11081432", "--target-branch", "main",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when github refuses the ledger read, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/reconcile_bypass_real_run_surfaces_a_github_failure_response",
		normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>"), stubServerRule(platform, "<PLATFORM_API>")))
}

func testExecRulesetBypassPlanRulesetBypassHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "plan-ruleset-bypass", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/plan_ruleset_bypass_help", normalize.Apply(result.Combined))
}

func testExecRulesetBypassPlanRulesetBypassDryRun(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "plan-ruleset-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--ruleset-id", "11081432", "--target-branch", "main",
		"--queue-actor", "erun-merge-queue", "--out-dir", "plan",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if _, err := os.Stat(filepath.Join(setup.Cwd, "plan")); !os.IsNotExist(err) {
		t.Fatalf("dry run created the output directory: %v", err)
	}
	golden.Equal(t, "exec/plan_ruleset_bypass_dry_run", normalize.Apply(result.Combined))
}

func testExecRulesetBypassPlanRulesetBypassDryRunMissingQueueActorTracesThenRefuses(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "plan-ruleset-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--ruleset-id", "11081432",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a missing queue actor, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/plan_ruleset_bypass_dry_run_missing_queue_actor_traces_then_refuses", normalize.Apply(result.Combined))
}

func testExecRulesetBypassPlanRulesetBypassDryRunRejectsAnUnsupportedActorType(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "plan-ruleset-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--ruleset-id", "11081432", "--queue-actor", "erun-merge-queue",
		"--queue-actor-type", "MachineAccount",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an unsupported actor type, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/plan_ruleset_bypass_dry_run_rejects_an_unsupported_actor_type", normalize.Apply(result.Combined))
}

func testExecRulesetBypassPlanRulesetBypassRealRunWritesBothStagesAndTheRollback(t *testing.T) {
	setup := env.New(t)
	github := githubRulesetStubServer(t, githubRulesetStubOptions{})
	envVars := append(setup.Env(), githubStubEnv(github)...)
	result := erun.Run(t, []string{
		"exec", "plan-ruleset-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--ruleset-id", "11081432", "--target-branch", "main",
		"--queue-actor", "erun-merge-queue", "--out-dir", "plan",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/plan_ruleset_bypass_real_run_writes_both_stages_and_the_rollback",
		normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>")))
	// The payload files are written outside the captured streams.
	for _, stage := range []string{"rollback", "stage1", "stage2"} {
		golden.Equal(t, "exec/plan_ruleset_bypass_payload_"+stage,
			normalize.Apply(mustReadFile(t, filepath.Join(setup.Cwd, "plan", "ruleset-11081432-"+stage+".json"))))
	}
}

func testExecRulesetBypassPlanRulesetBypassRealRunRefusesWhenTheQueueIdentityCannotPush(t *testing.T) {
	setup := env.New(t)
	github := githubRulesetStubServer(t, githubRulesetStubOptions{QueuePermission: "read"})
	envVars := append(setup.Env(), githubStubEnv(github)...)
	result := erun.Run(t, []string{
		"exec", "plan-ruleset-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--ruleset-id", "11081432", "--queue-actor", "erun-merge-queue",
		"--out-dir", "plan",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an identity that cannot push, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/plan_ruleset_bypass_real_run_refuses_when_the_queue_identity_cannot_push",
		normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>")))
}

func testExecRulesetBypassPlanRulesetBypassRealRunRefusesWhenTheRulesetDoesNotGovernTheBranch(t *testing.T) {
	setup := env.New(t)
	github := githubRulesetStubServer(t, githubRulesetStubOptions{RefInclude: `"refs/heads/release/*"`})
	envVars := append(setup.Env(), githubStubEnv(github)...)
	result := erun.Run(t, []string{
		"exec", "plan-ruleset-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--ruleset-id", "11081432", "--target-branch", "main",
		"--queue-actor", "erun-merge-queue", "--out-dir", "plan",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a ruleset that does not govern the branch, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/plan_ruleset_bypass_real_run_refuses_when_the_ruleset_does_not_govern_the_branch",
		normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>")))
}

func testExecRulesetBypassPlanRulesetBypassRealRunRefusesWhenGithubHidesTheBypassActors(t *testing.T) {
	// GitHub hides bypass_actors from tokens without write access; planning without them would drop every actor.
	setup := env.New(t)
	github := githubRulesetStubServer(t, githubRulesetStubOptions{HideBypassActors: true})
	envVars := append(setup.Env(), githubStubEnv(github)...)
	result := erun.Run(t, []string{
		"exec", "plan-ruleset-bypass",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--ruleset-id", "11081432", "--queue-actor", "erun-merge-queue",
		"--out-dir", "plan",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when bypass actors are hidden, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/plan_ruleset_bypass_real_run_refuses_when_github_hides_the_bypass_actors",
		normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>")))
}
