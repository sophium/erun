package releaseflow

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/shared/gitref"
)

func shouldSkipExistingReleaseTag(args []string) bool {
	return len(args) >= 3 && args[0] == "tag" && args[1] == "-a" && strings.TrimSpace(args[2]) != ""
}

func prepareReleaseTag(ctx core.Context, spec model.ReleaseSpec, runGit seam.GitCommandRunnerFunc, command model.ReleaseCommandSpec) (bool, error) {
	tag := strings.TrimSpace(command.Args[2])
	if tag == "" {
		return false, nil
	}
	if spec.Force {
		if err := deleteExistingReleaseTag(ctx, command.Dir, tag, runGit); err != nil {
			return false, err
		}
		return false, nil
	}
	return CanSkipExistingReleaseTag(ctx, spec, command.Dir, tag, runGit)
}

func CanSkipExistingReleaseTag(ctx core.Context, spec model.ReleaseSpec, projectRoot, tag string, runGit seam.GitCommandRunnerFunc) (bool, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return false, nil
	}

	tagCommit, ok, err := gitref.GitResolvedRef(ctx, projectRoot, tag+"^{}")
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	headCommit, ok, err := gitref.GitResolvedRef(ctx, projectRoot, "HEAD")
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("could not resolve HEAD for release tag check")
	}
	if tagCommit != headCommit {
		return false, releaseTagMismatchError(ctx, spec, projectRoot, tag, tagCommit, headCommit, runGit)
	}

	return true, nil
}

// releaseTagMismatchError diagnoses a tag not at HEAD. An unpushed tag origin never
// incorporated is an interrupted release's leftover; the refusal names the remedy rather
// than reclaiming it automatically.
func releaseTagMismatchError(ctx core.Context, spec model.ReleaseSpec, projectRoot, tag, tagCommit, headCommit string, runGit seam.GitCommandRunnerFunc) error {
	base := fmt.Errorf("release tag %q already exists at %s, expected current HEAD %s", tag, tagCommit, headCommit)
	pushed, err := gitref.GitRemoteTagExists(ctx, projectRoot, "origin", tag)
	if err != nil || pushed {
		return base
	}
	reachable, known := releaseBaseBranchIncorporates(projectRoot, spec.Branch, tagCommit, runGit)
	if !known || reachable {
		return base
	}
	return fmt.Errorf("%w\na previous run left an unpushed local tag %q at %s that origin/%s has never incorporated — most likely a release interrupted before it published anything; delete it with `git tag -d %s` to retry",
		base, tag, tagCommit, spec.Branch, tag)
}

// releaseBaseBranchIncorporates reports whether origin's branch contains commit; known is
// false when inconclusive, which is never treated as either answer.
func releaseBaseBranchIncorporates(projectRoot, branch, commit string, runGit seam.GitCommandRunnerFunc) (reachable bool, known bool) {
	branch = strings.TrimSpace(branch)
	if branch == "" || commit == "" {
		return false, false
	}
	if err := runGit(projectRoot, io.Discard, io.Discard, "fetch", "origin", branch); err != nil {
		return false, false
	}
	err := core.Command("git", "-C", projectRoot, "merge-base", "--is-ancestor", commit, "FETCH_HEAD").Run()
	if err == nil {
		return true, true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, true
	}
	return false, false
}

// ReleaseStampCommitPrefix lets a retry recognise its own leftover stamp commits.
const ReleaseStampCommitPrefix = "[skip ci] release "

// releaseStampWalkLimit bounds the walk; a long chain can only be repeated failed retries.
const releaseStampWalkLimit = 20

// ResolveReleaseVersionCommit names the version after the change, skipping leftover
// unpushed stamp commits at HEAD so a retry neither stacks stamps nor mints a new version.
func ResolveReleaseVersionCommit(ctx core.Context, projectRoot, branch string, resolveCommit seam.GitValueResolverFunc, runGit seam.GitCommandRunnerFunc) (string, error) {
	base, found, err := FindLeftoverReleaseStampBase(ctx, projectRoot, branch, runGit)
	if err != nil {
		return "", err
	}
	if !found {
		return resolveCommit(ctx, projectRoot)
	}
	ctx.Trace("release: HEAD is a leftover, unpushed release stamp from an earlier attempt; resolving the version from " + base + " instead")
	return gitref.GitShortCommitOf(ctx, projectRoot, base)
}

// FindLeftoverReleaseStampBase walks past leading unpushed stamp commits; found is false when
// HEAD carries none.
func FindLeftoverReleaseStampBase(ctx core.Context, projectRoot, branch string, runGit seam.GitCommandRunnerFunc) (string, bool, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "", false, nil
	}

	base, ok, err := gitref.GitResolvedRef(ctx, projectRoot, "HEAD")
	if err != nil || !ok {
		return "", false, err
	}

	found := false
	for i := 0; i < releaseStampWalkLimit; i++ {
		leftover, err := isLeftoverReleaseStampCommit(projectRoot, branch, base, runGit)
		if err != nil {
			return "", false, err
		}
		if !leftover {
			break
		}
		parent, ok, err := gitref.GitResolvedRef(ctx, projectRoot, base+"^")
		if err != nil {
			return "", false, err
		}
		if !ok {
			break
		}
		base = parent
		found = true
	}
	if !found {
		return "", false, nil
	}
	return base, true, nil
}

// isLeftoverReleaseStampCommit: a stamp subject that origin/branch has not incorporated.
func isLeftoverReleaseStampCommit(projectRoot, branch, commit string, runGit seam.GitCommandRunnerFunc) (bool, error) {
	subject, err := GitCommitSubject(projectRoot, commit)
	if err != nil {
		return false, err
	}
	if !strings.HasPrefix(subject, ReleaseStampCommitPrefix) {
		return false, nil
	}
	reachable, known := releaseBaseBranchIncorporates(projectRoot, branch, commit, runGit)
	return known && !reachable, nil
}

func deleteExistingReleaseTag(ctx core.Context, projectRoot, tag string, runGit seam.GitCommandRunnerFunc) error {
	localExists, err := gitref.GitTagExists(ctx, projectRoot, tag)
	if err != nil {
		return err
	}
	if localExists {
		ctx.TraceCommand(projectRoot, "git", "tag", "-d", tag)
		if !ctx.DryRun {
			if err := runGit(projectRoot, ctx.Stdout, ctx.Stderr, "tag", "-d", tag); err != nil {
				return err
			}
		}
	}

	remoteExists, err := gitref.GitRemoteTagExists(ctx, projectRoot, "origin", tag)
	if err != nil {
		return err
	}
	if remoteExists {
		ctx.TraceCommand(projectRoot, "git", "push", "--delete", "origin", tag)
		if !ctx.DryRun {
			var pushOutput strings.Builder
			if err := runReleasePush(ctx, projectRoot, runGit, &pushOutput, "push", "--delete", "origin", tag); err != nil {
				return err
			}
		}
	}

	return nil
}
