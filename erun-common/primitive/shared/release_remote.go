package shared

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
)

// A base branch that moves while a release builds would reject the final push after the
// artifacts and tag are public. The branch is re-read before the build spends, and the
// final push absorbs a later move. Registry claims are conditioned on ArtifactsPublished.

const releasePushStageName = "push"

// releasePushRebaseAttempts: the pushed commits are generated and conflict-free, so two
// retries cover ordinary races; beyond that the push's own error surfaces.
const releasePushRebaseAttempts = 2

// ensureReleaseBaseBranchUnmoved refuses a release whose base branch moved since sync-remote,
// before the build spends. An unreadable remote lets the release proceed.
func ensureReleaseBaseBranchUnmoved(ctx core.Context, spec model.ReleaseSpec, runGit gitx.GitCommandRunnerFunc) error {
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

// releaseBaseBranchAhead counts commits origin/<branch> has that the release lacks, reading
// the remote itself rather than a tracking ref.
func releaseBaseBranchAhead(projectRoot, branch string, runGit gitx.GitCommandRunnerFunc) (int, bool) {
	if err := runGit(projectRoot, io.Discard, io.Discard, "fetch", "origin", branch); err != nil {
		return 0, false
	}
	output, err := core.Command("git", "-C", projectRoot, "rev-list", "--count", "HEAD..FETCH_HEAD").Output()
	if err != nil {
		return 0, false
	}
	ahead, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, false
	}
	return ahead, true
}

func movedReleaseBaseBranchError(spec model.ReleaseSpec, branch string, ahead int) error {
	return fmt.Errorf("origin/%s has moved since this release rebased onto it: it carries %d commit(s) this release is not built on.\n"+
		"Building now would publish %s and push its tag, and only then fail at the final push with everything already public.\n"+
		"Nothing is published yet, so absorb the move and re-run:\n"+
		"  git -C %s pull --rebase origin %s\n"+
		"  erun build --release --force\n"+
		"(--force recreates the local v%s tag this run already made, which the rebase leaves behind.)",
		branch, ahead, spec.Version, spec.ProjectRoot, branch, spec.Version)
}

// isReleaseBranchPush excludes tag pushes: a moved branch cannot reject a tag.
func isReleaseBranchPush(stage model.ReleaseStage, command model.ReleaseCommandSpec) bool {
	return stage.Name == releasePushStageName &&
		command.Name == "git" &&
		isGitPushArgs(command.Args)
}

// isGitPushArgs reports whether a stage command is a push, which needs its stderr captured.
func isGitPushArgs(args []string) bool {
	return len(args) > 0 && args[0] == "push"
}

// RunReleaseBranchPush pushes the generated commits, rebasing onto a moved base branch and
// retrying (bounded), since the tag is already public by now.
func RunReleaseBranchPush(ctx core.Context, spec model.ReleaseSpec, command model.ReleaseCommandSpec, runGit gitx.GitCommandRunnerFunc) error {
	branch := strings.TrimSpace(spec.Branch)
	var pushOutput strings.Builder
	err := runReleasePush(ctx, command.Dir, runGit, &pushOutput, command.Args...)
	for attempt := 1; err != nil && branch != "" && attempt <= releasePushRebaseAttempts; attempt++ {
		rejections := ParseReleasePushRejections(pushOutput.String())
		if !ReleasePushRejectedTheMovedBaseBranch(rejections, branch) {
			return ReleasePushRejectedError(spec, rejections, err)
		}
		ctx.Info(fmt.Sprintf("release: push rejected; origin/%s moved during the release, rebasing onto it and retrying (%d/%d)", branch, attempt, releasePushRebaseAttempts))
		if rebaseErr := rebaseReleaseOntoRemoteBranch(ctx, command.Dir, branch, runGit); rebaseErr != nil {
			return fmt.Errorf("%w\nrebasing onto origin/%s to absorb the move failed: %w\n%s",
				err, branch, rebaseErr, ReleaseRebaseFailedRecovery(spec, branch))
		}
		if repointErr := repointReleaseTagIfRebased(ctx, spec, command.Dir, runGit); repointErr != nil {
			return fmt.Errorf("%w\nrebasing onto origin/%s absorbed the move, but re-pointing the already-public release tag failed: %w\n%s",
				err, branch, repointErr, ReleaseRepointFailedRecovery(spec))
		}
		pushOutput.Reset()
		err = runReleasePush(ctx, command.Dir, runGit, &pushOutput, releaseBranchPushArgs(spec, command)...)
	}
	return err
}

