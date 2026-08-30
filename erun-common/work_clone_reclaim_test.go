package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This repository squash-merges every branch. That means the *normal* end
// state for a landed feature branch is: its commits are individually absent
// from the default branch (folded into one squash commit under a different
// SHA), and its own remote branch is very often deleted outright once GitHub
// merges it. A reclaim check that reads "no upstream" or "commits not on
// upstream" as unsafe by itself would therefore refuse to reclaim almost
// every clone that actually finished cleanly. The tests below pin both
// halves of that: the ordinary unpushed-and-truly-abandoned case stays kept,
// and the squash-merged case is proven safe and reclaimed.

func withFakeWorkHome(t *testing.T) (home, root string) {
	t.Helper()
	home = t.TempDir()
	root = filepath.Join(home, "work")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir work root: %v", err)
	}
	original := workCloneUserHomeDir
	workCloneUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { workCloneUserHomeDir = original })
	return home, root
}

func initGitRepoAt(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir repo dir: %v", err)
	}
	runGitForTest(t, dir, "init", "-q", "-b", "main")
	runGitForTest(t, dir, "config", "user.email", "test@example.com")
	runGitForTest(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGitForTest(t, dir, "add", "seed.txt")
	runGitForTest(t, dir, "commit", "-q", "-m", "seed")
}

func testContext() Context {
	return Context{Logger: NewLogger(VerbosityInfo)}
}

// writeAndCommit writes name/content into dir and commits it, for building up
// the exact commit graphs the decision tests below compare against.
func writeAndCommit(t *testing.T, dir, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGitForTest(t, dir, "add", name)
	runGitForTest(t, dir, "commit", "-q", "-m", message)
}

// newRepoWithBareRemote is newAgentJobTestRepo plus newBareRemoteForTest,
// returning both paths for scenarios that need a second clone of the same
// remote (simulating a separate actor merging the branch).
func newRepoWithBareRemote(t *testing.T) (repo, bare string) {
	t.Helper()
	repo = newAgentJobTestRepo(t)
	bare = newBareRemoteForTest(t, repo)
	return repo, bare
}

func TestDecideWorkCloneReclaimCleanAndFullyPushedBranchIsReclaimed(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "feature/lane")

	decision := DecideWorkCloneReclaim(testContext(), repo)
	if !decision.Reclaim {
		t.Fatalf("Reclaim = false, want true (reason: %s)", decision.Reason)
	}
}

func TestDecideWorkCloneReclaimDirtyWorkingTreeIsKept(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "feature/lane")
	dirtyWorkingTree(t, repo)

	decision := DecideWorkCloneReclaim(testContext(), repo)
	if decision.Reclaim {
		t.Fatalf("Reclaim = true for a dirty tree, want false")
	}
	if !strings.Contains(decision.Reason, "uncommitted") {
		t.Fatalf("Reason %q does not explain the dirty tree", decision.Reason)
	}
}

func TestDecideWorkCloneReclaimUntrackedFileOnlyStillCountsAsDirty(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "feature/lane")
	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("not committed\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	decision := DecideWorkCloneReclaim(testContext(), repo)
	if decision.Reclaim {
		t.Fatalf("Reclaim = true for an untracked-only tree, want false: an untracked file can still hold real work")
	}
}

func TestDecideWorkCloneReclaimUnpushedCommitsWithNoProofTheyLandedElsewhereAreKept(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "feature/lane")
	writeAndCommit(t, repo, "more.txt", "more work\n", "unpushed work")

	decision := DecideWorkCloneReclaim(testContext(), repo)
	if decision.Reclaim {
		t.Fatalf("Reclaim = true for a branch with unpushed, unmerged commits, want false")
	}
	if !strings.Contains(decision.Reason, "ahead") {
		t.Fatalf("Reason %q does not explain the unpushed commit(s)", decision.Reason)
	}
}

func TestDecideWorkCloneReclaimBranchWithNoUpstreamAndNoProofOfMergeIsKept(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	writeAndCommit(t, repo, "more.txt", "more work\n", "never pushed")

	decision := DecideWorkCloneReclaim(testContext(), repo)
	if decision.Reclaim {
		t.Fatalf("Reclaim = true for a branch that was never pushed at all, want false")
	}
	if !strings.Contains(decision.Reason, "no upstream") {
		t.Fatalf("Reason %q does not explain the missing upstream", decision.Reason)
	}
}

func TestDecideWorkCloneReclaimDetachedHeadReachableFromARemoteRefIsReclaimed(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "origin", "main")
	runGitForTest(t, repo, "checkout", "-q", "--detach", "HEAD")

	decision := DecideWorkCloneReclaim(testContext(), repo)
	if !decision.Reclaim {
		t.Fatalf("Reclaim = false for a detached HEAD already pushed via main, want true (reason: %s)", decision.Reason)
	}
}

