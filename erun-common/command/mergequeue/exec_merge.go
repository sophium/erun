package mergequeue

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/gitcmd"
	"github.com/sophium/erun/erun-common/infra/gitx"
)

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
	CurrentBranch   gitcmd.GitValueResolverFunc
	CurrentCommit   gitcmd.GitValueResolverFunc
	RunGit          gitcmd.GitCommandRunnerFunc
	ConflictedFiles func(root string, runGit gitcmd.GitCommandRunnerFunc) ([]string, error)
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
func MergeWorkingTreeBranch(ctx core.Context, root string, params model.MergeWorkingTreeBranchParams, deps MergeWorkingTreeBranchDependencies) (model.MergeWorkingTreeBranchResult, error) {
	target := strings.TrimSpace(params.TargetBranch)
	if target == "" {
		return model.MergeWorkingTreeBranchResult{}, fmt.Errorf("target branch is required")
	}
	remote := strings.TrimSpace(params.Remote)
	if remote == "" {
		remote = "origin"
	}
	deps = normalizeMergeWorkingTreeBranchDependencies(deps)

	current, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return model.MergeWorkingTreeBranchResult{}, fmt.Errorf("resolve current branch: %w", err)
	}

	remoteRef := remote + "/" + target
	ctx.TraceCommand(root, "git", "fetch", remote, target)
	ctx.TraceCommand(root, "git", "merge", "--no-edit", remoteRef)
	if ctx.DryRun {
		return model.MergeWorkingTreeBranchResult{Branch: current, TargetBranch: target, Remote: remote}, nil
	}

	return fetchAndMergeWorkingTree(ctx, root, target, remote, remoteRef, deps)
}

func fetchAndMergeWorkingTree(ctx core.Context, root, target, remote, remoteRef string, deps MergeWorkingTreeBranchDependencies) (model.MergeWorkingTreeBranchResult, error) {
	var fetchStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &fetchStderr, "fetch", remote, target); err != nil {
		return model.MergeWorkingTreeBranchResult{}, fmt.Errorf("git fetch: %w: %s", err, strings.TrimSpace(fetchStderr.String()))
	}

	var mergeStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &mergeStderr, "merge", "--no-edit", remoteRef); err != nil {
		conflicted, conflictErr := deps.ConflictedFiles(root, deps.RunGit)
		if conflictErr != nil || len(conflicted) == 0 {
			return model.MergeWorkingTreeBranchResult{}, fmt.Errorf("git merge: %w: %s", err, strings.TrimSpace(mergeStderr.String()))
		}
		remaining, resolveErr := resolveAtlasSumConflicts(root, deps.RunGit, deps.RunAtlasHash, conflicted)
		if resolveErr != nil {
			return model.MergeWorkingTreeBranchResult{}, fmt.Errorf("regenerate conflicted atlas.sum: %w", resolveErr)
		}
		if len(remaining) > 0 {
			return model.MergeWorkingTreeBranchResult{}, &MergeConflictError{TargetBranch: target, ConflictedFiles: remaining}
		}
		// Every conflict was a regenerated atlas.sum; finish the pending merge commit.
		var commitStderr bytes.Buffer
		if err := deps.RunGit(root, io.Discard, &commitStderr, "commit", "--no-edit"); err != nil {
			return model.MergeWorkingTreeBranchResult{}, fmt.Errorf("git commit --no-edit: %w: %s", err, strings.TrimSpace(commitStderr.String()))
		}
	}

	commit, err := deps.CurrentCommit(ctx, root)
	if err != nil {
		return model.MergeWorkingTreeBranchResult{}, fmt.Errorf("resolve merged commit: %w", err)
	}
	landedBranch, err := deps.CurrentBranch(ctx, root)
	if err != nil {
		return model.MergeWorkingTreeBranchResult{}, fmt.Errorf("resolve merged branch: %w", err)
	}

	return model.MergeWorkingTreeBranchResult{Branch: landedBranch, TargetBranch: target, Remote: remote, Commit: commit}, nil
}

// gitMergeConflictedFiles distinguishes a real conflict from other merge failures by what
// git left behind.
func gitMergeConflictedFiles(root string, runGit gitcmd.GitCommandRunnerFunc) ([]string, error) {
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
