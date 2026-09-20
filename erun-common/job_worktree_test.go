package eruncommon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The reproduction this closes: an agent job's own exit status says nothing
// about the state of the working tree it ran in. Six lanes in one session
// each ended a turn with substantial work sitting uncommitted, and every one
// of them read as a plain "exited 0" -- indistinguishable from a lane that
// committed everything it did. A test that only proves the clean-finish case
// still passes would leave every one of those cases shipping silently; the
// dirty case below is the one that has to fail loudly.

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// newAgentJobTestRepo creates a real git working tree with one commit on
// branch "main", so tests exercise the actual git plumbing job_worktree.go
// runs rather than a mocked stand-in for it.
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

// newBareRemoteForTest creates a bare repo elsewhere and wires it as dir's
// "origin", so a checkpoint's push has a real remote to land on rather than
// a stub that merely returns zero.
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

func TestAgentJobWithADirtyWorkingTreeIsNotReportedAsSuccessAndIsCheckpointedAndPushed(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")

	const tenant = "worktree-contract"
	const environment = "dirty-checkpointed"
	const id = "job"

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
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
	if job.Succeeded {
		t.Fatalf("job reported success even though it ended with an uncommitted working tree: %+v", job)
	}
	if !job.WorktreeDirty {
		t.Fatalf("WorktreeDirty = false, want true: %+v", job)
	}
	if job.WorktreeBranch != "feature/lane" {
		t.Fatalf("WorktreeBranch = %q, want %q", job.WorktreeBranch, "feature/lane")
	}
	if job.WorktreeCommit == "" {
		t.Fatalf("WorktreeCommit is empty, want a checkpoint commit: %+v", job)
	}
	if !job.WorktreePushed {
		t.Fatalf("WorktreePushed = false, want true (reason: %s): %+v", job.WorktreeReason, job)
	}
	if job.WorktreeRemote != "origin" {
		t.Fatalf("WorktreeRemote = %q, want %q", job.WorktreeRemote, "origin")
	}

	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/feature/lane")
	cmd.Dir = remote
	if err := cmd.Run(); err != nil {
		t.Fatalf("checkpoint commit did not reach the real remote: %v", err)
	}
}

func TestAgentJobWithACleanWorkingTreeStillSucceeds(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")

	const tenant = "worktree-contract"
	const environment = "clean"
	const id = "job"

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
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
	if !job.Succeeded {
		t.Fatalf("job with a clean working tree did not report success: %+v", job)
	}
	if job.WorktreeDirty {
		t.Fatalf("WorktreeDirty = true for a clean working tree: %+v", job)
	}
}

func TestAgentJobWithADirtyDetachedHeadIsReportedButNotCheckpointed(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	runGitForTest(t, repo, "checkout", "-q", "--detach", "HEAD")

	const tenant = "worktree-contract"
	const environment = "detached"
	const id = "job"

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
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
	if job.Succeeded {
		t.Fatalf("job reported success even though it ended with an uncommitted, detached-HEAD working tree: %+v", job)
	}
	if !job.WorktreeDirty || !job.WorktreeDetached {
		t.Fatalf("WorktreeDirty/WorktreeDetached = %v/%v, want true/true: %+v", job.WorktreeDirty, job.WorktreeDetached, job)
	}
	if job.WorktreeCommit != "" {
		t.Fatalf("WorktreeCommit = %q, want empty: a detached HEAD must never get an automatic checkpoint commit", job.WorktreeCommit)
	}
	if !strings.Contains(job.WorktreeReason, "detached") {
		t.Fatalf("WorktreeReason %q does not explain the detached HEAD", job.WorktreeReason)
	}
}

