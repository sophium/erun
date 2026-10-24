//go:build !linux

package podruntime

import (
	"fmt"
	"runtime"
)

// defaultClaudeHost refuses outside Linux: the claude link exists only in the
// runtime image, and the hand-over is an execve.
func defaultClaudeHost() (claudeHost, error) {
	return claudeHost{}, claudeUnsupportedError(runtime.GOOS)
}

func claudeUnsupportedError(goos string) error {
	return fmt.Errorf("claude: erun stands in for Claude Code only in the Linux runtime image; this host is %s, run Claude Code directly", goos)
}
