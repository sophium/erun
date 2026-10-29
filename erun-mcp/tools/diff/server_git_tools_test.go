package diff

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimedefaults"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

func TestDiffToolReturnsStructuredGitDiff(t *testing.T) {
	projectRoot := t.TempDir()
	runGitTestCommand(t, projectRoot, "init", "-b", "main")
	runGitTestCommand(t, projectRoot, "config", "user.email", "codex@example.com")
	runGitTestCommand(t, projectRoot, "config", "user.name", "Codex")
	if err := os.WriteFile(filepath.Join(projectRoot, "app.txt"), []byte("old\nsame\n"), 0o644); err != nil {
		t.Fatalf("write app.txt: %v", err)
	}
	runGitTestCommand(t, projectRoot, "add", ".")
	runGitTestCommand(t, projectRoot, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(projectRoot, "app.txt"), []byte("new\nsame\nadded\n"), 0o644); err != nil {
		t.Fatalf("write app.txt: %v", err)
	}

	handler := DiffTool(runtimedefaults.NormalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{RepoPath: projectRoot},
	}))
	_, output, err := handler(context.Background(), nil, DiffInput{})
	if err != nil {
		t.Fatalf("diffTool failed: %v", err)
	}

	assertStructuredDiffOutput(t, output, projectRoot)
}

func assertStructuredDiffOutput(t *testing.T, output model.DiffResult, projectRoot string) {
	t.Helper()

	if output.WorkingDirectory != projectRoot || output.RawDiff == "" {
		t.Fatalf("unexpected output: %+v", output)
	}
	if output.Summary.FileCount != 1 || output.Summary.Additions != 2 || output.Summary.Deletions != 1 {
		t.Fatalf("unexpected summary: %+v", output.Summary)
	}
	if len(output.Files) != 1 || output.Files[0].Path != "app.txt" {
		t.Fatalf("unexpected files: %+v", output.Files)
	}
	if len(output.Tree) != 1 || output.Tree[0].Name != "app.txt" {
		t.Fatalf("unexpected tree: %+v", output.Tree)
	}
}
