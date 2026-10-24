package mergequeue

import (
	"strings"
	"testing"
)

// TestGitResolveRefIncludesGitStderr covers the merge-gate's own ref
// resolution: git's stderr is already captured onto exec.ExitError.Stderr by
// Output(), and must surface rather than the content-free "exit status N"
// that "%w" alone renders.
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
