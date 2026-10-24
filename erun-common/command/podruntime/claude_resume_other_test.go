//go:build !linux

package podruntime

import (
	"runtime"
	"strings"
	"testing"
)

func TestRunClaudeIsUnsupportedOffLinux(t *testing.T) {
	err := RunClaude(nil)
	if err == nil || !strings.Contains(err.Error(), "this host is "+runtime.GOOS) {
		t.Fatalf("expected an unsupported-OS error naming %s, got %v", runtime.GOOS, err)
	}
}
