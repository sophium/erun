package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Every agent task that clones the repo into a work directory leaves that
// clone behind once its job finishes -- nothing ever reclaimed it, so the
// work directory grows without bound. The obvious fix, sweeping the whole
// directory on a timer or `rm -rf`ing anything old, is destructive: a clone
// can hold commits that exist nowhere else (never pushed) or a dirty tree
// (never committed at all), and losing either is real work gone, not disk
// reclaimed. This file is the safety check that has to hold before any clone
// is removed, plus the one place it is actually acted on: the same job-finish
// hook that already checkpoints a dirty tree (job_worktree.go).
//
// "Finished" is answered by construction rather than by a timestamp or a
// directory scan: reclaim only ever runs once, from inside finishEnvironmentJob,
// at the exact moment the supervisor that owns this job observes its process
// exit. There is no window in which a running job's directory could be
// mistaken for an idle one, because nothing here ever looks at an arbitrary
// directory's mtime to guess whether work on it has stopped.

// workCloneUserHomeDir is the seam a test overrides to point workCloneRoot at
// a fixture home directory instead of the real one.
var workCloneUserHomeDir = os.UserHomeDir

// workCloneRoot resolves the directory that holds per-task agent clones
// (conventionally /home/erun/work), distinct from the tenant's own runtime
// repository (/home/erun/git/<tenant>). Reclaim only ever acts on a path
// under this root -- see isUnderWorkRoot -- which is what keeps the runtime
// repo itself unreachable no matter what a caller happens to set a job's Dir
// to.
func workCloneRoot(homeDir string) (string, error) {
	resolved := strings.TrimSpace(homeDir)
	if resolved == "" {
		home, err := workCloneUserHomeDir()
		if err != nil {
			return "", err
		}
		resolved = home
	}
	return filepath.Join(resolved, "work"), nil
}

// isUnderWorkRoot reports whether dir sits strictly inside root. root itself
// does not count -- reclaim only ever removes a clone inside the work root,
// never the work root directory.
func isUnderWorkRoot(root, dir string) bool {
	root = filepath.Clean(root)
	dir = filepath.Clean(dir)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// WorkCloneReclaimDecision is the outcome of checking whether an agent job's
// working directory is safe to remove now that the job has finished. It is
// its own dry-run report: computing it never mutates or deletes anything.
type WorkCloneReclaimDecision struct {
	Dir string
	// Reclaim is true only when nothing in dir would be lost by deleting it:
	// no uncommitted change (tracked or untracked) and every commit reachable
	// from HEAD already exists on a remote.
	Reclaim bool
	// Reason always explains the decision -- why it is safe to reclaim, or
	// why it was kept.
	Reason string
}

// DecideWorkCloneReclaim is the pure git-state check behind reclaim: given a
// candidate directory, is deleting it safe? It never deletes anything itself
// and never consults job records -- see reclaimAgentJobWorkClone for the
// caller that also confirms the job that owned dir has actually finished
// before acting on this.
//
// A missing answer is never read as safe. Every early return below keeps the
// clone (Reclaim stays false) unless a specific, checked condition proves
// deleting it loses nothing.
func DecideWorkCloneReclaim(ctx Context, dir string) WorkCloneReclaimDecision {
	decision := WorkCloneReclaimDecision{Dir: dir}

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		decision.Reason = "not a directory"
		return decision
	}
	inside, err := gitIsInsideWorkTree(ctx, dir)
	if err != nil || !inside {
		decision.Reason = "not a git working tree"
		return decision
	}
	changed, err := gitChangedWorkingTreeFiles(ctx, dir)
	if err != nil {
		decision.Reason = fmt.Sprintf("could not read working tree status: %v", err)
		return decision
	}
	if len(changed) > 0 {
		decision.Reason = fmt.Sprintf("working tree has %d uncommitted change(s) (e.g. %s), tracked or untracked", len(changed), changed[0])
		return decision
	}

	// Best-effort: a stale local view of the remote can only make this check
	// more conservative (it might miss a merge that landed since the last
	// fetch and keep a clone that was actually safe), never less safe, so a
	// fetch failure (no network, offline test fixture) is not fatal.
	gitFetchOrigin(ctx, dir)

	branch, err := GitCurrentBranch(ctx, dir)
	if err != nil {
		decision.Reason = fmt.Sprintf("could not read the current branch: %v", err)
		return decision
	}
	if branch == "HEAD" {
		return decideDetachedWorkCloneReclaim(ctx, dir, decision)
	}
	return decideBranchWorkCloneReclaim(ctx, dir, branch, decision)
}

