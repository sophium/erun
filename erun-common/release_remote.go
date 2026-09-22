package eruncommon

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
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
//
// Everything here that describes what the registry holds is written for the
// run that publishes, because `erun build --release` is the run that reaches
// the final push with its images and charts verified. `erun release` reaches
// the same push having published nothing (see ReleaseSpec.ArtifactsPublished),
// so each of those reports is conditioned on that field rather than assuming
// the publishing shape.

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
		"  erun build --release --force\n"+
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
// By the time this runs the version's tag is public, and so are its images and
// charts when the run published any. Failing here leaves the repository without
// the commits that record the release — VERSION still on the version just
// published, so a plain `erun build` would re-mint it — which is exactly the
// inconsistency the publish-before-tag ordering exists to prevent. A
// source-control-only release reaches this with nothing published, so for it
// the tag is the whole of what is public. The commits are generated, `[skip ci]`, and
// conflict-free by construction, so rebasing onto the branch as it stands now and
// retrying is strictly better than stopping. It stays bounded: a rebase that
// cannot apply, or a push that keeps failing, still surfaces the push's own error.
func runReleaseBranchPush(ctx Context, spec ReleaseSpec, command ReleaseCommandSpec, runGit GitCommandRunnerFunc) error {
	branch := strings.TrimSpace(spec.Branch)
	var pushOutput strings.Builder
	err := runGit(command.Dir, ctx.Stdout, releasePushStderrWriter(ctx, &pushOutput), command.Args...)
	for attempt := 1; err != nil && branch != "" && attempt <= releasePushRebaseAttempts; attempt++ {
		rejections := parseReleasePushRejections(pushOutput.String())
		if !releasePushRejectedTheMovedBaseBranch(rejections, branch) {
			return releasePushRejectedError(spec, rejections, err)
		}
		ctx.Info(fmt.Sprintf("release: push rejected; origin/%s moved during the release, rebasing onto it and retrying (%d/%d)", branch, attempt, releasePushRebaseAttempts))
		if rebaseErr := rebaseReleaseOntoRemoteBranch(ctx, command.Dir, branch, runGit); rebaseErr != nil {
			return fmt.Errorf("%w\nrebasing onto origin/%s to absorb the move failed: %v\n%s",
				err, branch, rebaseErr, releaseRebaseFailedRecovery(spec, branch))
		}
		if repointErr := repointReleaseTagIfRebased(ctx, spec, command.Dir, runGit); repointErr != nil {
			return fmt.Errorf("%w\nrebasing onto origin/%s absorbed the move, but re-pointing the already-public release tag failed: %v\n%s",
				err, branch, repointErr, releaseRepointFailedRecovery(spec))
		}
		pushOutput.Reset()
		err = runGit(command.Dir, ctx.Stdout, releasePushStderrWriter(ctx, &pushOutput), releaseBranchPushArgs(spec, command)...)
	}
	return err
}

// releasePushStderrWriter streams git's push output to the operator while
// keeping a copy for the rejection parse. A caller with no stderr sink (tests,
// embedding callers) would make io.MultiWriter panic on a nil writer, so the
// capture stands alone there.
func releasePushStderrWriter(ctx Context, capture *strings.Builder) io.Writer {
	if ctx.Stderr == nil {
		return capture
	}
	return io.MultiWriter(ctx.Stderr, capture)
}

// releasePushRejection is one ref git refused to update, as git reported it on
// the `! [rejected]  <from> -> <to> (reason)` line. The local side is the ref
// the push named, which is the only thing a remediation may be scoped to; the
// reason is carried through so an operator reads git's own words.
type releasePushRejection struct {
	Ref    string
	Reason string
}

// releasePushRejectionLine matches git's rejected-ref line. The reason is
// optional because not every rejection carries one.
var releasePushRejectionLine = regexp.MustCompile(`(?m)^\s*!\s*\[rejected\]\s+(\S+)\s*->\s*\S+\s*(?:\(([^)]*)\))?\s*$`)

