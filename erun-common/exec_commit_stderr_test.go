package eruncommon

import (
	"os"
	"strconv"
	"strings"
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

// TestGitStagedFilesIncludesGitStderr and
// TestGitChangedWorkingTreeFilesIncludesGitStderr are erun#1768's pattern
// applied to the git-backed commit helpers: git's own stderr is already
// captured onto exec.ExitError.Stderr by Output(), and the caller-visible
// message must include it instead of the content-free "exit status N" a bare
// "%w" wrap renders.
func TestGitStagedFilesIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: not a git repository (or any of the parent directories): .git", 128)
	_, err := gitStagedFiles(testTraceContext(false), t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestGitChangedWorkingTreeFilesIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: not a git repository (or any of the parent directories): .git", 128)
	_, err := gitChangedWorkingTreeFiles(testTraceContext(false), t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}
