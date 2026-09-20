package eruncommon

import (
	"fmt"
	"io"
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

func TestJoinGitStreams(t *testing.T) {
	if got := joinGitStreams(" nothing to commit, working tree clean \n", ""); got != "nothing to commit, working tree clean" {
		t.Fatalf("stdout only: %q", got)
	}
	if got := joinGitStreams("", "fatal: hook failed"); got != "fatal: hook failed" {
		t.Fatalf("stderr only: %q", got)
	}
	if got := joinGitStreams("nothing to commit", "hint: use git commit --allow-empty"); got != "nothing to commit\nhint: use git commit --allow-empty" {
		t.Fatalf("both: %q", got)
	}
}

func TestGateMergeOneSourceSkipsNothingToCommit(t *testing.T) {
	deps := GateMergeWorkingTreeDependencies{
		ResolveRef: func(Context, string, string) (string, error) { return "abc123", nil },
		RunGit: func(_ string, stdout, _ io.Writer, args ...string) error {
			if len(args) > 0 && args[0] == "commit" {
				fmt.Fprint(stdout, "On branch probe\nnothing to commit, working tree clean\n")
				return fmt.Errorf("exit status 1")
			}
			return nil
		},
		ConflictedFiles: func(string, GitCommandRunnerFunc) ([]string, error) { return nil, nil },
	}
	landed, skipped, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), GateMergeSource{Branch: "already-landed", Message: "x"}, "origin", deps)
	if err != nil {
		t.Fatalf("no-op squash must skip, not fail the batch: %v", err)
	}
	if landed != nil {
		t.Fatalf("no-op squash must not land, got %+v", landed)
	}
	if skipped == nil {
		t.Fatal("expected a skip for a no-op squash")
	}
	if skipped.SourceBranch != "already-landed" {
		t.Fatalf("source branch: %q", skipped.SourceBranch)
	}
	if skipped.SourceCommit != "abc123" {
		t.Fatalf("source commit: %q", skipped.SourceCommit)
	}
	if !strings.Contains(skipped.Reason, "no changes") {
		t.Fatalf("skip reason must name the no-op, got: %q", skipped.Reason)
	}
}

func TestGateMergeOneSourceCommitErrorIncludesStdout(t *testing.T) {
	deps := GateMergeWorkingTreeDependencies{
		ResolveRef: func(Context, string, string) (string, error) { return "abc123", nil },
		RunGit: func(_ string, stdout, _ io.Writer, args ...string) error {
			if len(args) > 0 && args[0] == "commit" {
				fmt.Fprint(stdout, "golangci-lint found issues\n")
				return fmt.Errorf("exit status 1")
			}
			return nil
		},
		ConflictedFiles: func(string, GitCommandRunnerFunc) ([]string, error) { return nil, nil },
	}
	landed, skipped, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), GateMergeSource{Branch: "hook-fail", Message: "x"}, "origin", deps)
	if err == nil {
		t.Fatal("expected a fatal error when commit fails for a reason other than a no-op")
	}
	if landed != nil || skipped != nil {
		t.Fatalf("hook failure must not land or skip, landed=%v skipped=%v", landed, skipped)
	}
	if !strings.Contains(err.Error(), "golangci-lint found issues") {
		t.Fatalf("error must include git commit stdout, got: %v", err)
	}
	if !strings.Contains(err.Error(), "hook-fail") {
		t.Fatalf("error must name the source branch, got: %v", err)
	}
}