func parseReleasePushRejections(output string) []releasePushRejection {
	var rejections []releasePushRejection
	for _, match := range releasePushRejectionLine.FindAllStringSubmatch(output, -1) {
		ref := strings.TrimSpace(match[1])
		if ref == "" || ref == "(none)" {
			continue
		}
		rejections = append(rejections, releasePushRejection{Ref: ref, Reason: strings.TrimSpace(match[2])})
	}
	return rejections
}

// releasePushRejectedTheMovedBaseBranch reports whether this rejection is the
// one case the rebase-and-retry explains: the base branch itself was rejected,
// and because it moved.
//
// A push carries several refs, and only the base branch can be repaired by
// rebasing the base branch. A develop rejected as a non-fast-forward is a
// different failure — the branch diverged, and rebasing main onto an origin/main
// that never moved is a no-op that spends every retry without ever fetching or
// merging origin/develop. Anything unrecognised, including a
// rejection whose reason is a hook rather than a moved branch, is left to the
// operator with git's own reason attached.
func releasePushRejectedTheMovedBaseBranch(rejections []releasePushRejection, branch string) bool {
	if branch == "" || len(rejections) == 0 {
		return false
	}
	for _, rejection := range rejections {
		if rejection.Ref != branch || !releasePushReasonIsMovedBranch(rejection.Reason) {
			return false
		}
	}
	return true
}

func releasePushReasonIsMovedBranch(reason string) bool {
	reason = strings.ToLower(reason)
	return strings.Contains(reason, "fast-forward") ||
		strings.Contains(reason, "fetch first") ||
		strings.Contains(reason, "stale info")
}

// releasePushRejectedError names the ref git actually rejected, what the
// registry holds for this version, and what is now missing, instead of letting
// a bare failure stand in for it.
//
// By the time this push runs the version's tag is on the remote, and its images
// and charts are there too when the run published any; the GitHub Release
// object is created after this push, so a push failure also leaves it absent.
// The unqualified failure that used to be reported here reads as the
// pre-publication shape "Recovering an interrupted release" covers, and acting
// on that shape would delete a public tag and reset a branch that already
// landed. So the error says which ref did not land, why git refused
// it, and that the tag must not be deleted.
//
// The registry half is the run's own to state, and spec.ArtifactsPublished is
// what it is stated from: `erun release` marks source control only, so claiming
// its images and charts verified on a registry sends its operator past the
// publish this version still needs and onto a version no environment can
// deploy.
func releasePushRejectedError(spec ReleaseSpec, rejections []releasePushRejection, cause error) error {
	// described carries git's own reason for the reader; names is the bare ref
	// for the recovery command, where a parenthesised reason would not be a
	// ref that can be reconciled.
	described := make([]string, 0, len(rejections))
	names := make([]string, 0, len(rejections))
	for _, rejection := range rejections {
		names = append(names, rejection.Ref)
		if rejection.Reason != "" {
			described = append(described, fmt.Sprintf("%s (%s)", rejection.Ref, rejection.Reason))
			continue
		}
		described = append(described, rejection.Ref)
	}
	refs := strings.Join(described, ", ")
	if refs == "" {
		refs = "a ref git did not name"
	}
	unlanded := strings.Join(names, ", ")
	if unlanded == "" {
		unlanded = refs
	}
	version := strings.TrimSpace(spec.Version)
	published := fmt.Sprintf("Everything else this release publishes is already public: its images and charts verified on the registry and tag v%s is on the remote. What did not land is %s.", version, refs)
	recovery := fmt.Sprintf("  reconcile %s with its remote, push it, then create the GitHub Release for the existing tag v%s", unlanded, version)
	if !spec.ArtifactsPublished {
		// Nothing in this run built or published an artifact, so the registry
		// holds nothing for this version and the recovery has a publish of its
		// own to name: the tag is public, and a tag with no artifacts behind it
		// is a version no environment can deploy (erun deploy never builds).
		published = fmt.Sprintf("This release marks source control only: it built and published no images or charts, so version %s is on no registry. What landed is source control; what did not land is %s.", version, refs)
		recovery = fmt.Sprintf("  reconcile %s with its remote and push it\n"+
			"  publish the version's images and charts, which this release never did: erun push --version %s\n"+
			"  then create the GitHub Release for the existing tag v%s", unlanded, version, version)
	}
	return fmt.Errorf("%w\nrelease: the push was rejected for %s, which is not origin/%s having moved during the release, so nothing is rebased or retried.\n"+
		published+"\n"+
		"The GitHub Release object for v%s is created after this push, so it does not exist yet either.\n"+
		"Recover by hand — do not delete tag v%s and do not reset %s, both are already public:\n"+
		"  git -C %s fetch origin\n"+
		recovery,
		cause, refs, spec.Branch, version, version, spec.Branch, spec.ProjectRoot)
}

