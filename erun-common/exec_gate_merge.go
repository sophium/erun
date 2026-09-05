package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// GateMergeSource is one branch to squash into the prospective merge, in the
// order it should land. Message becomes that branch's own squash commit
// message — normally the branch's review name (AGENTS.md: "Review name is
// the squash merge message") — since a landed branch becomes a real commit
// on TargetBranch if the gate passes.
type GateMergeSource struct {
	Branch  string
	Message string
}

// GateMergeWorkingTreeParams squash-merges Sources onto a fresh local
// checkout of TargetBranch, one commit per landed source — the git half of
// gating a merge queue promotion. The environment a review's merge queue
// promotes to MERGE runs this before `erun build`: fetch every branch, build
// the prospective merge onto the target's *current* remote tip (never the
// working tree's own existing branch, and never a source branch as its
// author left it), leaving a stack of squash commits ready to gate before
// anything is pushed.
//
// A single review's ordinary gate is the Sources-of-one case. Batching
// (Sources with more than one entry) is what lets a caller test whether
// several unmerged branches compile *together*, not just individually —
// something no repeated single-source call can see, since each call resets
// the working tree from the target's remote tip and discards whatever an
// earlier call landed.
type GateMergeWorkingTreeParams struct {
	// Sources are the branches to squash in, in order. At least one is
	// required.
	Sources []GateMergeSource
	// TargetBranch is the branch the squash merges land onto, checked out
	// fresh from its own current remote tip — not from the working tree's
	// checked-out branch, which this leaves behind.
	TargetBranch string
	// Remote is the git remote every branch is fetched from. Empty defaults
	// to "origin".
	Remote string
	// UnderLeaseID names an exclusive environment claim the caller already
	// holds, so a drive that took the environment for its own whole window is
	// not refused by its own claim. Empty for a caller holding nothing, which
	// is then refused by any claim it finds.
	UnderLeaseID string
}

// GateMergeLandedSource is one source branch that squash-merged cleanly.
type GateMergeLandedSource struct {
	SourceBranch string `json:"sourceBranch"`
	SourceCommit string `json:"sourceCommit"`
	// Commit is the squash commit that landed this branch.
	Commit string `json:"commit"`
}

// GateMergeSkippedSource is one source branch that did not land — a
// conflicting squash, or a branch this could not even resolve after
// fetching (e.g. deleted since the caller decided to batch it). The rest of
// the batch still gates; a skip here is reported, not fatal.
type GateMergeSkippedSource struct {
	SourceBranch    string   `json:"sourceBranch"`
	SourceCommit    string   `json:"sourceCommit,omitempty"`
	Reason          string   `json:"reason"`
	ConflictedFiles []string `json:"conflictedFiles,omitempty"`
}

// GateMergeWorkingTreeResult is what actually landed. Commit is the tip of
// the resulting stack — the last landed source's squash commit, or the
// target's own unchanged tip when every source was skipped.
type GateMergeWorkingTreeResult struct {
	TargetBranch string                   `json:"targetBranch"`
	Remote       string                   `json:"remote"`
	Commit       string                   `json:"commit"`
	Landed       []GateMergeLandedSource  `json:"landed"`
	Skipped      []GateMergeSkippedSource `json:"skipped,omitempty"`
}

// GateMergeWorkingTreeDependencies lets tests replace the git plumbing
// without a real remote, mirroring MergeWorkingTreeBranchDependencies.
type GateMergeWorkingTreeDependencies struct {
	WorkingTreeClean func(ctx Context, root string) (bool, error)
	ResolveRef       func(ctx Context, root, ref string) (string, error)
	RunGit           GitCommandRunnerFunc
	ConflictedFiles  func(root string, runGit GitCommandRunnerFunc) ([]string, error)
	// RunAtlasHash regenerates a conflicted atlas.sum instead of leaving it
	// for a human to resolve. See resolveAtlasSumConflicts.
	RunAtlasHash AtlasMigrateHashRunnerFunc
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
	if deps.RunAtlasHash == nil {
		deps.RunAtlasHash = runAtlasMigrateHash
	}
	return deps
}

