package mergequeue

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// TestGateMergeOneSourceNamesACommitFailureReportedOnStdout uses the RunGit seam, since no real
// git invocation reaches a stdout-only commit failure.
func TestGateMergeOneSourceNamesACommitFailureReportedOnStdout(t *testing.T) {
	const explanation = "On branch main\nnothing to commit, working tree clean"
	deps := GateMergeWorkingTreeDependencies{
		ResolveRef: func(_ core.Context, _ string, ref string) (string, error) {
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
		StagedPaths: func(core.Context, string) ([]string, error) {
			return []string{"feature.txt"}, nil
		},
	}

	_, _, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), model.GateMergeSource{Branch: "feature", Message: "Add widget"}, "origin", "refs/erun/gate-merge/main", deps)
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
