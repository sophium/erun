package eruncommon

import (
	"strings"
	"testing"
)

// These are erun#1768's pattern applied to the release tag-replay helpers:
// git's stderr is already captured onto exec.ExitError.Stderr by Output(),
// and each caller (findReplayedReleaseTagCommit, moveReleaseTagOnto) folds
// the bare error straight into an operator-facing message, so dropping the
// stderr here means that message never carries it either.
func TestGitCommitSubjectIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: bad object deadbeef", 128)
	_, err := gitCommitSubject(t.TempDir(), "deadbeef")
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "bad object deadbeef") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestFindCommitsBySubjectInRangeIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: bad revision 'FETCH_HEAD..HEAD'", 128)
	_, err := findCommitsBySubjectInRange(t.TempDir(), "FETCH_HEAD..HEAD", "some subject")
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "bad revision") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestGitTagAnnotationSubjectIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: unable to resolve refs/tags/v1.0.0", 128)
	_, err := gitTagAnnotationSubject(t.TempDir(), "v1.0.0")
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "unable to resolve") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}
