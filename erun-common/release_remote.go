package eruncommon

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// A release establishes that it can fast-forward exactly once, in sync-remote,
// and then spends the whole build before it pushes anything. Nothing
// re-establishes it in between, so a base branch that moved while the release was
// working is discovered at the final push — after the images, the charts and the
// tag are already public. That leaves the registry holding a version the
// repository has no commits for, and VERSION still on the version just published,
// which is the one state the stage ordering exists to prevent.
//
// This file holds both halves of the answer: the branch is re-read immediately
// before the build spends anything, and the final push absorbs a move that
// happened after that read rather than failing on it.

// releasePushStageName is the stage that makes the release's generated commits
// public. Named once because both the stage constructors and the push's
// rebase-and-retry have to agree on it.
const releasePushStageName = "push"

// releasePushRebaseAttempts bounds the rebase-and-retry. The commits this push
// carries are generated, `[skip ci]`, and conflict-free by construction, so one
// retry absorbs the ordinary race and a second covers a branch that moved again
// while the first was running. Past that, something other than a merge is going
// on and the operator should see the push's own error instead of a loop.
const releasePushRebaseAttempts = 2

// ensureReleaseBaseBranchUnmoved refuses a release whose base branch has moved
// since sync-remote rebased onto it, while the refusal still costs nothing.
//
// It runs immediately before the publish because that is where the spend starts:
// two seconds of git instead of a multi-architecture build, every chart, a public
// tag, and only then a push that cannot land. Same class of upfront refusal as an
// unusable registry credential or a release whose images nothing publishes.
//
// A remote it cannot read is not an answer: an inconclusive check lets the
// release proceed exactly as it does today. The point is to turn a *known*
// failure into an immediate one, not to invent a new way for a release to refuse
// to start.
func ensureReleaseBaseBranchUnmoved(ctx Context, spec ReleaseSpec, runGit GitCommandRunnerFunc) error {
	branch := strings.TrimSpace(spec.Branch)
	if branch == "" {
		return nil
	}

	ctx.TraceCommand(spec.ProjectRoot, "git", "fetch", "origin", branch)
	ctx.TraceCommand(spec.ProjectRoot, "git", "rev-list", "--count", "HEAD..FETCH_HEAD")
	if ctx.DryRun {
		ctx.Trace("release: a base branch that moved since sync-remote refuses the release here, before the build spends anything")
		return nil
	}

	ahead, known := releaseBaseBranchAhead(spec.ProjectRoot, branch, runGit)
	switch {
	case !known:
		ctx.Trace("release: origin/" + branch + " could not be read; the moved-branch check is left to the push")
	case ahead == 0:
		ctx.Trace("release: origin/" + branch + " has not moved since sync-remote")
	default:
		return movedReleaseBaseBranchError(spec, branch, ahead)
	}
	return nil
}

// releaseBaseBranchAhead counts the commits origin/<branch> carries that this
// release is not built on. It reads the remote itself rather than a
// remote-tracking ref, which is only what sync-remote already believed; the
// fetch's own chatter is discarded because the release's trace lines already say
// what was asked and what came back.
func releaseBaseBranchAhead(projectRoot, branch string, runGit GitCommandRunnerFunc) (int, bool) {
	if err := runGit(projectRoot, io.Discard, io.Discard, "fetch", "origin", branch); err != nil {
		return 0, false
	}
	output, err := Command("git", "-C", projectRoot, "rev-list", "--count", "HEAD..FETCH_HEAD").Output()
	if err != nil {
		return 0, false
	}
	ahead, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, false
	}
	return ahead, true
}

func movedReleaseBaseBranchError(spec ReleaseSpec, branch string, ahead int) error {
	return fmt.Errorf("origin/%s has moved since this release rebased onto it: it carries %d commit(s) this release is not built on.\n"+
		"Building now would publish %s and push its tag, and only then fail at the final push with everything already public.\n"+
		"Nothing is published yet, so absorb the move and re-run:\n"+
		"  git -C %s pull --rebase origin %s\n"+
		"  erun release --force\n"+
		"(--force recreates the local v%s tag this run already made, which the rebase leaves behind.)",
		branch, ahead, spec.Version, spec.ProjectRoot, branch, spec.Version)
}

