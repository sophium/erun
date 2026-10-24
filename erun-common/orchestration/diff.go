package orchestration

import (
	"strings"
)

func FormatGitCommandStderr(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return "\n" + stderr
}