func TestDecideWorkCloneReclaimDetachedHeadNotReachableAnywhereIsKept(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "origin", "main")
	writeAndCommit(t, repo, "more.txt", "more work\n", "never pushed")
	runGitForTest(t, repo, "checkout", "-q", "--detach", "HEAD")

	decision := DecideWorkCloneReclaim(testContext(), repo)
	if decision.Reclaim {
		t.Fatalf("Reclaim = true for a detached HEAD with no remote ref containing it, want false")
	}
	if !strings.Contains(decision.Reason, "detached") {
		t.Fatalf("Reason %q does not explain the unreachable detached HEAD", decision.Reason)
	}
}

func TestDecideWorkCloneReclaimNotAGitWorkingTreeIsKept(t *testing.T) {
	dir := t.TempDir()
	decision := DecideWorkCloneReclaim(testContext(), dir)
	if decision.Reclaim {
		t.Fatalf("Reclaim = true for a plain directory, want false")
	}
}

func TestDecideWorkCloneReclaimSquashMergedBranchWithADeletedRemoteBranchIsReclaimed(t *testing.T) {
	repo, bare := newRepoWithBareRemote(t)
	runGitForTest(t, repo, "push", "-q", "origin", "main")
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	writeAndCommit(t, repo, "feature-a.txt", "feature work a\n", "feature commit 1")
	writeAndCommit(t, repo, "feature-b.txt", "feature work b\n", "feature commit 2")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "feature/lane")

	// A separate clone plays the role of the merge tool: squash-merge
	// feature/lane into main, push main, then delete the remote feature
	// branch outright -- the exact behavior GitHub's "delete branch on
	// merge" performs, and the normal end state for a branch this
	// repository lands.
	merger := t.TempDir()
	runGitForTest(t, filepath.Dir(merger), "clone", "-q", "--branch", "main", bare, merger)
	runGitForTest(t, merger, "fetch", "-q", "origin", "feature/lane")
	runGitForTest(t, merger, "merge", "-q", "--squash", "origin/feature/lane")
	runGitForTest(t, merger, "commit", "-q", "-m", "Squash merge feature/lane (#1)")
	runGitForTest(t, merger, "push", "-q", "origin", "main")
	runGitForTest(t, merger, "push", "-q", "origin", "--delete", "feature/lane")

	decision := DecideWorkCloneReclaim(testContext(), repo)
	if !decision.Reclaim {
		t.Fatalf("Reclaim = false for a branch squash-merged into main, want true (reason: %s)", decision.Reason)
	}
	if !strings.Contains(decision.Reason, "merged") {
		t.Fatalf("Reason %q does not explain the squash-merge proof", decision.Reason)
	}
}

func TestDecideWorkCloneReclaimUnpushedBranchThatOnlyLooksLikeASquashMergeIsKept(t *testing.T) {
	repo, bare := newRepoWithBareRemote(t)
	runGitForTest(t, repo, "push", "-q", "origin", "main")
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	writeAndCommit(t, repo, "feature-a.txt", "feature work a\n", "feature commit 1")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "feature/lane")

	// Somebody else's unrelated work lands on main -- main moves, but
	// feature/lane's own changeset never gets merged anywhere.
	other := t.TempDir()
	runGitForTest(t, filepath.Dir(other), "clone", "-q", "--branch", "main", bare, other)
	writeAndCommit(t, other, "unrelated.txt", "unrelated\n", "unrelated main work")
	runGitForTest(t, other, "push", "-q", "origin", "main")

	// Then feature/lane's own commit gets one more unpushed commit ahead of
	// what it already pushed, so this is unambiguously "still live work in
	// progress", not a landed branch.
	writeAndCommit(t, repo, "feature-b.txt", "feature work b\n", "feature commit 2, unpushed")

	decision := DecideWorkCloneReclaim(testContext(), repo)
	if decision.Reclaim {
		t.Fatalf("Reclaim = true for a branch that merely resembles a squash merge, want false (its changeset was never actually merged)")
	}
}

func TestIsUnderWorkRoot(t *testing.T) {
	cases := []struct {
		name string
		root string
		dir  string
		want bool
	}{
		{"clone inside root", "/home/erun/work", "/home/erun/work/erun-w2", true},
		{"nested clone inside root", "/home/erun/work", "/home/erun/work/erun-w2/sub", true},
		{"the root itself", "/home/erun/work", "/home/erun/work", false},
		{"sibling runtime repo", "/home/erun/work", "/home/erun/git/erun", false},
		{"parent of root", "/home/erun/work", "/home/erun", false},
		{"path traversal escape", "/home/erun/work", "/home/erun/work/../git/erun", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUnderWorkRoot(tc.root, tc.dir); got != tc.want {
				t.Fatalf("isUnderWorkRoot(%q, %q) = %v, want %v", tc.root, tc.dir, got, tc.want)
			}
		})
	}
}

func TestReclaimAgentJobWorkCloneCleanPushedIsRemoved(t *testing.T) {
	isolateActivityCache(t)
	_, root := withFakeWorkHome(t)
	repo := filepath.Join(root, "erun-w1")
	initGitRepoAt(t, repo)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "feature/lane")

	const tenant, environment, id = "reclaim-contract", "clean-pushed", "job"
	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant: tenant, Environment: environment, ID: id, Name: id,
		Dir: repo, Agent: "claude", Command: []string{"sh", "-c", "exit 0"},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if !job.CloneReclaimed {
		t.Fatalf("CloneReclaimed = false, want true (kept reason: %s)", job.CloneKeptReason)
	}
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Fatalf("clone dir still exists after being reported reclaimed: %v", err)
	}
}