func TestAgentJobWithADirtyWorkingTreeOnAProtectedBranchIsReportedButNotCheckpointed(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	dirtyWorkingTree(t, repo)

	const tenant = "worktree-contract"
	const environment = "protected-branch"
	const id = "job"

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
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

// TestAgentJobFinishingWhileASiblingIsStillLiveOnTheSameWorktreeIsNotCheckpointed
// reproduces the misattribution failure mode directly: a job that never
// wrote anything itself finishes (here, by a plain clean exit rather than a
// cancellation, since both reach the same finishEnvironmentJob checkpoint
// path) while a second, still-running job shares its exact worktree with
// real uncommitted changes. Before the fix, the finishing job's checkpoint
// read `git status` at large and committed the sibling's work under its own
// name; this asserts that never happens while the sibling is still alive.
func TestAgentJobFinishingWhileASiblingIsStillLiveOnTheSameWorktreeIsNotCheckpointed(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	dirtyWorkingTree(t, repo)

	const tenant = "worktree-contract"
	const environment = "concurrent-sibling"
	const siblingID = "i2083"
	const id = "land2083"

	dir, err := environmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}
	sibling := EnvironmentJob{
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

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
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

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
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

// rejectCommitsWithAHook installs a pre-commit hook that refuses every commit,
// modelling the repository's own hook refusing one whose staged code a linter
// objects to. It records that it ran, so a test can tell "the hook was
// bypassed" apart from "the hook was never reachable" — the two are
// indistinguishable from the commit's own exit status, which is exactly how
// this defect stayed hidden.
func rejectCommitsWithAHook(t *testing.T, repo string) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	// Pin the hook directory explicitly: the ambient core.hooksPath of whatever
	// repository runs these tests must not decide whether this test has a hook.
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

// fileOnRemoteBranch reads a file out of a branch as it exists on the real
// remote, so a test can require the run's actual bytes rather than merely that
// a ref landed there. A ref can exist and still not carry the work.
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

// The reproduction of the reported failure: a killed job's in-flight work was
// lost because the automatic checkpoint commit ran the repository's own
// pre-commit hook, and a lint finding about an unused constant refused it. The
// hook is a veto over a human's commit; against a checkpoint that is the last
// act of an already-finished run, the veto destroys the work instead of
// deferring it. Before this fix the checkpoint commit fails here, WorktreeCommit
// stays empty, and nothing reaches the remote.
func TestAgentJobCheckpointCommitSurvivesARepositoryPreCommitHookThatRejectsIt(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	marker := rejectCommitsWithAHook(t, repo)

	const tenant = "worktree-contract"
	const environment = "hostile-pre-commit"
	const id = "job"

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
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
	// The assertions that matter: the work was actually preserved. Each of
	// these is empty or false on the pre-fix code, for the reported reason.
	if job.WorktreeCommit == "" {
		t.Fatalf("the checkpoint commit did not survive the repository's pre-commit hook; "+
			"the run's work was lost (reason: %s): %+v", job.WorktreeReason, job)
	}
	if !job.WorktreePushed {
		t.Fatalf("the checkpoint commit was not pushed, so it saved nothing beyond this one "+
			"working tree (reason: %s): %+v", job.WorktreeReason, job)
	}

	// The reported failure is the loss of specific bytes — a staged file that
	// existed only in the killed pod's working tree. A ref landing on the remote
	// would not by itself prove they survived, so read the run's work back out of
	// the remote and require the work itself.
	if got := fileOnRemoteBranch(t, remote, "feature/lane", "uncommitted.txt"); got != "lane work\n" {
		t.Fatalf("the remote's rescued checkpoint commit carries %q, want the run's in-flight work %q",
			got, "lane work\n")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("the repository's pre-commit hook ran for the checkpoint commit; hooks must not "+
			"be able to veto the safety net (marker: %s)", marker)
	}

	// Guard against a vacuous pass: if the hook above were inert or unreachable,
	// the checkpoint assertions would hold for the wrong reason. Prove it still
	// refuses an ordinary commit in this very repository.
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

// The other half of the contract, so the bypass cannot quietly become the
// default: a commit that does not ask to skip hooks still runs them and is
// still refused by them. This is what keeps `erun exec commit` — a person's own
// commit — under the repository's hook enforcement.
func TestCommitWorkingTreeStillHonorsRepositoryHooksWhenNotSkipped(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	marker := rejectCommitsWithAHook(t, repo)
	dirtyWorkingTree(t, repo)

	_, err := CommitWorkingTree(testContext(), repo, CommitWorkingTreeParams{
		Branch:  "feature/lane",
		Message: "a person's own commit",
	}, CommitWorkingTreeDependencies{})
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
