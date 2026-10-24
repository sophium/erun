package build

import (
	"errors"
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

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
