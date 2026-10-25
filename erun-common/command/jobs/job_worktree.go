package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/build/jobhost"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

// An agent job's exit status says nothing about its working tree, so the
// supervisor checks it on finish and, where safe (a real unprotected branch, no
// git operation in progress, no other live job on the worktree), checkpoints
// and pushes it. Otherwise it only reports.

// agentJobWorktreeCommitMessage marks a checkpoint commit as machine-authored.
const agentJobWorktreeCommitMessage = "WIP: checkpoint by the erun job supervisor\n\n" +
	"The agent job that produced this working tree ended without committing\n" +
	"its own changes. This commit exists only to keep that work from being\n" +
	"lost, not as a finished change — rewrite, amend, or squash it freely."

// environmentJobWorktreeOutcome is the zero value when there is nothing to report.
type environmentJobWorktreeOutcome struct {
	dirty    bool
	branch   string
	detached bool
	commit   string
	pushed   bool
	remote   string
	reason   string
}

func (o environmentJobWorktreeOutcome) apply(job *jobstate.EnvironmentJob) {
	job.WorktreeDirty = o.dirty
	job.WorktreeBranch = o.branch
	job.WorktreeDetached = o.detached
	job.WorktreeCommit = o.commit
	job.WorktreePushed = o.pushed
	job.WorktreeRemote = o.remote
	job.WorktreeReason = o.reason
}

func captureAgentJobWorktreeOutcome(job jobstate.EnvironmentJob, jobsDir string) environmentJobWorktreeOutcome {
	ctx := core.Context{Logger: core.NewLogger(core.VerbosityInfo)}
	dir, branch, dirty := agentJobDirtyWorktree(ctx, job)
	if !dirty {
		return environmentJobWorktreeOutcome{}
	}
	outcome := environmentJobWorktreeOutcome{dirty: true, branch: branch, detached: branch == "HEAD"}
	if contenderID, contended := agentJobWorktreeContender(jobsDir, job, time.Now()); contended {
		outcome.reason = fmt.Sprintf("the working tree had uncommitted changes when the job ended, but job %q is "+
			"still running and shares this worktree; a checkpoint here could commit that job's in-flight work under "+
			"this job's name, so none was made", contenderID)
		return outcome
	}
	if reason, refused := refuseAgentJobWorktreeCheckpoint(ctx, dir, branch); refused {
		outcome.reason = reason
		return outcome
	}
	return checkpointAgentJobWorktree(ctx, dir, branch, outcome)
}

// agentJobWorktreeContender reports another live job of any kind on the same
// worktree; dead siblings reconcile as finished.
func agentJobWorktreeContender(jobsDir string, job jobstate.EnvironmentJob, now time.Time) (string, bool) {
	dir := strings.TrimSpace(job.Dir)
	if jobsDir == "" || dir == "" {
		return "", false
	}
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return "", false
	}
	hostname := jobhost.CurrentJobHostname()
	for _, entry := range entries {
		if other, live := agentJobWorktreeContenderCandidate(jobsDir, entry, job.ID, dir, now, hostname); live {
			return other, true
		}
	}
	return "", false
}

func agentJobWorktreeContenderCandidate(jobsDir string, entry os.DirEntry, selfID, dir string, now time.Time, hostname string) (string, bool) {
	if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
		return "", false
	}
	if strings.TrimSuffix(entry.Name(), ".json") == selfID {
		return "", false
	}
	other, err := jobstate.ReadEnvironmentJob(filepath.Join(jobsDir, entry.Name()))
	if err != nil || strings.TrimSpace(other.Dir) != dir {
		return "", false
	}
	other = reconcileEnvironmentJob(jobsDir, other, now, activitylease.ProcessAlive, hostname)
	if other.Finished() {
		return "", false
	}
	return other.ID, true
}