// decideDetachedWorkCloneReclaim handles a clone left on a detached HEAD: safe
// only when the exact commit checked out is already reachable from some
// remote-tracking ref, since a detached commit is unreachable (and therefore
// as good as lost) the moment nothing else points at it.
func decideDetachedWorkCloneReclaim(ctx Context, dir string, decision WorkCloneReclaimDecision) WorkCloneReclaimDecision {
	sha, ok, err := gitResolvedRef(ctx, dir, "HEAD")
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

// decideBranchWorkCloneReclaim handles a clone on a real branch. The ordinary
// case is an upstream that already has every local commit; a repository that
// squash-merges (this one included) leaves a second, equally common end
// state for a landed branch -- the local commits are individually absent from
// upstream (or upstream is gone entirely) because the merge tool folded them
// into one commit on the default branch under a different SHA. That case is
// only ever treated as safe when it can be proven (see findSquashMergeCommit);
// otherwise the branch is kept exactly as an ordinary set of unpushed commits
// would be.
func decideBranchWorkCloneReclaim(ctx Context, dir, branch string, decision WorkCloneReclaimDecision) WorkCloneReclaimDecision {
	upstream, hasUpstream := resolveBranchUpstreamCandidate(ctx, dir, branch)
	if hasUpstream {
		ahead, err := gitCommitsAhead(ctx, dir, upstream, branch)
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

// resolveBranchUpstreamCandidate answers "what would branch be compared
// against to know it is safe" -- branch's configured @{u} when it has one,
// falling back to a same-named branch on origin otherwise. The fallback
// matters because a plain `git push origin branch:branch` (exactly what the
// job-finish checkpoint in job_worktree.go does) lands the commit on the
// remote without ever configuring `@{u}`, so a missing upstream is not proof
// nothing was pushed -- it is only proof that *if* something was pushed, it
// was not pushed the way `-u` would have recorded it.
func resolveBranchUpstreamCandidate(ctx Context, dir, branch string) (string, bool) {
	if upstream, ok := gitUpstreamRef(ctx, dir, branch); ok {
		return upstream, true
	}
	candidate := "origin/" + branch
	if _, ok, err := gitResolvedRef(ctx, dir, candidate); err == nil && ok {
		return candidate, true
	}
	return "", false
}

// findSquashMergeCommit proves a branch's exact changeset already landed on
// the remote's default branch under a different commit, the normal end state
// for a branch this repository squash-merged. Proof is a tree-hash match: a
// commit on the default branch whose full resulting tree is byte-identical to
// branch's own tip tree means every file branch would contribute is already
// present there, which is a stronger, exact claim than comparing patches
// (immune to context-line or whitespace differences a patch-id compare could
// be fooled by). Search is bounded to commits between the branches' merge
// base and the default branch's tip, so a large history costs one bounded
// walk rather than the whole repository log.
func findSquashMergeCommit(ctx Context, dir, branch string) (sha, defaultBranch string, ok bool) {
	defaultBranch, hasDefault := resolveDefaultBranchForMerge(ctx, dir, "origin")
	if !hasDefault {
		return "", "", false
	}
	defaultRef := "origin/" + defaultBranch
	if _, exists, err := gitResolvedRef(ctx, dir, defaultRef); err != nil || !exists {
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

// resolveDefaultBranchForMerge mirrors job_worktree.go's own
// environmentJobWorktreeIsProtectedBranch fallback: origin/HEAD's symref is
// authoritative when it is set, but a shallow or single-branch fetch (a real
// layout this repository's own lanes use) never sets it, so the common
// default names are checked directly against what actually exists on the
// remote rather than guessed blindly.
func resolveDefaultBranchForMerge(ctx Context, dir, remote string) (string, bool) {
	if def, ok := gitRemoteDefaultBranch(ctx, dir, remote); ok {
		return def, true
	}
	for _, candidate := range []string{"main", "master", "develop"} {
		if _, ok, err := gitResolvedRef(ctx, dir, remote+"/"+candidate); err == nil && ok {
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

// gitFetchOrigin refreshes the local view of the remote so the checks above
// see recently landed merges rather than whatever the clone last observed.
// Best-effort by design (see DecideWorkCloneReclaim): a failure here only
// ever pushes the decision toward keeping a clone, never toward losing one.
func gitFetchOrigin(ctx Context, dir string) {
	ctx.TraceCommand("", "git", "-C", dir, "fetch", "origin", "--prune")
	_ = Command("git", "-C", dir, "fetch", "origin", "--prune").Run()
}

func gitUpstreamRef(ctx Context, dir, branch string) (string, bool) {
	ref := branch + "@{u}"
	ctx.TraceCommand("", "git", "-C", dir, "rev-parse", "--abbrev-ref", ref)
	output, err := Command("git", "-C", dir, "rev-parse", "--abbrev-ref", ref).Output()
	if err != nil {
		return "", false
	}
	upstream := strings.TrimSpace(string(output))
	if upstream == "" {
		return "", false
	}
	return upstream, true
}

func gitCommitsAhead(ctx Context, dir, base, tip string) (int, error) {
	rangeSpec := base + ".." + tip
	ctx.TraceCommand("", "git", "-C", dir, "rev-list", "--count", rangeSpec)
	output, err := Command("git", "-C", dir, "rev-list", "--count", rangeSpec).Output()
	if err != nil {
		return 0, err
	}
	count, convErr := strconv.Atoi(strings.TrimSpace(string(output)))
	if convErr != nil {
		return 0, convErr
	}
	return count, nil
}

func gitMergeBase(ctx Context, dir, a, b string) (string, bool) {
	ctx.TraceCommand("", "git", "-C", dir, "merge-base", a, b)
	output, err := Command("git", "-C", dir, "merge-base", a, b).Output()
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(string(output))
	return sha, sha != ""
}

func gitTreeHash(ctx Context, dir, rev string) (string, bool) {
	sha, ok, err := gitResolvedRef(ctx, dir, rev+"^{tree}")
	if err != nil || !ok {
		return "", false
	}
	return sha, true
}

// gitCommitTree pairs a commit with the tree it produced, as read from `git
// log --format=%H %T`.
type gitCommitTree struct {
	sha  string
	tree string
}

func gitCommitTreesInRange(ctx Context, dir, rangeSpec string) ([]gitCommitTree, error) {
	ctx.TraceCommand("", "git", "-C", dir, "log", "--format=%H %T", rangeSpec)
	output, err := Command("git", "-C", dir, "log", "--format=%H %T", rangeSpec).Output()
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

// gitRemoteRefContainingCommit reports the first remote-tracking ref that
// contains sha, skipping a remote's symbolic HEAD line (e.g.
// "origin/HEAD -> origin/main") which names no ref of its own.
func gitRemoteRefContainingCommit(ctx Context, dir, sha string) (string, bool) {
	ctx.TraceCommand("", "git", "-C", dir, "branch", "-r", "--contains", sha)
	output, err := Command("git", "-C", dir, "branch", "-r", "--contains", sha).Output()
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

// reclaimAgentJobWorkClone runs once, at the same point captureAgentJobWorktreeOutcome
// runs (finishEnvironmentJob), and only for a job whose own run just ended
// cleanly (EnvironmentJobStateExited): never one still running, never one
// that left unresolved background work behind it (abandoned/gate-incomplete
// are excluded by that same check), and never anything outside the work root,
// which is what keeps the runtime repo untouchable regardless of what a
// caller set Dir to. Restricted to Kind == EnvironmentJobKindAgent, the kind
// the actual problem is about; a command job's Dir is routinely the runtime
// repo itself, and this never even inspects a command job's Dir.
func reclaimAgentJobWorkClone(job EnvironmentJob) (reclaimed bool, reason string) {
	if job.Kind != EnvironmentJobKindAgent || job.State != EnvironmentJobStateExited {
		return false, ""
	}
	dir := strings.TrimSpace(job.Dir)
	if dir == "" {
		return false, ""
	}
	root, err := workCloneRoot("")
	if err != nil || !isUnderWorkRoot(root, dir) {
		return false, ""
	}

	ctx := Context{Logger: NewLogger(VerbosityInfo)}
	decision := DecideWorkCloneReclaim(ctx, dir)
	if !decision.Reclaim {
		return false, decision.Reason
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, fmt.Sprintf("git state allowed reclaiming %s but removing it failed: %v", dir, err)
	}
	// os.RemoveAll returning nil is not, by itself, proof dir is gone -- it
	// also returns nil when a path never existed, and nothing in this package
	// guards against a future change trusting that return value alone. Stat
	// the path directly so "reclaimed" can never be reported while dir still
	// resolves on disk.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		if err == nil {
			return false, fmt.Sprintf("git state allowed reclaiming %s but it still exists on disk after removal", dir)
		}
		return false, fmt.Sprintf("git state allowed reclaiming %s but its post-removal state could not be confirmed: %v", dir, err)
	}
	return true, ""
}
