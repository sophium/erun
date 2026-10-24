package mergequeue

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/gitx"
)

// MergeWorkingTreeBranchParams names the branch to merge in. Always a merge, never a rebase:
// review comments anchor to commit IDs.
type MergeWorkingTreeBranchParams struct {
	// TargetBranch is resolved against Remote, e.g. "origin/main".
	TargetBranch string
	// Remote defaults to "origin".
	Remote string
}

// MergeWorkingTreeBranchResult is what actually landed.
type MergeWorkingTreeBranchResult struct {
	Branch       string `json:"branch"`
	TargetBranch string `json:"targetBranch"`
	Remote       string `json:"remote"`
	Commit       string `json:"commit"`
}

// MergeConflictError reports a merge left mid-conflict for the caller to resolve or abort.
type MergeConflictError struct {
	TargetBranch    string
	ConflictedFiles []string
}

func (e *MergeConflictError) Error() string {
	return fmt.Sprintf(
		"merging %s left %d file(s) conflicted: %s — resolve them and commit, or run `git merge --abort` to back out",
		e.TargetBranch, len(e.ConflictedFiles), strings.Join(e.ConflictedFiles, ", "),
	)
}

// MergeWorkingTreeBranchDependencies lets tests replace the git plumbing.
type MergeWorkingTreeBranchDependencies struct {
	CurrentBranch   gitx.GitValueResolverFunc
	CurrentCommit   gitx.GitValueResolverFunc
	RunGit          gitx.GitCommandRunnerFunc
	ConflictedFiles func(root string, runGit gitx.GitCommandRunnerFunc) ([]string, error)
	RunAtlasHash    AtlasMigrateHashRunnerFunc
}

func normalizeMergeWorkingTreeBranchDependencies(deps MergeWorkingTreeBranchDependencies) MergeWorkingTreeBranchDependencies {
	if deps.CurrentBranch == nil {
		deps.CurrentBranch = gitx.GitCurrentBranch
	}
	if deps.CurrentCommit == nil {
		deps.CurrentCommit = gitx.GitShortCommit
	}
	if deps.RunGit == nil {
		deps.RunGit = gitx.GitCommandRunner
	}
	if deps.ConflictedFiles == nil {
		deps.ConflictedFiles = gitMergeConflictedFiles
	}
	if deps.RunAtlasHash == nil {
		deps.RunAtlasHash = runAtlasMigrateHash
	}
	return deps
}

// MergeWorkingTreeBranch fetches and merges Remote/TargetBranch with a merge commit. A conflict
// is left in place as *MergeConflictError, except one confined to atlas.sum, which is regenerated.
func MergeWorkingTreeBranch(ctx core.Context, root string, params MergeWorkingTreeBranchParams, deps MergeWorkingTreeBranchDependencies) (MergeWorkingTreeBranchResult, error) {
	target := strings.TrimSpace(params.TargetBranch)
	if target == "" {
		return MergeWorkingTreeBranchResult{}, fmt.Errorf("target branch is required")
	}
	remote := strings.TrimSpace(params.Remote)
	if remote == "" {
		remote = "origin"
	}
	deps = normalizeMergeWorkingTreeBranchDependencies(deps)

	current, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return MergeWorkingTreeBranchResult{}, fmt.Errorf("resolve current branch: %w", err)
	}

	remoteRef := remote + "/" + target
	ctx.TraceCommand(root, "git", "fetch", remote, target)
	ctx.TraceCommand(root, "git", "merge", "--no-edit", remoteRef)
	if ctx.DryRun {
		return MergeWorkingTreeBranchResult{Branch: current, TargetBranch: target, Remote: remote}, nil
	}

	return fetchAndMergeWorkingTree(ctx, root, target, remote, remoteRef, deps)
}

func fetchAndMergeWorkingTree(ctx core.Context, root, target, remote, remoteRef string, deps MergeWorkingTreeBranchDependencies) (MergeWorkingTreeBranchResult, error) {
	var fetchStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &fetchStderr, "fetch", remote, target); err != nil {
		return MergeWorkingTreeBranchResult{}, fmt.Errorf("git fetch: %w: %s", err, strings.TrimSpace(fetchStderr.String()))
	}

	var mergeStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &mergeStderr, "merge", "--no-edit", remoteRef); err != nil {
		conflicted, conflictErr := deps.ConflictedFiles(root, deps.RunGit)
		if conflictErr != nil || len(conflicted) == 0 {
			return MergeWorkingTreeBranchResult{}, fmt.Errorf("git merge: %w: %s", err, strings.TrimSpace(mergeStderr.String()))
		}
		remaining, resolveErr := resolveAtlasSumConflicts(root, deps.RunGit, deps.RunAtlasHash, conflicted)
		if resolveErr != nil {
			return MergeWorkingTreeBranchResult{}, fmt.Errorf("regenerate conflicted atlas.sum: %w", resolveErr)
		}
		if len(remaining) > 0 {
			return MergeWorkingTreeBranchResult{}, &MergeConflictError{TargetBranch: target, ConflictedFiles: remaining}
		}
		// Every conflict was a regenerated atlas.sum; finish the pending merge commit.
		var commitStderr bytes.Buffer
		if err := deps.RunGit(root, io.Discard, &commitStderr, "commit", "--no-edit"); err != nil {
			return MergeWorkingTreeBranchResult{}, fmt.Errorf("git commit --no-edit: %w: %s", err, strings.TrimSpace(commitStderr.String()))
		}
	}

	commit, err := deps.CurrentCommit(ctx, root)
	if err != nil {
		return MergeWorkingTreeBranchResult{}, fmt.Errorf("resolve merged commit: %w", err)
	}
	landedBranch, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return MergeWorkingTreeBranchResult{}, fmt.Errorf("resolve merged branch: %w", err)
	}

	return MergeWorkingTreeBranchResult{Branch: landedBranch, TargetBranch: target, Remote: remote, Commit: commit}, nil
}

// gitMergeConflictedFiles distinguishes a real conflict from other merge failures by what
// git left behind.
func gitMergeConflictedFiles(root string, runGit gitx.GitCommandRunnerFunc) ([]string, error) {
	var stdout, stderr bytes.Buffer
	if err := runGit(root, &stdout, &stderr, "diff", "--name-only", "--diff-filter=U"); err != nil {
		return nil, fmt.Errorf("git diff --diff-filter=U: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var files []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}
