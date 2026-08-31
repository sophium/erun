package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// GateMergeWorkingTreeParams squash-merges SourceBranch onto a fresh local
// checkout of TargetBranch — the git half of gating a merge queue promotion.
// The environment a review's merge queue promotes to MERGE runs this before
// `erun build`: fetch both branches, build the prospective squash merge onto
// the target's *current* remote tip (never the working tree's own existing
// branch, and never the source branch as its author left it), leaving one
// commit ready to gate before anything is pushed.
type GateMergeWorkingTreeParams struct {
	// SourceBranch is the review's source branch to squash in.
	SourceBranch string
	// TargetBranch is the branch the squash merge lands onto, checked out
	// fresh from its own current remote tip — not from the working tree's
	// checked-out branch, which this leaves behind.
	TargetBranch string
	// Message becomes the squash commit's message. This is the review's name
	// (AGENTS.md: "Review name is the squash merge message"), since that
	// commit is what ends up on TargetBranch if the gate passes.
	Message string
	// Remote is the git remote both branches are fetched from. Empty
	// defaults to "origin".
	Remote string
}

// GateMergeWorkingTreeResult is what actually landed.
type GateMergeWorkingTreeResult struct {
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
	Remote       string `json:"remote"`
	SourceCommit string `json:"sourceCommit"`
	Commit       string `json:"commit"`
}

// GateSquashConflictError reports a squash merge that left the working tree
// mid-conflict — the same distinct, named outcome MergeConflictError gives an
// ordinary merge, so a caller driving this unattended can tell "stop and
// record the GATE build as failed" apart from any other failure.
type GateSquashConflictError struct {
	SourceBranch    string
	TargetBranch    string
	ConflictedFiles []string
}

func (e *GateSquashConflictError) Error() string {
	return fmt.Sprintf(
		"squashing %s onto %s left %d file(s) conflicted: %s",
		e.SourceBranch, e.TargetBranch, len(e.ConflictedFiles), strings.Join(e.ConflictedFiles, ", "),
	)
}

// GateMergeWorkingTreeDependencies lets tests replace the git plumbing
// without a real remote, mirroring MergeWorkingTreeBranchDependencies.
type GateMergeWorkingTreeDependencies struct {
	WorkingTreeClean func(ctx Context, root string) (bool, error)
	ResolveRef       func(ctx Context, root, ref string) (string, error)
	RunGit           GitCommandRunnerFunc
	ConflictedFiles  func(root string, runGit GitCommandRunnerFunc) ([]string, error)
}

func normalizeGateMergeWorkingTreeDependencies(deps GateMergeWorkingTreeDependencies) GateMergeWorkingTreeDependencies {
	if deps.WorkingTreeClean == nil {
		deps.WorkingTreeClean = gitWorktreeClean
	}
	if deps.ResolveRef == nil {
		deps.ResolveRef = gitResolveRef
	}
	if deps.RunGit == nil {
		deps.RunGit = GitCommandRunner
	}
	if deps.ConflictedFiles == nil {
		deps.ConflictedFiles = gitMergeConflictedFiles
	}
	return deps
}

