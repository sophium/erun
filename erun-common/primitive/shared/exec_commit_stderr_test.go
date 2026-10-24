package shared

import (
	"os"
	"strconv"
	"testing"
)

// writeGitStub points ERUN_GIT_BIN at a script that prints stderr and exits
// non-zero, so a test can drive a failing git invocation deterministically
// rather than depending on a real git's own version-specific non-repo wording.
func writeGitStub(t *testing.T, stderr string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/git-stub"
	script := "#!/bin/sh\ncat <<'EOF' >&2\n" + stderr + "\nEOF\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write git stub: %v", err)
	}
	t.Setenv("ERUN_GIT_BIN", path)
}