// GateMergeWorkingTree fetches Remote/TargetBranch and every Remote/source in
// Params.Sources, checks out a local branch named TargetBranch at its own
// fresh remote tip, then squash-merges each source onto it in order, each as
// its own commit. A source whose squash conflicts is skipped — the merge is
// aborted, the conflict recorded in the result's Skipped list, and the next
// source is tried against the working tree as it stood before that attempt
// — rather than failing the whole batch, so one bad branch cannot turn an
// otherwise-clean batch dead. Sources is required to be non-empty.
//
// The working tree must be clean before this runs: unlike the ordinary
// exec merge/commit/push primitives, this checks out a different local
// branch than whatever the tree is currently on, so an in-progress change
// left uncommitted there would otherwise be silently carried onto the
// prospective merge or lost.
func GateMergeWorkingTree(ctx Context, root string, params GateMergeWorkingTreeParams, deps GateMergeWorkingTreeDependencies) (GateMergeWorkingTreeResult, error) {
	target := strings.TrimSpace(params.TargetBranch)
	if target == "" {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("target branch is required")
	}
	if err := validateGateMergeSources(params.Sources); err != nil {
		return GateMergeWorkingTreeResult{}, err
	}
	remote := strings.TrimSpace(params.Remote)
	if remote == "" {
		remote = "origin"
	}
	deps = normalizeGateMergeWorkingTreeDependencies(deps)

	// A gate-merge rewrites the environment's one shared worktree onto the
	// target branch, so two of them in flight at once are not merely slow —
	// they are wrong: one merge-queue drive reported pushing a commit that
	// belonged to the other batch's tree, and two pull requests were closed
	// against work that had not landed. Refused here, before the fetch, so a
	// drive that has lost the environment stops without touching the tree at
	// all. Checked in --dry-run too, for the same reason the clean check below
	// is: it is a read, and a dry run should refuse what a real run refuses.
	if err := EnsureEnvironmentNotExclusivelyHeld(ctx, "gate-merge", params.UnderLeaseID); err != nil {
		return GateMergeWorkingTreeResult{}, err
	}

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

	targetRef := remote + "/" + target
	fetchArgs := traceGateMergePlan(ctx, root, params.Sources, target, remote, targetRef)
	if ctx.DryRun {
		return GateMergeWorkingTreeResult{TargetBranch: target, Remote: remote}, nil
	}

	return fetchAndGateMergeWorkingTree(ctx, root, params.Sources, target, remote, fetchArgs, targetRef, deps)
}

// validateGateMergeSources checks that every source names a branch and a
// commit message, isolated so GateMergeWorkingTree's own branching stays low
// enough for the cyclomatic-complexity gate.
func validateGateMergeSources(sources []GateMergeSource) error {
	if len(sources) == 0 {
		return fmt.Errorf("at least one source branch is required")
	}
	for _, source := range sources {
		if strings.TrimSpace(source.Branch) == "" {
			return fmt.Errorf("source branch is required for every entry")
		}
		if strings.TrimSpace(source.Message) == "" {
			return fmt.Errorf("message is required for every source branch")
		}
	}
	return nil
}

// traceGateMergePlan emits the trace lines for the fetch, the checkout, and
// each source's squash-merge + commit pair, and returns the fetch argv so
// the real run doesn't have to rebuild it. Traced unconditionally (not only
// under --dry-run), matching every other exec primitive's audit contract.
func traceGateMergePlan(ctx Context, root string, sources []GateMergeSource, target, remote, targetRef string) []string {
	fetchArgs := []string{"fetch", remote, target}
	for _, source := range sources {
		fetchArgs = append(fetchArgs, source.Branch)
	}
	ctx.TraceCommand(root, "git", fetchArgs...)
	ctx.TraceCommand(root, "git", "checkout", "-B", target, targetRef)
	for _, source := range sources {
		ctx.TraceCommand(root, "git", "merge", "--squash", remote+"/"+source.Branch)
		ctx.TraceCommand(root, "git", "commit", "-m", "<message>")
	}
	return fetchArgs
}