// releasePushStderrWriter: io.MultiWriter panics on a nil writer, so a nil sink uses the capture alone.
func releasePushStderrWriter(ctx core.Context, capture *strings.Builder) io.Writer {
	if ctx.Stderr == nil {
		return capture
	}
	return io.MultiWriter(ctx.Stderr, capture)
}

// runReleasePush runs a push that a ruleset can protect, reporting GitHub's bypass notice.
// The caller owns output so a retry reports the attempt that carried the notice.
func runReleasePush(ctx core.Context, dir string, runGit gitx.GitCommandRunnerFunc, output *strings.Builder, args ...string) error {
	err := runGit(dir, ctx.Stdout, releasePushStderrWriter(ctx, output), args...)
	gitx.ReportRulesetBypassFromPushArgs(ctx, args, output.String())
	return err
}

// ReleasePushRejection is one `! [rejected]` ref; remediation is scoped to its local side.
type ReleasePushRejection struct {
	Ref    string
	Reason string
}

var releasePushRejectionLine = regexp.MustCompile(`(?m)^\s*!\s*\[rejected\]\s+(\S+)\s*->\s*\S+\s*(?:\(([^)]*)\))?\s*$`)

func ParseReleasePushRejections(output string) []ReleasePushRejection {
	var rejections []ReleasePushRejection
	for _, match := range releasePushRejectionLine.FindAllStringSubmatch(output, -1) {
		ref := strings.TrimSpace(match[1])
		if ref == "" || ref == "(none)" {
			continue
		}
		rejections = append(rejections, ReleasePushRejection{Ref: ref, Reason: strings.TrimSpace(match[2])})
	}
	return rejections
}

