package gitref

import (
	"strings"
	"testing"
)

// The reclaim decision folds this error into its Reason, so git's stderr must survive.
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
