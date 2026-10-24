package shared

import (
	"strings"
	"testing"
)

// TestGitCommitsAheadIncludesGitStderr covers the reclaim decision's own
// comparison: git's stderr is already captured onto
// exec.ExitError.Stderr by Output(), and decideBranchWorkCloneReclaim folds
// the returned error's %v straight into the operator-facing Reason field, so
// dropping the stderr there means the reclaim decision's own explanation
// never carries it either.
func TestGitCommitsAheadIncludesGitStderr(t *testing.T) {
	nonRepo := t.TempDir()
	_, err := GitCommitsAhead(testTraceContext(false), nonRepo, "a", "b")
	if err == nil {
		t.Fatal("expected an error for a non-git directory")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}
