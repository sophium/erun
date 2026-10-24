package shared

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

func gitShortCommitOf(ctx core.Context, projectRoot, commit string) (string, error) {
	ctx.TraceCommand("", "git", "-C", projectRoot, "rev-parse", "--short", commit)
	output, err := core.Command("git", "-C", projectRoot, "rev-parse", "--short", commit).Output()
	if err != nil {
		if stderr := core.StderrFromExitError(err); stderr != "" {
			return "", fmt.Errorf("%w: %s", err, stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func gitTagExists(ctx core.Context, projectRoot, tag string) (bool, error) {
	_, ok, err := GitResolvedRef(ctx, projectRoot, tag+"^{}")
	return ok, err
}

func GitRemoteTagExists(ctx core.Context, projectRoot, remote, tag string) (bool, error) {
	remote = strings.TrimSpace(remote)
	tag = strings.TrimSpace(tag)
	if remote == "" || tag == "" {
		return false, nil
	}

	ctx.TraceCommand("", "git", "-C", projectRoot, "ls-remote", "--tags", "--refs", remote, "refs/tags/"+tag)
	output, err := core.Command("git", "-C", projectRoot, "ls-remote", "--tags", "--refs", remote, "refs/tags/"+tag).CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(output)); detail != "" {
			return false, fmt.Errorf("%w: %s", err, detail)
		}
		return false, err
	}
	return strings.TrimSpace(string(output)) != "", nil
}

func GitResolvedRef(ctx core.Context, projectRoot, ref string) (string, bool, error) {
	ctx.TraceCommand("", "git", "-C", projectRoot, "rev-parse", ref)
	output, err := core.Command("git", "-C", projectRoot, "rev-parse", ref).CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(output)), true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() != 0 {
		return "", false, nil
	}
	if detail := strings.TrimSpace(string(output)); detail != "" {
		return "", false, fmt.Errorf("%w: %s", err, detail)
	}
	return "", false, err
}

// GitBranchExists checks local and origin-tracking refs: `git checkout <branch>` creates a
// local branch from origin when needed.
func GitBranchExists(ctx core.Context, projectRoot, branch string) (bool, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return false, nil
	}
	if exists, err := gitRefExists(ctx, projectRoot, "refs/heads/"+branch); err != nil || exists {
		return exists, err
	}
	return gitRefExists(ctx, projectRoot, "refs/remotes/origin/"+branch)
}

func gitRefExists(ctx core.Context, projectRoot, ref string) (bool, error) {
	ctx.TraceCommand("", "git", "-C", projectRoot, "show-ref", "--verify", "--quiet", ref)
	cmd := core.Command("git", "-C", projectRoot, "show-ref", "--verify", "--quiet", ref)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func GitWorktreeClean(ctx core.Context, projectRoot string) (bool, error) {
	// Only tracked changes gate the release, which publishes HEAD.
	ctx.TraceCommand("", "git", "-C", projectRoot, "status", "--porcelain", "--untracked-files=no")
	output, err := core.Command("git", "-C", projectRoot, "status", "--porcelain", "--untracked-files=no").CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(output)); detail != "" {
			return false, fmt.Errorf("%w: %s", err, detail)
		}
		return false, err
	}
	return strings.TrimSpace(string(output)) == "", nil
}
