package mergequeue

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// gateMergeTrailerSeam fakes one squash's git calls and records the committed message.
func gateMergeTrailerSeam(body string, committed *string) GateMergeWorkingTreeDependencies {
	return GateMergeWorkingTreeDependencies{
		ResolveRef: func(core.Context, string, string) (string, error) { return "abc123", nil },
		RunGit: func(_ string, stdout io.Writer, _ io.Writer, args ...string) error {
			if len(args) > 0 && args[0] == "log" {
				_, _ = fmt.Fprint(stdout, body+"\x00")
			}
			if len(args) >= 3 && args[0] == "commit" {
				*committed = args[len(args)-1]
			}
			return nil
		},
		StagedPaths: func(core.Context, string) ([]string, error) { return []string{"widget.go"}, nil },
	}
}

func TestGateMergeOneSourceDoesNotRepeatTrailersTheCallerAlreadyPassed(t *testing.T) {
	var committed string
	deps := gateMergeTrailerSeam("Fix the widget\n\nCloses #2601\n", &committed)

	if _, _, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), model.GateMergeSource{Branch: "feature", Message: "Fix the widget\n\nCloses #2601"}, "origin", "refs/erun/gate-merge/main", deps); err != nil {
		t.Fatalf("gate-merge one source: %v", err)
	}
	if got := strings.Count(committed, "Closes #2601"); got != 1 {
		t.Fatalf("expected the trailer once, got %d in:\n%s", got, committed)
	}
}

func TestGateMergeOneSourceSkipsADefectFixThatNamesNoReproduction(t *testing.T) {
	var gitCalls []string
	var committed string
	deps := gateMergeTrailerSeam("Fix the widget\n\nCloses #2601\n", &committed)
	runGit := deps.RunGit
	deps.RunGit = func(root string, stdout, stderr io.Writer, args ...string) error {
		gitCalls = append(gitCalls, strings.Join(args, " "))
		return runGit(root, stdout, stderr, args...)
	}

	landed, skipped, err := gateMergeOneSource(testTraceContext(false), t.TempDir(), model.GateMergeSource{Branch: "bug/2601-widget", Message: "Fix the widget"}, "origin", "refs/erun/gate-merge/main", deps)
	if err != nil {
		t.Fatalf("a policy refusal must be a skip, not a batch failure: %v", err)
	}
	if landed != nil || skipped == nil {
		t.Fatalf("expected the source to be skipped, got landed=%+v skipped=%+v", landed, skipped)
	}
	if !strings.HasPrefix(skipped.Reason, "regression-test policy: ") || !strings.Contains(skipped.Reason, `"Regression-Test:"`) {
		t.Fatalf("expected the skip to name the missing declaration, got %q", skipped.Reason)
	}
	if !slices.Contains(gitCalls, "reset --hard HEAD") {
		t.Fatalf("expected the squash to be backed out, got git calls %q", gitCalls)
	}
	if committed != "" {
		t.Fatalf("a refused source must not be committed, got %q", committed)
	}
}
