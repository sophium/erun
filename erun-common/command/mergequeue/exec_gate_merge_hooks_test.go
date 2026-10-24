package mergequeue

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

func TestGateMergeLandsTheSourceTreeEvenWhenAPreCommitHookRewritesIt(t *testing.T) {
	repo, _ := newRepoWithBareRemote(t)
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")
	runGitForTest(t, repo, "checkout", "-q", "-b", "feature")
	writeAndCommit(t, repo, "widget.go", "package widget\n", "Add the widget")
	runGitForTest(t, repo, "push", "-q", "origin", "feature")
	runGitForTest(t, repo, "checkout", "-q", "main")
	sourceTree := gitOutputForTest(t, repo, "rev-parse", "feature^{tree}")

	hooks := t.TempDir()
	hook := "#!/bin/sh\necho '// rewritten by the hook' >> widget.go\ngit add widget.go\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatalf("write pre-commit hook: %v", err)
	}
	runGitForTest(t, repo, "config", "core.hooksPath", hooks)

	result, err := GateMergeWorkingTree(testTraceContext(false), repo, model.GateMergeWorkingTreeParams{
		Sources:      []model.GateMergeSource{{Branch: "feature", Message: "Add the widget"}},
		TargetBranch: "main",
	}, GateMergeWorkingTreeDependencies{})
	if err != nil {
		t.Fatalf("gate-merge: %v", err)
	}
	if len(result.Landed) != 1 {
		t.Fatalf("expected the source to land, got %+v", result)
	}
	if landed := gitOutputForTest(t, repo, "rev-parse", "HEAD^{tree}"); landed != sourceTree {
		t.Fatalf("the merge commit's tree must be the source's tree %s, got %s:\n%s", sourceTree, landed, gitOutputForTest(t, repo, "show", "HEAD"))
	}
}