// fetchAndGateMergeWorkingTree runs the mutating half of GateMergeWorkingTree,
// isolated so the validation and dry-run branching above it don't inflate
// that function's complexity.
func fetchAndGateMergeWorkingTree(ctx Context, root string, sources []GateMergeSource, target, remote string, fetchArgs []string, targetRef string, deps GateMergeWorkingTreeDependencies) (GateMergeWorkingTreeResult, error) {
	var fetchStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &fetchStderr, fetchArgs...); err != nil {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("git fetch: %w: %s", err, strings.TrimSpace(fetchStderr.String()))
	}

	var checkoutStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &checkoutStderr, "checkout", "-B", target, targetRef); err != nil {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("git checkout: %w: %s", err, strings.TrimSpace(checkoutStderr.String()))
	}

	result := GateMergeWorkingTreeResult{TargetBranch: target, Remote: remote}
	tip, err := deps.ResolveRef(ctx, root, "HEAD")
	if err != nil {
		return GateMergeWorkingTreeResult{}, fmt.Errorf("resolve target tip: %w", err)
	}
	result.Commit = tip

	for _, source := range sources {
		landed, skipped, err := gateMergeOneSource(ctx, root, source, remote, deps)
		if err != nil {
			return GateMergeWorkingTreeResult{}, err
		}
		if skipped != nil {
			result.Skipped = append(result.Skipped, *skipped)
			continue
		}
		result.Landed = append(result.Landed, *landed)
		result.Commit = landed.Commit
	}

	return result, nil
}

// gateMergeOneSource squash-merges and commits one source branch onto
// whatever the working tree currently holds. A conflicted squash is backed
// out with `git reset --hard HEAD` and reported as a skip rather than
// returned as an error, so the caller can keep trying the rest of the batch
// against a clean tree — `git merge --abort` is not available here, since
// `--squash` deliberately never records a MERGE_HEAD to abort. Any other
// git failure (a bad ref, a real I/O error) is fatal for the whole batch,
// since it says something is wrong beyond this one branch.
func gateMergeOneSource(ctx Context, root string, source GateMergeSource, remote string, deps GateMergeWorkingTreeDependencies) (*GateMergeLandedSource, *GateMergeSkippedSource, error) {
	sourceRef := remote + "/" + source.Branch
	sourceCommit, err := deps.ResolveRef(ctx, root, sourceRef)
	if err != nil {
		return nil, &GateMergeSkippedSource{
			SourceBranch: source.Branch,
			Reason:       fmt.Sprintf("could not resolve %s after fetch: %v", sourceRef, err),
		}, nil
	}

	var mergeStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &mergeStderr, "merge", "--squash", sourceRef); err != nil {
		conflicted, conflictErr := deps.ConflictedFiles(root, deps.RunGit)
		if conflictErr == nil && len(conflicted) > 0 {
			remaining, resolveErr := resolveAtlasSumConflicts(root, deps.RunGit, deps.RunAtlasHash, conflicted)
			if resolveErr != nil {
				return nil, nil, fmt.Errorf("regenerate conflicted atlas.sum for %s: %w", source.Branch, resolveErr)
			}
			if len(remaining) > 0 {
				var resetStderr bytes.Buffer
				if err := deps.RunGit(root, io.Discard, &resetStderr, "reset", "--hard", "HEAD"); err != nil {
					return nil, nil, fmt.Errorf("git reset --hard after a conflicted squash of %s: %w: %s", source.Branch, err, strings.TrimSpace(resetStderr.String()))
				}
				return nil, &GateMergeSkippedSource{
					SourceBranch:    source.Branch,
					SourceCommit:    sourceCommit,
					Reason:          fmt.Sprintf("squashing %s onto %s left %d file(s) conflicted", source.Branch, remote, len(remaining)),
					ConflictedFiles: remaining,
				}, nil
			}
			// Every conflict was an atlas.sum this regenerated and staged —
			// fall through and land this source like a clean squash.
		} else {
			return nil, nil, fmt.Errorf("git merge --squash %s: %w: %s", sourceRef, err, strings.TrimSpace(mergeStderr.String()))
		}
	}

	var commitStderr bytes.Buffer
	if err := deps.RunGit(root, io.Discard, &commitStderr, "commit", "-m", source.Message); err != nil {
		return nil, nil, fmt.Errorf("git commit %s: %w: %s", source.Branch, err, strings.TrimSpace(commitStderr.String()))
	}

	commit, err := deps.ResolveRef(ctx, root, "HEAD")
	if err != nil {
		return nil, nil, fmt.Errorf("resolve squash merge commit for %s: %w", source.Branch, err)
	}

	return &GateMergeLandedSource{SourceBranch: source.Branch, SourceCommit: sourceCommit, Commit: commit}, nil, nil
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
