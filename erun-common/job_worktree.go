package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// An agent job's working tree is the one thing its own exit status says
// nothing about: a clean exit and a tree with 1,200 uncommitted lines look
// identical to a caller reading only State and ExitCode. This is that gap,
// closed the same way #1568 closed gate-incomplete — a check the supervisor
// makes on every agent job's own finish, folded into the record it already
// writes, rather than a prompt instruction the agent has to remember under
// turn pressure.
//
// The supervisor does not just observe. Where it is safe to, it also makes a
// machine-authored checkpoint commit and pushes it, because the agent that
// would otherwise do this by hand is already gone by the time anyone reads
// the record. "Safe" is deliberately narrow: a real branch (not detached
// HEAD, where a commit is unreachable the moment HEAD moves), not a branch
// this job treats as protected (main/master/develop, or the remote's actual
// default), and not mid-merge/rebase/cherry-pick (where committing over the
// operation's own state could corrupt it). Outside that envelope the
// supervisor only reports what it saw; it never guesses its way into a
// commit that could do more harm than the dirty tree it was trying to save.

// agentJobWorktreeCommitMessage marks a checkpoint commit as machine-authored
// so nobody mistakes it for the agent's own work.
const agentJobWorktreeCommitMessage = "WIP: checkpoint by the erun job supervisor\n\n" +
	"The agent job that produced this working tree ended without committing\n" +
	"its own changes. This commit exists only to keep that work from being\n" +
	"lost, not as a finished change — rewrite, amend, or squash it freely."

// environmentJobWorktreeOutcome is what an agent job's finish observed about
// its working tree and, best-effort, did about it. The zero value means
// "nothing to report": either the job was not an agent job, its Dir was not a
// git working tree, or the tree was clean.
type environmentJobWorktreeOutcome struct {
	dirty    bool
	branch   string
	detached bool
	commit   string
	pushed   bool
	remote   string
	reason   string
}

func (o environmentJobWorktreeOutcome) apply(job *EnvironmentJob) {
	job.WorktreeDirty = o.dirty
	job.WorktreeBranch = o.branch
	job.WorktreeDetached = o.detached
	job.WorktreeCommit = o.commit
	job.WorktreePushed = o.pushed
	job.WorktreeRemote = o.remote
	job.WorktreeReason = o.reason
}

// captureAgentJobWorktreeOutcome is called once, at the same point the
// supervisor resolves the rest of a finished job's outcome. It runs plain git
// plumbing directly rather than through a caller-supplied Context, because the
// supervisor has none of its own beyond a discarded stdout/stderr — this is
// not a user-facing trace, it is the same kind of internal bookkeeping call
// finishEnvironmentJob already makes.
func captureAgentJobWorktreeOutcome(job EnvironmentJob) environmentJobWorktreeOutcome {
	if job.Kind != EnvironmentJobKindAgent {
		return environmentJobWorktreeOutcome{}
	}
	dir := strings.TrimSpace(job.Dir)
	if dir == "" {
		return environmentJobWorktreeOutcome{}
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return environmentJobWorktreeOutcome{}
	}
	ctx := Context{Logger: NewLogger(VerbosityInfo)}
	if inside, err := gitIsInsideWorkTree(ctx, dir); err != nil || !inside {
		return environmentJobWorktreeOutcome{}
	}
	changed, err := gitChangedWorkingTreeFiles(ctx, dir)
	if err != nil || len(changed) == 0 {
		return environmentJobWorktreeOutcome{}
	}
	branch, err := GitCurrentBranch(ctx, dir)
	if err != nil {
		return environmentJobWorktreeOutcome{}
	}

	outcome := environmentJobWorktreeOutcome{dirty: true, branch: branch}
	if branch == "HEAD" {
		outcome.detached = true
		outcome.reason = "the working tree had uncommitted changes when the job ended, but HEAD was detached; " +
			"a checkpoint commit there would be unreachable the moment HEAD moves, so none was made"
		return outcome
	}
	if repoDir, err := gitResolveGitDir(ctx, dir); err == nil && gitOperationInProgress(repoDir) {
		outcome.reason = "the working tree had uncommitted changes when the job ended, but a git operation " +
			"(merge, rebase, cherry-pick, or revert) was in progress; committing over it could corrupt that " +
			"operation's own state, so none was made"
		return outcome
	}
	if environmentJobWorktreeIsProtectedBranch(ctx, dir, branch) {
		outcome.reason = fmt.Sprintf("the working tree had uncommitted changes when the job ended, on %q, which "+
			"this job treats as a protected branch; an automatic checkpoint commit there is refused", branch)
		return outcome
	}

	commitResult, err := CommitWorkingTree(ctx, dir, CommitWorkingTreeParams{
		Branch:  branch,
		Message: agentJobWorktreeCommitMessage,
	}, CommitWorkingTreeDependencies{})
	if err != nil {
		outcome.reason = fmt.Sprintf("the working tree had uncommitted changes when the job ended and the "+
			"automatic checkpoint commit failed: %v", err)
		return outcome
	}
	outcome.commit = commitResult.Commit

	pushResult, err := PushWorkingTreeBranch(ctx, dir, PushWorkingTreeBranchParams{Branch: branch}, PushWorkingTreeBranchDependencies{})
	if err != nil {
		outcome.reason = fmt.Sprintf("the job made a checkpoint commit (%s) for its uncommitted changes but could "+
			"not push it: %v; that commit is only as safe as this one working tree", commitResult.Commit, err)
		return outcome
	}
	outcome.pushed = true
	outcome.remote = pushResult.Remote
	return outcome
}

