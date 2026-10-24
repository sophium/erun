package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// workCloneRemoveAll is a test seam: a chmod fixture proves nothing under root.
var workCloneRemoveAll = os.RemoveAll

// isUnderWorkRoot reports whether dir is strictly inside root (root itself excluded).
func isUnderWorkRoot(root, dir string) bool {
	root = filepath.Clean(root)
	dir = filepath.Clean(dir)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// WorkCloneReclaimDecision says whether a finished agent job's clone is safe to remove.
type WorkCloneReclaimDecision struct {
	Dir string
	// Reclaim is true only with no uncommitted change and every HEAD commit on a remote.
	Reclaim bool
	Reason  string
}

// DecideWorkCloneReclaim reports whether deleting dir loses nothing; it never
// deletes, and any unanswered check keeps the clone.
func DecideWorkCloneReclaim(ctx core.Context, dir string) WorkCloneReclaimDecision {
	decision := WorkCloneReclaimDecision{Dir: dir}

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		decision.Reason = "not a directory"
		return decision
	}
	if !gitIsInsideWorkTree(ctx, dir) {
		decision.Reason = "not a git working tree"
		return decision
	}
	changed, err := gitx.GitChangedWorkingTreeFiles(ctx, dir)
	if err != nil {
		decision.Reason = fmt.Sprintf("could not read working tree status: %v", err)
		return decision
	}
	if len(changed) > 0 {
		decision.Reason = fmt.Sprintf("working tree has %d uncommitted change(s) (e.g. %s), tracked or untracked", len(changed), changed[0])
		return decision
	}

	// Best-effort: a stale remote view only makes the check more conservative.
	gitFetchOrigin(ctx, dir)

	branch, err := gitx.GitCurrentBranch(ctx, dir)
	if err != nil {
		decision.Reason = fmt.Sprintf("could not read the current branch: %v", err)
		return decision
	}
	if branch == "HEAD" {
		return decideDetachedWorkCloneReclaim(ctx, dir, decision)
	}
	return decideBranchWorkCloneReclaim(ctx, dir, branch, decision)
}

// decideDetachedWorkCloneReclaim is safe only when HEAD is reachable from a remote ref.
func decideDetachedWorkCloneReclaim(ctx core.Context, dir string, decision WorkCloneReclaimDecision) WorkCloneReclaimDecision {
	sha, ok, err := shared.GitResolvedRef(ctx, dir, "HEAD")
	if err != nil || !ok {
		decision.Reason = "detached HEAD and its commit could not be resolved"
		return decision
	}
	if ref, ok := gitRemoteRefContainingCommit(ctx, dir, sha); ok {
		decision.Reclaim = true
		decision.Reason = fmt.Sprintf("detached HEAD at %s is already reachable from remote ref %s", shortSHA(sha), ref)
		return decision
	}
	decision.Reason = fmt.Sprintf("detached HEAD at %s is not reachable from any remote-tracking ref", shortSHA(sha))
	return decision
}

// decideBranchWorkCloneReclaim also accepts a proven squash-merge, since squashed
// commits are absent from upstream under their own SHAs.
func decideBranchWorkCloneReclaim(ctx core.Context, dir, branch string, decision WorkCloneReclaimDecision) WorkCloneReclaimDecision {
	upstream, hasUpstream := shared.ResolveBranchUpstreamCandidate(ctx, dir, branch)
	if hasUpstream {
		ahead, err := shared.GitCommitsAhead(ctx, dir, upstream, branch)
		if err != nil {
			decision.Reason = fmt.Sprintf("could not compare %s against its upstream %s: %v", branch, upstream, err)
			return decision
		}
		if ahead == 0 {
			decision.Reclaim = true
			decision.Reason = fmt.Sprintf("branch %s is fully pushed to %s", branch, upstream)
			return decision
		}
		if sha, mergedBranch, ok := findSquashMergeCommit(ctx, dir, branch); ok {
			decision.Reclaim = true
			decision.Reason = fmt.Sprintf("%d commit(s) on %s are unpushed to %s, but that changeset is already merged into %s as %s",
				ahead, branch, upstream, mergedBranch, shortSHA(sha))
			return decision
		}
		decision.Reason = fmt.Sprintf("%d commit(s) on %s are ahead of %s and were not found merged anywhere else", ahead, branch, upstream)
		return decision
	}

	if sha, mergedBranch, ok := findSquashMergeCommit(ctx, dir, branch); ok {
		decision.Reclaim = true
		decision.Reason = fmt.Sprintf("branch %s has no upstream, but its changeset is already merged into %s as %s", branch, mergedBranch, shortSHA(sha))
		return decision
	}
	decision.Reason = fmt.Sprintf("branch %s has no upstream configured, and its commits were not found merged anywhere else", branch)
	return decision
}