// releaseRebaseFailedRecovery says what is left to do when the retry's rebase
// onto the moved branch could not apply. It restates what the registry holds
// for the same reason releasePushRejectedError does: the release that did not
// publish must not send its operator away believing there is nothing left to
// publish.
func releaseRebaseFailedRecovery(spec ReleaseSpec, branch string) string {
	version := strings.TrimSpace(spec.Version)
	if spec.ArtifactsPublished {
		return fmt.Sprintf("version %s is already published, so rebase the release's own commits onto origin/%s by hand and push them", version, branch)
	}
	return fmt.Sprintf("version %s is on no registry — this release marks source control only — so rebase the release's own commits onto origin/%s by hand, push them, then publish the version's images and charts with `erun push --version %s`", version, branch, version)
}

// releaseRepointFailedRecovery is the same statement for the retry that
// absorbed the move and then could not bring the already-public release tag
// back onto the rebased commit. The tag is public either way; the artifacts
// are not, unless this run published them.
func releaseRepointFailedRecovery(spec ReleaseSpec) string {
	version := strings.TrimSpace(spec.Version)
	if spec.ArtifactsPublished {
		return fmt.Sprintf("version %s is already published, so move tag v%s onto the rebased release commit by hand and force-push it", version, version)
	}
	return fmt.Sprintf("version %s is on no registry — this release marks source control only — so move tag v%s onto the rebased release commit by hand, force-push it, then publish the version's images and charts with `erun push --version %s`", version, version, version)
}

// repointReleaseTagIfRebased brings the release's own annotated tag back onto
// the branch's history after a rebase rewrote the commit it named. A rebase
// replays every commit from the release's own fork point forward, including
// the one push-release-tag already published the tag under, so an already-public
// tag can end up naming a commit reachable from nothing once that commit is
// replayed under a new sha. The remote already holds the tag at the old
// commit, and an annotated tag update is never a fast-forward, so bringing it
// back onto the branch always needs a force.
func repointReleaseTagIfRebased(ctx Context, spec ReleaseSpec, projectRoot string, runGit GitCommandRunnerFunc) error {
	version := strings.TrimSpace(spec.Version)
	if version == "" {
		return nil
	}
	tag := "v" + version

	tagCommit, ok, err := gitResolvedRef(ctx, projectRoot, tag+"^{commit}")
	if err != nil || !ok {
		return nil
	}
	ancestor, err := gitIsAncestorOfHead(projectRoot, tagCommit)
	if err != nil || ancestor {
		return err
	}

	newCommit, err := findReplayedReleaseTagCommit(projectRoot, spec.Branch, tag, tagCommit)
	if err != nil {
		return err
	}
	return moveReleaseTagOnto(ctx, projectRoot, runGit, tag, newCommit)
}

// findReplayedReleaseTagCommit locates the commit a rebase gave the release's
// own tagged commit under its new sha, by the message the two must share (see
// findCommitsBySubjectInRange). The search is bounded to FETCH_HEAD..HEAD —
// the commits the rebase moments earlier in rebaseReleaseOntoRemoteBranch
// actually replayed onto the upstream it just fetched — rather than all of
// history, so an unrelated older commit that happens to share the subject can
// never be mistaken for the replayed one.
func findReplayedReleaseTagCommit(projectRoot, branch, tag, tagCommit string) (string, error) {
	subject, err := gitCommitSubject(projectRoot, tagCommit)
	if err != nil {
		return "", err
	}
	matches, err := findCommitsBySubjectInRange(projectRoot, "FETCH_HEAD..HEAD", subject)
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("tag %q named a commit the rebase rewrote, but no commit with its original message %q could be found on %s; move the tag onto the right commit by hand and push it",
			tag, subject, branch)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("tag %q named a commit the rebase rewrote, but %d commits with its original message %q were found on %s; move the tag onto the right commit by hand and push it",
			tag, len(matches), subject, branch)
	}
}

