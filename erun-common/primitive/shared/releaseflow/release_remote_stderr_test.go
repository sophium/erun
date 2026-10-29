package releaseflow

import (
	"strings"
	"testing"
)

// Callers fold this error into operator messages, so git's stderr must survive.
func TestGitCommitSubjectIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: bad object deadbeef", 128)
	_, err := GitCommitSubject(t.TempDir(), "deadbeef")
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "bad object deadbeef") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestFindCommitsBySubjectInRangeIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: bad revision 'FETCH_HEAD..HEAD'", 128)
	_, err := FindCommitsBySubjectInRange(t.TempDir(), "FETCH_HEAD..HEAD", "some subject")
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "bad revision") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}

func TestGitTagAnnotationSubjectIncludesGitStderr(t *testing.T) {
	writeGitStub(t, "fatal: unable to resolve refs/tags/v1.0.0", 128)
	_, err := GitTagAnnotationSubject(t.TempDir(), "v1.0.0")
	if err == nil {
		t.Fatal("expected an error for a failing git invocation")
	}
	if !strings.Contains(err.Error(), "unable to resolve") {
		t.Fatalf("error must include git's own stderr, got: %v", err)
	}
}