// findSquashMergeCommit finds a default-branch commit whose tree equals branch's
// tip tree, searching only from the merge base.
func findSquashMergeCommit(ctx core.Context, dir, branch string) (sha, defaultBranch string, ok bool) {
	defaultBranch, hasDefault := resolveDefaultBranchForMerge(ctx, dir, "origin")
	if !hasDefault {
		return "", "", false
	}
	defaultRef := "origin/" + defaultBranch
	if _, exists, err := shared.GitResolvedRef(ctx, dir, defaultRef); err != nil || !exists {
		return "", "", false
	}

	tipTree, ok := gitTreeHash(ctx, dir, branch)
	if !ok || tipTree == "" {
		return "", "", false
	}

	rangeSpec := defaultRef
	if base, ok := gitMergeBase(ctx, dir, branch, defaultRef); ok {
		rangeSpec = base + ".." + defaultRef
	}
	commits, err := gitCommitTreesInRange(ctx, dir, rangeSpec)
	if err != nil {
		return "", "", false
	}
	for _, commit := range commits {
		if commit.tree == tipTree {
			return commit.sha, defaultBranch, true
		}
	}
	return "", "", false
}

// resolveDefaultBranchForMerge falls back to common names that exist on the
// remote when origin/HEAD is unset (shallow or single-branch fetches).
func resolveDefaultBranchForMerge(ctx core.Context, dir, remote string) (string, bool) {
	if def, ok := gitx.GitRemoteDefaultBranch(ctx, dir, remote); ok {
		return def, true
	}
	for _, candidate := range []string{"main", "master", "develop"} {
		if _, ok, err := shared.GitResolvedRef(ctx, dir, remote+"/"+candidate); err == nil && ok {
			return candidate, true
		}
	}
	return "", false
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// gitFetchOrigin is best-effort; failure only biases toward keeping a clone.
func gitFetchOrigin(ctx core.Context, dir string) {
	ctx.TraceCommand("", "git", "-C", dir, "fetch", "origin", "--prune")
	_ = core.Command("git", "-C", dir, "fetch", "origin", "--prune").Run()
}

func gitMergeBase(ctx core.Context, dir, a, b string) (string, bool) {
	ctx.TraceCommand("", "git", "-C", dir, "merge-base", a, b)
	output, err := core.Command("git", "-C", dir, "merge-base", a, b).Output()
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(string(output))
	return sha, sha != ""
}

func gitTreeHash(ctx core.Context, dir, rev string) (string, bool) {
	sha, ok, err := shared.GitResolvedRef(ctx, dir, rev+"^{tree}")
	if err != nil || !ok {
		return "", false
	}
	return sha, true
}

type gitCommitTree struct {
	sha  string
	tree string
}

func gitCommitTreesInRange(ctx core.Context, dir, rangeSpec string) ([]gitCommitTree, error) {
	ctx.TraceCommand("", "git", "-C", dir, "log", "--format=%H %T", rangeSpec)
	output, err := core.Command("git", "-C", dir, "log", "--format=%H %T", rangeSpec).Output()
	if err != nil {
		return nil, err
	}
	var commits []gitCommitTree
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		commits = append(commits, gitCommitTree{sha: fields[0], tree: fields[1]})
	}
	return commits, nil
}

// gitRemoteRefContainingCommit skips symbolic "origin/HEAD -> ..." lines.
func gitRemoteRefContainingCommit(ctx core.Context, dir, sha string) (string, bool) {
	ctx.TraceCommand("", "git", "-C", dir, "branch", "-r", "--contains", sha)
	output, err := core.Command("git", "-C", dir, "branch", "-r", "--contains", sha).Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "->") {
			continue
		}
		return line, true
	}
	return "", false
}

// reclaimAgentJobWorkClone removes a cleanly exited agent job's clone, only
// inside the work root, so the runtime repo is never touched.
func reclaimAgentJobWorkClone(job shared.EnvironmentJob) (reclaimed bool, reason string) {
	if job.Kind != EnvironmentJobKindAgent || job.State != shared.EnvironmentJobStateExited {
		return false, ""
	}
	dir := strings.TrimSpace(job.Dir)
	if dir == "" {
		return false, ""
	}
	root, err := shared.WorkCloneRoot("")
	if err != nil || !isUnderWorkRoot(root, dir) {
		return false, ""
	}

	ctx := core.Context{Logger: core.NewLogger(core.VerbosityInfo)}
	decision := DecideWorkCloneReclaim(ctx, dir)
	if !decision.Reclaim {
		return false, decision.Reason
	}
	if err := workCloneRemoveAll(dir); err != nil {
		return false, fmt.Sprintf("git state allowed reclaiming %s but removing it failed: %v", dir, err)
	}
	// RemoveAll returns nil for a missing path too; confirm the dir is gone.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		if err == nil {
			return false, fmt.Sprintf("git state allowed reclaiming %s but it still exists on disk after removal", dir)
		}
		return false, fmt.Sprintf("git state allowed reclaiming %s but its post-removal state could not be confirmed: %v", dir, err)
	}
	return true, ""
}