// isReleaseBranchPush identifies the release's own final push — the one that
// makes the generated commits public. A tag push is deliberately not it: a moved
// branch cannot reject a tag, so rebasing would not explain whatever did.
func isReleaseBranchPush(stage ReleaseStage, command ReleaseCommandSpec) bool {
	return stage.Name == releasePushStageName &&
		command.Name == "git" &&
		len(command.Args) > 0 &&
		command.Args[0] == "push"
}

// runReleaseBranchPush pushes the release's generated commits, absorbing a base
// branch that moved while the release was building.
//
// By the time this runs the version is public: its images, charts and tag all
// resolve. Failing here leaves the repository without the commits that record the
// release — VERSION still on the version just published, so a plain `erun build`
// would re-mint it — which is exactly the inconsistency the publish-before-tag
// ordering exists to prevent. The commits are generated, `[skip ci]`, and
// conflict-free by construction, so rebasing onto the branch as it stands now and
// retrying is strictly better than stopping. It stays bounded: a rebase that
// cannot apply, or a push that keeps failing, still surfaces the push's own error.
func runReleaseBranchPush(ctx Context, spec ReleaseSpec, command ReleaseCommandSpec, runGit GitCommandRunnerFunc) error {
	branch := strings.TrimSpace(spec.Branch)
	err := runGit(command.Dir, ctx.Stdout, ctx.Stderr, command.Args...)
	for attempt := 1; err != nil && branch != "" && attempt <= releasePushRebaseAttempts; attempt++ {
		ctx.Info(fmt.Sprintf("release: push rejected; origin/%s moved during the release, rebasing onto it and retrying (%d/%d)", branch, attempt, releasePushRebaseAttempts))
		if rebaseErr := rebaseReleaseOntoRemoteBranch(ctx, command.Dir, branch, runGit); rebaseErr != nil {
			return fmt.Errorf("%w\nrebasing onto origin/%s to absorb the move failed: %v\nversion %s is already published, so rebase the release's own commits onto origin/%s by hand and push them",
				err, branch, rebaseErr, spec.Version, branch)
		}
		err = runGit(command.Dir, ctx.Stdout, ctx.Stderr, releaseBranchPushArgs(spec, command)...)
	}
	return err
}

// releaseBranchPushArgs is the retried push. A rebase rewrites the commit the
// release tag points at, so --follow-tags no longer considers the tag reachable
// and would silently leave it behind. The tag object still names the version that
// is already published, so the retry pushes it by name; a tag origin already has
// makes that a no-op.
func releaseBranchPushArgs(spec ReleaseSpec, command ReleaseCommandSpec) []string {
	version := strings.TrimSpace(spec.Version)
	if version == "" {
		return command.Args
	}
	return append(append([]string{}, command.Args...), "v"+version)
}

// rebaseReleaseOntoRemoteBranch replays the release's generated commits on top of
// the base branch as it stands now. A rebase that cannot apply is aborted rather
// than left half-done, so a failed absorb hands back the checkout the release was
// working in, and its output rides on the error instead of the release's streams
// (git's rebase chatter is version-dependent and says nothing on success).
func rebaseReleaseOntoRemoteBranch(ctx Context, projectRoot, branch string, runGit GitCommandRunnerFunc) error {
	ctx.TraceCommand(projectRoot, "git", "fetch", "origin", branch)
	if err := runGit(projectRoot, io.Discard, io.Discard, "fetch", "origin", branch); err != nil {
		return err
	}

	ctx.TraceCommand(projectRoot, "git", "rebase", "FETCH_HEAD")
	var output strings.Builder
	if err := runGit(projectRoot, &output, &output, "rebase", "FETCH_HEAD"); err != nil {
		ctx.TraceCommand(projectRoot, "git", "rebase", "--abort")
		_ = runGit(projectRoot, io.Discard, io.Discard, "rebase", "--abort")
		if details := strings.TrimSpace(output.String()); details != "" {
			return fmt.Errorf("%w: %s", err, details)
		}
		return err
	}
	return nil
}
