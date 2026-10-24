package eruncommon

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/infra/gitx"
)

func OriginRemoteURL() (string, error) {
	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	if err := gitx.GitCommandRunner("", AsWriter(stdout), AsWriter(stderr), "remote", "get-url", "origin"); err != nil {
		return "", fmt.Errorf("remote-url is required: no --remote-url given and reading origin failed: %w%s",
			err, FormatGitCommandStderr(stderr.String()))
	}
	remote := strings.TrimSpace(stdout.String())
	if remote == "" {
		return "", fmt.Errorf("remote-url is required: the current checkout has no origin remote")
	}
	return remote, nil
}
