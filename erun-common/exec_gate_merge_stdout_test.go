package eruncommon

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// TestGateMergeOneSourceNamesACommitFailureReportedOnStdout reproduces a
// reported discarded stream. The commit error path was built to explain the
// failure — it captures a buffer and interpolates it into the message — but it
// captured only *stderr*, and git explains the failure that motivated the
// report ("nothing to commit, working tree clean") on *stdout*. The caller got
// a bare `git commit <branch>: exit status 1:`, which reads like a hook or
// toolchain problem and sent the reporter probing golangci-lint, hooksPath and
// the hook itself before establishing the commit had simply staged nothing.
//
// Driven through the RunGit seam rather than a real repository: once a squash
// that stages nothing is skipped instead of committed (see
// gateMergeOneSource), no real git invocation reaches a stdout-only commit
// failure, so this is the only way to hold the stream choice itself to account.
// RunGit succeeds for the squash and fails only for the commit, and the stub
// writes git's explanation to the stdout it is handed — exactly the split a
// real `git commit` produces.
func TestGateMergeOneSourceNamesACommitFailureReportedOnStdout(t *testing.T) {
	const explanation = "On branch main\nnothing to commit, working tree clean"
	deps := GateMergeWorkingTreeDependencies{
		ResolveRef: func(_ Context, _ string, ref string) (string, error) {
			return "abc123", nil
		},
		RunGit: func(_ string, stdout io.Writer, _ io.Writer, args ...string) error {
			if len(args) > 0 && args[0] == "commit" {
				if _, err := fmt.Fprint(stdout, explanation); err != nil {
					t.Fatalf("writing the stub commit's stdout explanation: %v", err)
				}
				return errors.New("exit status 1")
			}
			return nil
		},
		// One staged path, so the source is a real contribution and the run
		// reaches the commit rather than being skipped as a no-op.
		StagedPaths: func(Context, string) ([]string, error) {
			return []string{"feature.txt"}, nil
		},
	}

	_, _, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), GateMergeSource{Branch: "feature", Message: "Add widget"}, "origin", "refs/erun/gate-merge/main", deps)
	if err == nil {
		t.Fatal("expected the failed commit to be reported as an error")
	}
	if !strings.Contains(err.Error(), explanation) {
		t.Fatalf("the error must carry git's own explanation, which git prints on stdout, got: %v", err)
	}
	if !strings.Contains(err.Error(), "git commit feature") {
		t.Fatalf("expected the error to name the source branch, got: %v", err)
	}
}
