package eruncommon

import (
	"strings"
	"testing"
)

// TestGitResolveRefIncludesGitStderr is erun#1768's pattern applied to the
// merge-gate's own ref resolution: git's stderr is already captured onto
// exec.ExitError.Stderr by Output(), and the caller wraps only "%w" (which
// renders as the content-free "exit status N") around the bare error this
// used to return.
func TestGitResolveRefIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: ambiguous argument 'nonexistent-ref': unknown revision or path not in the working tree.", 128)
	_, err := gitResolveRef(testTraceContext(false), t.TempDir(), "nonexistent-ref")
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "ambiguous argument") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}