func TestReclaimAgentJobWorkCloneDirtyDetachedIsKept(t *testing.T) {
	isolateActivityCache(t)
	_, root := withFakeWorkHome(t)
	repo := filepath.Join(root, "erun-w2")
	initGitRepoAt(t, repo)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "--detach", "HEAD")

	const tenant, environment, id = "reclaim-contract", "dirty-detached", "job"
	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant: tenant, Environment: environment, ID: id, Name: id,
		Dir: repo, Agent: "claude", Command: []string{"sh", "-c", "printf 'work\\n' > uncommitted.txt"},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if job.CloneReclaimed {
		t.Fatalf("CloneReclaimed = true for a dirty, detached-HEAD clone, want false")
	}
	if job.CloneKeptReason == "" {
		t.Fatalf("CloneKeptReason is empty, want an explanation")
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatalf("clone dir was removed even though it was kept: %v", err)
	}
}

func TestReclaimAgentJobWorkCloneCheckpointedAndPushedIsReclaimedAfterTheCheckpoint(t *testing.T) {
	isolateActivityCache(t)
	_, root := withFakeWorkHome(t)
	repo := filepath.Join(root, "erun-w3")
	initGitRepoAt(t, repo)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")

	const tenant, environment, id = "reclaim-contract", "checkpointed", "job"
	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant: tenant, Environment: environment, ID: id, Name: id,
		Dir: repo, Agent: "claude", Command: []string{"sh", "-c", "printf 'work\\n' > uncommitted.txt"},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if !job.WorktreePushed {
		t.Fatalf("expected the dirty tree to be checkpointed and pushed first (reason: %s)", job.WorktreeReason)
	}
	if !job.CloneReclaimed {
		t.Fatalf("CloneReclaimed = false after its own dirty tree was checkpointed and pushed, want true (kept reason: %s)", job.CloneKeptReason)
	}
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Fatalf("clone dir still exists after being reported reclaimed: %v", err)
	}
}

func TestReclaimAgentJobWorkCloneCommandJobsAreNeverReclaimed(t *testing.T) {
	isolateActivityCache(t)
	_, root := withFakeWorkHome(t)
	repo := filepath.Join(root, "erun-w4")
	initGitRepoAt(t, repo)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "feature/lane")

	const tenant, environment, id = "reclaim-contract", "command-kind", "job"
	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant: tenant, Environment: environment, ID: id, Name: id,
		Dir: repo, Command: []string{"sh", "-c", "exit 0"},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if job.CloneReclaimed {
		t.Fatalf("CloneReclaimed = true for a command job, want false: only agent jobs are candidates")
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatalf("a command job's Dir must never be touched: %v", err)
	}
}

func TestReclaimAgentJobWorkCloneOutsideWorkRootIsNeverReclaimed(t *testing.T) {
	isolateActivityCache(t)
	withFakeWorkHome(t)
	// Deliberately outside the fake home's work root -- stands in for the
	// runtime repo itself, or any other directory a caller might mistakenly
	// pass as Dir.
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "feature/lane")

	const tenant, environment, id = "reclaim-contract", "outside-root", "job"
	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant: tenant, Environment: environment, ID: id, Name: id,
		Dir: repo, Agent: "claude", Command: []string{"sh", "-c", "exit 0"},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if job.CloneReclaimed {
		t.Fatalf("CloneReclaimed = true for a clone outside the work root, want false")
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatalf("a clone outside the work root must never be touched: %v", err)
	}
}

func TestReclaimAgentJobWorkCloneStillRunningIsNeverTouched(t *testing.T) {
	isolateActivityCache(t)
	_, root := withFakeWorkHome(t)
	repo := filepath.Join(root, "erun-w5")
	initGitRepoAt(t, repo)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature/lane")
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "feature/lane")

	const tenant, environment, id = "reclaim-contract", "still-running", "job"
	done := make(chan error, 1)
	go func() {
		done <- RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
			Tenant: tenant, Environment: environment, ID: id, Name: id,
			Dir: repo, Agent: "claude", Command: []string{"sh", "-c", "sleep 0.5"},
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	sawRunning := false
	for time.Now().Before(deadline) {
		job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
		if err == nil && job.State == EnvironmentJobStateRunning {
			sawRunning = true
			if _, statErr := os.Stat(repo); statErr != nil {
				t.Fatalf("clone dir must never be touched while its job is running: %v", statErr)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !sawRunning {
		t.Fatalf("never observed the job in the running state; the assertion above never ran")
	}

	if err := <-done; err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}
	job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if !job.CloneReclaimed {
		t.Fatalf("CloneReclaimed = false once the job actually finished, want true (kept reason: %s)", job.CloneKeptReason)
	}
}
