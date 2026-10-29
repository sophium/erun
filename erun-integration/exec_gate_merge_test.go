package integration

import (
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testExecGateMergeHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "gate-merge", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/gate_merge_help", normalize.Apply(result.Combined))
}

func testExecGateMergeRefusesMissingSource(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "gate-merge", "--target", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit with no --source, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/gate_merge_refuses_missing_source", normalize.Apply(result.Combined))
}

func testExecGateMergeRefusesMissingTarget(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "feature/add-widget"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add widget\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without --target, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/gate_merge_refuses_missing_target", normalize.Apply(result.Combined))
}

func testExecGateMergeRefusesStdinMessageCountMismatch(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "a", "--source", "b", "--target", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add a\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a message-count mismatch, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/gate_merge_refuses_stdin_message_count_mismatch", normalize.Apply(result.Combined))
}

func testExecGateMergeDryRunMustNotReachTheNetwork(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "feature/add-widget", "--target", "main", "--remote", "nonexistent", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add widget\n"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/gate_merge_dry_run_must_not_reach_the_network", normalize.Apply(result.Combined))
}

func testExecGateMergeDryRunRefusesDirtyWorktree(t *testing.T) {
	// The dirty check runs under --dry-run and reads only tracked files.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	mustWriteFile(t, filepath.Join(setup.Cwd, "README.md"), "uncommitted change\n")
	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "feature/add-widget", "--target", "main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add widget\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit against a dirty worktree, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/gate_merge_dry_run_refuses_dirty_worktree", normalize.Apply(result.Combined))
}

func testExecGateMergeDryRunBatchTracesEachSource(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "feature/a", "--source", "feature/b", "--target", "main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "Add a\x00Add b"})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/gate_merge_dry_run_batch_traces_each_source", normalize.Apply(result.Combined))
}

func testExecGateMergeRefusesWhileTheEnvironmentIsHeldExclusively(t *testing.T) {
	// Concurrent gate-merges corrupt the shared worktree; off-pod (no ERUN_TENANT/ERUN_ENVIRONMENT) the check no-ops.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	// inEnvironment makes the lease verbs use the local store instead of an MCP edge.
	envVars := inEnvironment(append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev"))
	take := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "other drive", "--id", "other-drive", "--exclusive", "--scope", "environment",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if take.ExitCode != 0 {
		t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
	}
	result := erun.Run(t, []string{"exec", "gate-merge", "--source", "feature/add-widget", "--target", "main", "--dry-run"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "Add widget\n"})
	if result.ExitCode == 0 {
		t.Fatalf("expected a refusal while the environment is held exclusively, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/gate_merge_refuses_while_the_environment_is_held_exclusively", normalize.Apply(result.Combined))
}

func testExecGateMergeRefusesAClaimTakenAtTheDefaultScope(t *testing.T) {
	// The default exclusive scope, "worktree", is the tree gate-merge rewrites, so it must refuse too.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	envVars := inEnvironment(append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev"))
	take := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "merge-queue drive 2442", "--id", "merge-queue", "--exclusive", "--orchestrator", "erun",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if take.ExitCode != 0 {
		t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
	}
	list := erun.Run(t, []string{"activity", "lease", "list", "--tenant", "team", "--environment", "dev"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	refused := erun.Run(t, []string{"exec", "gate-merge", "--source", "feature/add-widget", "--target", "main", "--dry-run"},
		erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "Add widget\n"})
	if refused.ExitCode == 0 {
		t.Fatalf("expected a refusal while a default-scoped exclusive claim is held, got 0:\n%s", refused.Combined)
	}
	own := erun.Run(t, []string{
		"exec", "gate-merge", "--source", "feature/add-widget", "--target", "main",
		"--under-lease", "merge-queue", "--remote", "nonexistent", "--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "Add widget\n"})
	if own.ExitCode != 0 {
		t.Fatalf("the claim's own holder must not be refused: exit %d: %s", own.ExitCode, own.Combined)
	}
	other := erun.Run(t, []string{
		"exec", "gate-merge", "--source", "feature/add-widget", "--target", "main",
		"--under-lease", "some-other-drive", "--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "Add widget\n"})
	if other.ExitCode == 0 {
		t.Fatalf("--under-lease must exempt only the caller's own claim, got 0:\n%s", other.Combined)
	}
	golden.Equal(t, "exec/gate_merge_refuses_a_claim_taken_at_the_default_scope", normalize.Apply(
		take.Combined+list.Combined+refused.Combined+own.Combined+other.Combined))
}

func testExecGateMergeUnderLeaseIsNotRefusedByTheCallersOwnClaim(t *testing.T) {
	// A merge-queue drive spans several processes, so it holds the claim via --under-lease.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	// inEnvironment makes the lease verbs use the local store instead of an MCP edge.
	envVars := inEnvironment(append(setup.Env(), "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev"))
	take := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "team", "--environment", "dev",
		"--name", "this drive", "--id", "my-drive", "--exclusive", "--scope", "environment",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if take.ExitCode != 0 {
		t.Fatalf("take: exit %d: %s", take.ExitCode, take.Combined)
	}
	result := erun.Run(t, []string{
		"exec", "gate-merge", "--source", "feature/add-widget", "--target", "main",
		"--under-lease", "my-drive", "--remote", "nonexistent", "--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "Add widget\n"})
	if result.ExitCode != 0 {
		t.Fatalf("the claim's own holder must not be refused: exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/gate_merge_under_lease_is_not_refused_by_the_callers_own_claim", normalize.Apply(result.Combined))
}