// moveReleaseTagOnto re-creates tag at newCommit, preserving its original
// annotation message, and force-publishes it: the remote already has this
// tag at the pre-rebase commit, and an annotated tag update is never a
// fast-forward.
func moveReleaseTagOnto(ctx Context, projectRoot string, runGit GitCommandRunnerFunc, tag, newCommit string) error {
	message, err := gitTagAnnotationSubject(projectRoot, tag)
	if err != nil {
		return err
	}
	ctx.Info(fmt.Sprintf("release: rebase moved the commit tag %s named; re-pointing it onto %s and force-pushing", tag, newCommit))
	ctx.TraceCommand(projectRoot, "git", "tag", "-f", "-a", tag, newCommit, "-m", message)
	if err := runGit(projectRoot, ctx.Stdout, ctx.Stderr, "tag", "-f", "-a", tag, newCommit, "-m", message); err != nil {
		return err
	}
	ctx.TraceCommand(projectRoot, "git", "push", "--force", "origin", "refs/tags/"+tag)
	return runGit(projectRoot, ctx.Stdout, ctx.Stderr, "push", "--force", "origin", "refs/tags/"+tag)
}

// gitIsAncestorOfHead reports whether commit is already part of HEAD's own
// history — true for a tag a rebase left untouched (it named a commit at or
// before the merge-base, which every rebase leaves alone), false once the
// commit it used to name was replayed under a new sha.
func gitIsAncestorOfHead(projectRoot, commit string) (bool, error) {
	err := Command("git", "-C", projectRoot, "merge-base", "--is-ancestor", commit, "HEAD").Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func gitCommitSubject(projectRoot, commit string) (string, error) {
	output, err := Command("git", "-C", projectRoot, "show", "-s", "--format=%s", commit).Output()
	if err != nil {
		if stderr := stderrFromExitError(err); stderr != "" {
			return "", fmt.Errorf("%w: %s", err, stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// findCommitsBySubjectInRange returns every commit in revRange whose subject
// is an exact match, newest first. A rebase replays commits in order without
// changing their message, and the release's generated commit subjects always
// carry the version being released, so the subject is normally unique within
// the commits a rebase just replayed — but the caller decides what to do with
// more than one match rather than this function silently picking one.
func findCommitsBySubjectInRange(projectRoot, revRange, subject string) ([]string, error) {
	output, err := Command("git", "-C", projectRoot, "log", "--format=%H%x01%s", revRange).Output()
	if err != nil {
		if stderr := stderrFromExitError(err); stderr != "" {
			return nil, fmt.Errorf("%w: %s", err, stderr)
		}
		return nil, err
	}
	var matches []string
	for _, line := range strings.Split(strings.TrimRight(string(output), "\n"), "\n") {
		hash, lineSubject, ok := strings.Cut(line, "\x01")
		if ok && lineSubject == subject {
			matches = append(matches, hash)
		}
	}
	return matches, nil
}

func gitTagAnnotationSubject(projectRoot, tag string) (string, error) {
	output, err := Command("git", "-C", projectRoot, "for-each-ref", "--format=%(contents:subject)", "refs/tags/"+tag).Output()
	if err != nil {
		if stderr := stderrFromExitError(err); stderr != "" {
			return "", fmt.Errorf("%w: %s", err, stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// releaseBranchPushArgs is the retried push. repointReleaseTagIfRebased has
// already re-pointed and force-pushed the release tag by the time this runs
// if the rebase moved it, so appending the tag by name here only matters when
// the tag never needed to move — a no-op against what origin already has.
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