// agentJobDirtyWorktree returns the dir and branch ("HEAD" when detached) of an
// agent job's dirty git worktree.
func agentJobDirtyWorktree(ctx core.Context, job jobstate.EnvironmentJob) (dir, branch string, dirty bool) {
	if job.Kind != EnvironmentJobKindAgent {
		return "", "", false
	}
	dir = strings.TrimSpace(job.Dir)
	if dir == "" {
		return "", "", false
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", "", false
	}
	if !gitIsInsideWorkTree(ctx, dir) {
		return "", "", false
	}
	changed, err := gitx.GitChangedWorkingTreeFiles(ctx, dir)
	if err != nil || len(changed) == 0 {
		return "", "", false
	}
	branch, err = gitx.GitCurrentBranch(ctx, dir)
	if err != nil {
		return "", "", false
	}
	return dir, branch, true
}

// refuseAgentJobWorktreeCheckpoint reports why a checkpoint commit would be unsafe.
func refuseAgentJobWorktreeCheckpoint(ctx core.Context, dir, branch string) (string, bool) {
	if branch == "HEAD" {
		return "the working tree had uncommitted changes when the job ended, but HEAD was detached; " +
			"a checkpoint commit there would be unreachable the moment HEAD moves, so none was made", true
	}
	if repoDir, err := gitResolveGitDir(ctx, dir); err == nil && gitOperationInProgress(repoDir) {
		return "the working tree had uncommitted changes when the job ended, but a git operation " +
			"(merge, rebase, cherry-pick, or revert) was in progress; committing over it could corrupt that " +
			"operation's own state, so none was made", true
	}
	if environmentJobWorktreeIsProtectedBranch(ctx, dir, branch) {
		return fmt.Sprintf("the working tree had uncommitted changes when the job ended, on %q, which "+
			"this job treats as a protected branch; an automatic checkpoint commit there is refused", branch), true
	}
	return "", false
}

// checkpointAgentJobWorktree commits and pushes, recording any failure in outcome.reason.
func checkpointAgentJobWorktree(ctx core.Context, dir, branch string, outcome environmentJobWorktreeOutcome) environmentJobWorktreeOutcome {
	commitResult, err := gitx.CommitWorkingTree(ctx, dir, model.CommitWorkingTreeParams{
		Branch:  branch,
		Message: agentJobWorktreeCommitMessage,
		// A rejecting hook would discard the work, not veto the commit.
		SkipHooks: true,
	}, gitx.CommitWorkingTreeDependencies{})
	if err != nil {
		outcome.reason = fmt.Sprintf("the working tree had uncommitted changes when the job ended and the "+
			"automatic checkpoint commit failed: %v", err)
		return outcome
	}
	outcome.commit = commitResult.Commit

	pushResult, err := gitx.PushWorkingTreeBranch(ctx, dir, model.PushWorkingTreeBranchParams{Branch: branch}, gitx.PushWorkingTreeBranchDependencies{})
	if err != nil {
		outcome.reason = fmt.Sprintf("the job made a checkpoint commit (%s) for its uncommitted changes but could "+
			"not push it: %v; that commit is only as safe as this one working tree", commitResult.Commit, err)
		return outcome
	}
	outcome.pushed = true
	outcome.remote = pushResult.Remote
	return outcome
}

func gitIsInsideWorkTree(ctx core.Context, dir string) bool {
	ctx.TraceCommand("", "git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	output, err := core.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	return err == nil && strings.TrimSpace(string(output)) == "true"
}

// gitResolveGitDir follows a linked worktree's .git pointer file.
func gitResolveGitDir(ctx core.Context, dir string) (string, error) {
	ctx.TraceCommand("", "git", "-C", dir, "rev-parse", "--git-dir")
	output, err := core.Command("git", "-C", dir, "rev-parse", "--git-dir").Output()
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

// environmentJobWorktreeIsProtectedBranch prefers the remote's default branch;
// the common names cover clones without an origin/HEAD symref.
func environmentJobWorktreeIsProtectedBranch(ctx core.Context, dir, branch string) bool {
	if def, ok := gitx.GitRemoteDefaultBranch(ctx, dir, "origin"); ok {
		return branch == def
	}
	switch branch {
	case "main", "master", "develop":
		return true
	default:
		return false
	}
}
