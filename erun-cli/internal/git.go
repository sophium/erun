package internal

import (
	"errors"
	"os/exec"
	"strings"
)

func FindCurrentBranch(repoRoot string) (string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "branch", "--show-current")
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", ErrNotInGitRepository
		}
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}
