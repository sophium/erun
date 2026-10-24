package jobs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// newAgentJobTestRepo creates a real git working tree with one commit on "main".
func newAgentJobTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitForTest(t, dir, "init", "-q", "-b", "main")
	runGitForTest(t, dir, "config", "user.email", "test@example.com")
	runGitForTest(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGitForTest(t, dir, "add", "seed.txt")
	runGitForTest(t, dir, "commit", "-q", "-m", "seed")
	return dir
}

// newBareRemoteForTest wires a real bare repo as dir's "origin".
func newBareRemoteForTest(t *testing.T, dir string) string {
	t.Helper()
	remote := t.TempDir()
	runGitForTest(t, remote, "init", "-q", "--bare")
	runGitForTest(t, dir, "remote", "add", "origin", remote)
	return remote
}

func dirtyWorkingTree(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "uncommitted.txt"), []byte("lane work\n"), 0o644); err != nil {
		t.Fatalf("write uncommitted file: %v", err)
	}
}

func TestAgentJobWithADirtyWorkingTreeOnAProtectedBranchIsReportedButNotCheckpointed(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	dirtyWorkingTree(t, repo)

	const tenant = "worktree-contract"
	const environment = "protected-branch"
	const id = "job"

	if err := RunEnvironmentJobSupervisor(model.EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Dir:         repo,
		Agent:       "claude",
		Command:     []string{"sh", "-c", "exit 0"},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if job.Succeeded {
		t.Fatalf("job reported success even though it left main dirty: %+v", job)
	}
	if job.WorktreeCommit != "" {
		t.Fatalf("WorktreeCommit = %q, want empty: main must never get an automatic checkpoint commit", job.WorktreeCommit)
	}
	if !strings.Contains(job.WorktreeReason, "protected") {
		t.Fatalf("WorktreeReason %q does not explain the refusal", job.WorktreeReason)
	}
}

// A finishing job must not checkpoint a still-live sibling's work on a shared worktree.
func TestAgentJobFinishingWhileASiblingIsStillLiveOnTheSameWorktreeIsNotCheckpointed(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	dirtyWorkingTree(t, repo)

	const tenant = "worktree-contract"
	const environment = "concurrent-sibling"
	const siblingID = "i2083"
	const id = "land2083"

	dir, err := shared.EnvironmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	sibling := shared.EnvironmentJob{
		ID:        siblingID,
		Name:      siblingID,
		State:     EnvironmentJobStateRunning,
		Kind:      EnvironmentJobKindAgent,
		Dir:       repo,
		PID:       os.Getpid(),
		StartedAt: time.Now(),
	}
	if err := writeEnvironmentJob(dir, sibling); err != nil {
		t.Fatalf("writeEnvironmentJob (sibling): %v", err)
	}

	if err := RunEnvironmentJobSupervisor(model.EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Dir:         repo,
		Agent:       "claude",
		Command:     []string{"sh", "-c", "exit 0"},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if !job.WorktreeDirty {
		t.Fatalf("WorktreeDirty = false, want true: %+v", job)
	}
	if job.WorktreeCommit != "" {
		t.Fatalf("WorktreeCommit = %q, want empty: a job must never checkpoint a tree a still-running sibling shares", job.WorktreeCommit)
	}
	if !strings.Contains(job.WorktreeReason, siblingID) {
		t.Fatalf("WorktreeReason %q does not name the still-running sibling %q", job.WorktreeReason, siblingID)
	}

	status, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if len(strings.TrimSpace(string(status))) == 0 {
		t.Fatalf("the sibling's uncommitted work was committed out from under it")
	}
}

func TestCommandJobIgnoresItsWorkingTreeState(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	dirtyWorkingTree(t, repo)

	const tenant = "worktree-contract"
	const environment = "command-kind"
	const id = "job"

	if err := RunEnvironmentJobSupervisor(model.EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Dir:         repo,
		Command:     []string{"sh", "-c", "exit 0"},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if !job.Succeeded {
		t.Fatalf("a plain command job must not have its own working tree checked: %+v", job)
	}
	if job.WorktreeDirty {
		t.Fatalf("WorktreeDirty = true for a command job, want false (only agent jobs are checked)")
	}
}

// rejectCommitsWithAHook installs a pre-commit hook that refuses every commit and
// records that it ran, so bypassed and unreachable hooks can be told apart.
func rejectCommitsWithAHook(t *testing.T, repo string) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	// Pin hooksPath so the ambient repository's config cannot decide this.
	runGitForTest(t, repo, "config", "core.hooksPath", ".git/hooks")
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	script := "#!/bin/sh\n" +
		"printf 'ran\\n' >> " + marker + "\n" +
		"echo 'golangci-lint --fix erun-common: const gateRefusalTestTag is unused (unused)' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(script), 0o755); err != nil {
		t.Fatalf("write pre-commit hook: %v", err)
	}
	return marker
}

// fileOnRemoteBranch reads a file from the remote branch; a ref alone does not prove the work landed.
func fileOnRemoteBranch(t *testing.T, remote, branch, path string) string {
	t.Helper()
	cmd := exec.Command("git", "show", branch+":"+path)
	cmd.Dir = remote
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the rescued checkpoint commit did not reach the real remote carrying the run's "+
			"in-flight work (%s): %v", path, err)
	}
	return string(out)
}