// GateMergeWorkingTree fetches Remote/TargetBranch and Remote/SourceBranch,
// checks out a local branch named TargetBranch at its own fresh remote tip,
// and squash-merges SourceBranch onto it as one commit carrying Message. A
// conflicted squash is reported as *GateSquashConflictError, distinct from
// any other failure, and the worktree is left exactly as git left it for the
// caller to resolve or abort — nothing here cleans that up automatically.
//
// The working tree must be clean before this runs: unlike the ordinary
// exec merge/commit/push primitives, this checks out a different local
// branch than whatever the tree is currently on, so an in-progress change
// left uncommitted there would otherwise be silently carried onto the
// prospective merge or lost.
func GateMergeWorkingTree(ctx Context, root string, params GateMergeWorkingTreeParams, deps GateMergeWorkingTreeDependencies) (GateMergeWorkingTreeResult, error) {
	source := strings.TrimSpace(params.SourceBranch)
	target := strings.TrimSpace(params.TargetBranch)
	if source == "" || target == "" {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("source branch and target branch are required")
	}
	if strings.TrimSpace(params.Message) == "" {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("message is required")
	}
	remote := strings.TrimSpace(params.Remote)
	if remote == "" {
		remote = "origin"
	}
	deps = normalizeGateMergeWorkingTreeDependencies(deps)

	// Checked even during --dry-run, the same discipline CommitWorkingTree and
	// PushWorkingTreeBranch apply to their own branch-mismatch check: it is a
	// read, not a mutation, and a dry run should refuse exactly what a real run
	// would refuse.
	clean, err := deps.WorkingTreeClean(ctx, root)
	if err != nil {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("check working tree is clean: %w", err)
	}
	if !clean {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("refusing to gate-merge: the working tree has uncommitted changes")
	}

	sourceRef := remote + "/" + source
	targetRef := remote + "/" + target
	ctx.TraceCommand(root, "git", "fetch", remote, target, source)
	ctx.TraceCommand(root, "git", "checkout", "-B", target, targetRef)
	ctx.TraceCommand(root, "git", "merge", "--squash", sourceRef)
	ctx.TraceCommand(root, "git", "commit", "-m", "<message>")
	if ctx.DryRun {
		return GateMergeWorkingTreeResult{TargetBranch: target, SourceBranch: source, Remote: remote}, nil
	}

	return fetchAndGateMergeWorkingTree(ctx, root, source, target, remote, sourceRef, targetRef, params.Message, deps)
}

// fetchAndGateMergeWorkingTree runs the mutating half of GateMergeWorkingTree,
// isolated so the validation and dry-run branching above it don't inflate
// that function's complexity.
func fetchAndGateMergeWorkingTree(ctx Context, root, source, target, remote, sourceRef, targetRef, message string, deps GateMergeWorkingTreeDependencies) (GateMergeWorkingTreeResult, error) {
	var fetchStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &fetchStderr, "fetch", remote, target, source); err != nil {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("git fetch: %w: %s", err, strings.TrimSpace(fetchStderr.String()))
	}

	sourceCommit, err := deps.ResolveRef(ctx, root, sourceRef)
	if err != nil {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("resolve %s: %w", sourceRef, err)
	}

	var checkoutStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &checkoutStderr, "checkout", "-B", target, targetRef); err != nil {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("git checkout: %w: %s", err, strings.TrimSpace(checkoutStderr.String()))
	}

	var mergeStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &mergeStderr, "merge", "--squash", sourceRef); err != nil {
		conflicted, conflictErr := deps.ConflictedFiles(root, deps.RunGit)
		if conflictErr == nil && len(conflicted) > 0 {
			return GateMergeWorkingTreeResult{}, &GateSquashConflictError{SourceBranch: source, TargetBranch: target, ConflictedFiles: conflicted}
		}
		return GateMergeWorkingTreeResult{}, fmt.Errorf("git merge --squash: %w: %s", err, strings.TrimSpace(mergeStderr.String()))
	}

	var commitStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &commitStderr, "commit", "-m", message); err != nil {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("git commit: %w: %s", err, strings.TrimSpace(commitStderr.String()))
	}

	commit, err := deps.ResolveRef(ctx, root, "HEAD")
	if err != nil {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("resolve squash merge commit: %w", err)
	}

	return GateMergeWorkingTreeResult{
		TargetBranch: target,
		SourceBranch: source,
		Remote:       remote,
		SourceCommit: sourceCommit,
		Commit:       commit,
	}, nil
}

// gitResolveRef resolves ref to its full commit hash.
func gitResolveRef(ctx Context, root, ref string) (string, error) {
	ctx.TraceCommand("", "git", "-C", root, "rev-parse", ref)
	output, err := Command("git", "-C", root, "rev-parse", ref).Output()
	if err != nil {
		if stderr := stderrFromExitError(err); stderr != "" {
			return "", fmt.Errorf("%w: %s", err, stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
