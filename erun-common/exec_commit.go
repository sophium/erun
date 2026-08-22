package eruncommon

import (
	"fmt"
	"io"
	"strings"
)

// CommitWorkingTreeParams names the commit an orchestrator intends: the
// branch it believes the working tree is on, and the message to record.
// Neither is defaulted or assumed — Branch is verified against the tree's
// actual current branch before anything is staged.
type CommitWorkingTreeParams struct {
	Branch string
	// Message is recorded verbatim as the commit message. It is data, not a
	// shell fragment: nothing in it is interpreted, expanded, or executed.
	Message string
}

// CommitWorkingTreeResult is what actually landed.
type CommitWorkingTreeResult struct {
	Branch string   `json:"branch"`
	Commit string   `json:"commit"`
	Files  []string `json:"files"`
}

// CommitWorkingTreeDependencies lets tests replace the git plumbing without a
// real repository, mirroring the release flow's dependency-injection pattern.
type CommitWorkingTreeDependencies struct {
	CurrentBranch GitValueResolverFunc
	CurrentCommit GitValueResolverFunc
	RunGit        GitCommandRunnerFunc
	StagedFiles   func(Context, string) ([]string, error)
}

func normalizeCommitWorkingTreeDependencies(deps CommitWorkingTreeDependencies) CommitWorkingTreeDependencies {
	if deps.CurrentBranch == nil {
		deps.CurrentBranch = GitCurrentBranch
	}
	if deps.CurrentCommit == nil {
		deps.CurrentCommit = GitShortCommit
	}
	if deps.RunGit == nil {
		deps.RunGit = GitCommandRunner
	}
	if deps.StagedFiles == nil {
		deps.StagedFiles = gitStagedFiles
	}
	return deps
}

// CommitWorkingTree stages every change in root and commits it with Message.
// Branch is verified against the tree's actual current branch, not assumed —
// a mismatch is refused loudly before anything is staged, rather than landing
// the commit on whichever branch HEAD happens to be on.
func CommitWorkingTree(ctx Context, root string, params CommitWorkingTreeParams, deps CommitWorkingTreeDependencies) (CommitWorkingTreeResult, error) {
	branch := strings.TrimSpace(params.Branch)
	if branch == "" {
		return CommitWorkingTreeResult{}, fmt.Errorf("branch is required")
	}
	if strings.TrimSpace(params.Message) == "" {
		return CommitWorkingTreeResult{}, fmt.Errorf("message is required")
	}
	deps = normalizeCommitWorkingTreeDependencies(deps)

	current, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("resolve current branch: %w", err)
	}
	if current != branch {
		return CommitWorkingTreeResult{}, fmt.Errorf("refusing to commit: working tree is on branch %q, not the declared %q", current, branch)
	}

	ctx.TraceCommand(root, "git", "add", "-A")
	ctx.TraceCommand(root, "git", "commit", "-m", "<message>")
	if ctx.DryRun {
		return CommitWorkingTreeResult{Branch: branch}, nil
	}

	return stageAndCommitWorkingTree(ctx, root, branch, params.Message, deps)
}

// stageAndCommitWorkingTree runs the mutating half of CommitWorkingTree,
// isolated so the branch-verification and dry-run branching above it don't
// inflate that function's complexity.
func stageAndCommitWorkingTree(ctx Context, root, branch, message string, deps CommitWorkingTreeDependencies) (CommitWorkingTreeResult, error) {
	if err := deps.RunGit(root, io.Discard, io.Discard, "add", "-A"); err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("git add -A: %w", err)
	}

	files, err := deps.StagedFiles(ctx, root)
	if err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("resolve staged files: %w", err)
	}
	if len(files) == 0 {
		return CommitWorkingTreeResult{}, fmt.Errorf("nothing to commit: the working tree has no changes")
	}

	if err := deps.RunGit(root, io.Discard, io.Discard, "commit", "-m", message); err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("git commit: %w", err)
	}

	commit, err := deps.CurrentCommit(ctx, root)
	if err != nil {
		return CommitWorkingTreeResult{}, fmt.Errorf("resolve new commit: %w", err)
	}

	return CommitWorkingTreeResult{Branch: branch, Commit: commit, Files: files}, nil
}

// gitStagedFiles lists what is about to be committed, read back after staging
// rather than assumed, so Files always reflects what git actually staged.
func gitStagedFiles(ctx Context, root string) ([]string, error) {
	ctx.TraceCommand("", "git", "-C", root, "diff", "--cached", "--name-only")
	output, err := Command("git", "-C", root, "diff", "--cached", "--name-only").Output()
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}