// gitIsInsideWorkTree reports whether dir sits inside a git working tree at
// all — an agent job's Dir is not guaranteed to be one, and this is what lets
// every other check in this file no-op cleanly instead of misreading git's
// own "not a git repository" error as some other failure.
func gitIsInsideWorkTree(ctx Context, dir string) (bool, error) {
	ctx.TraceCommand("", "git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	output, err := Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(string(output)) == "true", nil
}

// gitResolveGitDir resolves dir's actual .git directory, following the
// pointer file a linked worktree uses instead of a real .git directory, so the
// in-progress-operation check below looks in the right place for a lane
// checked out as a worktree rather than a plain clone.
func gitResolveGitDir(ctx Context, dir string) (string, error) {
	ctx.TraceCommand("", "git", "-C", dir, "rev-parse", "--git-dir")
	output, err := Command("git", "-C", dir, "rev-parse", "--git-dir").Output()
	if err != nil {
		return "", err
	}
	resolved := strings.TrimSpace(string(output))
	if resolved == "" {
		return "", fmt.Errorf("git rev-parse --git-dir returned an empty path")
	}
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(dir, resolved)
	}
	return resolved, nil
}

// gitOperationInProgressMarkers are the state files git leaves in the git dir
// while a merge, rebase, cherry-pick, or revert is unresolved.
var gitOperationInProgressMarkers = []string{
	"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply",
}

func gitOperationInProgress(gitDir string) bool {
	for _, marker := range gitOperationInProgressMarkers {
		if _, err := os.Stat(filepath.Join(gitDir, marker)); err == nil {
			return true
		}
	}
	return false
}

// environmentJobWorktreeIsProtectedBranch reports whether branch is one this
// job refuses to leave an automatic checkpoint commit on. The remote's own
// default branch is authoritative when it can be read; the common names cover
// the case where origin/HEAD's symref was never set locally (e.g. a shallow
// or single-branch fetch), which is a real layout this repository's own lanes
// use.
func environmentJobWorktreeIsProtectedBranch(ctx Context, dir, branch string) bool {
	if def, ok := gitRemoteDefaultBranch(ctx, dir, "origin"); ok {
		return branch == def
	}
	switch branch {
	case "main", "master", "develop":
		return true
	default:
		return false
	}
}

func gitRemoteDefaultBranch(ctx Context, dir, remote string) (string, bool) {
	ctx.TraceCommand("", "git", "-C", dir, "symbolic-ref", "refs/remotes/"+remote+"/HEAD")
	output, err := Command("git", "-C", dir, "symbolic-ref", "refs/remotes/"+remote+"/HEAD").Output()
	if err != nil {
		return "", false
	}
	ref := strings.TrimSpace(string(output))
	prefix := "refs/remotes/" + remote + "/"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	return strings.TrimPrefix(ref, prefix), true
}