// A rejecting pre-commit hook must not discard a killed job's checkpoint.
func TestAgentJobCheckpointCommitSurvivesARepositoryPreCommitHookThatRejectsIt(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	marker := rejectCommitsWithAHook(t, repo)

	const tenant = "worktree-contract"
	const environment = "hostile-pre-commit"
	const id = "job"

	if err := RunEnvironmentJobSupervisor(model.EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Name:        id,
		Dir:         repo,
		Agent:       "claude",
		Command:     []string{"sh", "-c", "printf 'lane work\\n' > uncommitted.txt"},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if !job.WorktreeDirty {
		t.Fatalf("WorktreeDirty = false, want true (reason: %s): %+v", job.WorktreeReason, job)
	}
	if job.WorktreeCommit == "" {
		t.Fatalf("the checkpoint commit did not survive the repository's pre-commit hook; "+
			"the run's work was lost (reason: %s): %+v", job.WorktreeReason, job)
	}
	if !job.WorktreePushed {
		t.Fatalf("the checkpoint commit was not pushed, so it saved nothing beyond this one "+
			"working tree (reason: %s): %+v", job.WorktreeReason, job)
	}

	// Require the work's actual bytes on the remote.
	if got := fileOnRemoteBranch(t, remote, "feature/lane", "uncommitted.txt"); got != "lane work\n" {
		t.Fatalf("the remote's rescued checkpoint commit carries %q, want the run's in-flight work %q",
			got, "lane work\n")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("the repository's pre-commit hook ran for the checkpoint commit; hooks must not "+
			"be able to veto the safety net (marker: %s)", marker)
	}

	// Guard against a vacuous pass: the hook must still refuse an ordinary commit.
	if err := os.WriteFile(filepath.Join(repo, "after.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write after.txt: %v", err)
	}
	runGitForTest(t, repo, "add", "after.txt")
	ordinary := exec.Command("git", "commit", "-m", "ordinary commit")
	ordinary.Dir = repo
	if err := ordinary.Run(); err == nil {
		t.Fatalf("the pre-commit hook did not reject an ordinary commit; the checkpoint assertions " +
			"above prove nothing about hook bypass")
	}
}

// A commit that does not ask to skip hooks still runs them.
func TestCommitWorkingTreeStillHonorsRepositoryHooksWhenNotSkipped(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	marker := rejectCommitsWithAHook(t, repo)
	dirtyWorkingTree(t, repo)

	_, err := gitx.CommitWorkingTree(testContext(), repo, model.CommitWorkingTreeParams{
		Branch:  "feature/lane",
		Message: "a person's own commit",
	}, gitx.CommitWorkingTreeDependencies{})
	if err == nil {
		t.Fatalf("CommitWorkingTree committed despite a rejecting pre-commit hook; hook enforcement " +
			"must stay in force for callers that do not opt out")
	}
	if !strings.Contains(err.Error(), "is unused") {
		t.Fatalf("error %q does not carry the hook's own rejection; a caller cannot tell why it was refused", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("the pre-commit hook did not run for a non-skipping commit (%v); the bypass has become "+
			"the default", statErr)
	}
}