// ReleasePushRejectedTheMovedBaseBranch reports whether the base branch itself was rejected
// because it moved, the only case a rebase repairs.
func ReleasePushRejectedTheMovedBaseBranch(rejections []ReleasePushRejection, branch string) bool {
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

// ReleasePushRejectedError names the rejected ref, what the registry holds, and that the
// public tag must not be deleted. The registry claim follows spec.ArtifactsPublished.
func ReleasePushRejectedError(spec model.ReleaseSpec, rejections []ReleasePushRejection, cause error) error {
	// described carries git's reason; names is the bare ref for the recovery command.
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
		// A public tag with no artifacts is undeployable, so the recovery names a publish.
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

// ReleaseRebaseFailedRecovery says what remains when the retry's rebase could not apply.
func ReleaseRebaseFailedRecovery(spec model.ReleaseSpec, branch string) string {
	version := strings.TrimSpace(spec.Version)
	if spec.ArtifactsPublished {
		return fmt.Sprintf("version %s is already published, so rebase the release's own commits onto origin/%s by hand and push them", version, branch)
	}
	return fmt.Sprintf("version %s is on no registry — this release marks source control only — so rebase the release's own commits onto origin/%s by hand, push them, then publish the version's images and charts with `erun push --version %s`", version, branch, version)
}

// ReleaseRepointFailedRecovery says what remains when the public tag could not be re-pointed.
func ReleaseRepointFailedRecovery(spec model.ReleaseSpec) string {
	version := strings.TrimSpace(spec.Version)
	if spec.ArtifactsPublished {
		return fmt.Sprintf("version %s is already published, so move tag v%s onto the rebased release commit by hand and force-push it", version, version)
	}
	return fmt.Sprintf("version %s is on no registry — this release marks source control only — so move tag v%s onto the rebased release commit by hand, force-push it, then publish the version's images and charts with `erun push --version %s`", version, version, version)
}

// repointReleaseTagIfRebased force-moves the release tag onto its replayed commit after a
// rebase; an annotated tag update is never a fast-forward.
func repointReleaseTagIfRebased(ctx core.Context, spec model.ReleaseSpec, projectRoot string, runGit gitx.GitCommandRunnerFunc) error {
	version := strings.TrimSpace(spec.Version)
	if version == "" {
		return nil
	}
	tag := "v" + version

	tagCommit, ok := resolvedReleaseTagCommit(ctx, projectRoot, tag)
	if !ok {
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

// findReplayedReleaseTagCommit searches only FETCH_HEAD..HEAD, so an older commit with the
// same subject cannot match.
func findReplayedReleaseTagCommit(projectRoot, branch, tag, tagCommit string) (string, error) {
	subject, err := GitCommitSubject(projectRoot, tagCommit)
	if err != nil {
		return "", err
	}
	matches, err := FindCommitsBySubjectInRange(projectRoot, "FETCH_HEAD..HEAD", subject)
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

// moveReleaseTagOnto re-creates tag at newCommit with its original annotation and force-pushes it.
func moveReleaseTagOnto(ctx core.Context, projectRoot string, runGit gitx.GitCommandRunnerFunc, tag, newCommit string) error {
	message, err := GitTagAnnotationSubject(projectRoot, tag)
	if err != nil {
		return err
	}
	ctx.Info(fmt.Sprintf("release: rebase moved the commit tag %s named; re-pointing it onto %s and force-pushing", tag, newCommit))
	ctx.TraceCommand(projectRoot, "git", "tag", "-f", "-a", tag, newCommit, "-m", message)
	if err := runGit(projectRoot, ctx.Stdout, ctx.Stderr, "tag", "-f", "-a", tag, newCommit, "-m", message); err != nil {
		return err
	}
	ctx.TraceCommand(projectRoot, "git", "push", "--force", "origin", "refs/tags/"+tag)
	var pushOutput strings.Builder
	return runReleasePush(ctx, projectRoot, runGit, &pushOutput, "push", "--force", "origin", "refs/tags/"+tag)
}

// gitIsAncestorOfHead is false once a rebase replayed the commit under a new sha.
func gitIsAncestorOfHead(projectRoot, commit string) (bool, error) {
	err := core.Command("git", "-C", projectRoot, "merge-base", "--is-ancestor", commit, "HEAD").Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func GitCommitSubject(projectRoot, commit string) (string, error) {
	output, err := core.Command("git", "-C", projectRoot, "show", "-s", "--format=%s", commit).Output()
	if err != nil {
		if stderr := core.StderrFromExitError(err); stderr != "" {
			return "", fmt.Errorf("%w: %s", err, stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// FindCommitsBySubjectInRange returns exact subject matches in revRange, newest first;
// the caller handles more than one.
func FindCommitsBySubjectInRange(projectRoot, revRange, subject string) ([]string, error) {
	output, err := core.Command("git", "-C", projectRoot, "log", "--format=%H%x01%s", revRange).Output()
	if err != nil {
		if stderr := core.StderrFromExitError(err); stderr != "" {
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

func GitTagAnnotationSubject(projectRoot, tag string) (string, error) {
	output, err := core.Command("git", "-C", projectRoot, "for-each-ref", "--format=%(contents:subject)", "refs/tags/"+tag).Output()
	if err != nil {
		if stderr := core.StderrFromExitError(err); stderr != "" {
			return "", fmt.Errorf("%w: %s", err, stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// releaseBranchPushArgs is the retried push; the tag is already re-pointed if it had to move.
func releaseBranchPushArgs(spec model.ReleaseSpec, command model.ReleaseCommandSpec) []string {
	version := strings.TrimSpace(spec.Version)
	if version == "" {
		return command.Args
	}
	return append(append([]string{}, command.Args...), "v"+version)
}

// rebaseReleaseOntoRemoteBranch aborts a rebase that cannot apply and returns its output on the error.
func rebaseReleaseOntoRemoteBranch(ctx core.Context, projectRoot, branch string, runGit gitx.GitCommandRunnerFunc) error {
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

func resolvedReleaseTagCommit(ctx core.Context, projectRoot, tag string) (string, bool) {
	commit, ok, err := GitResolvedRef(ctx, projectRoot, tag+"^{commit}")
	return commit, err == nil && ok
}
