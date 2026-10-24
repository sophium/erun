package exectool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-mcp/tools/shared/jobenvelope"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimedefaults"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// dangerousExecContent would be reinterpreted by a shell; write/commit must never shell-parse it.
const dangerousExecContent = "line one\n`echo pwned` $(echo pwned) \"quoted\" 'quoted'\ntrailing\n\n"

func TestWriteToolWritesContentByteIdenticallyAndRefusesOutsideRoot(t *testing.T) {
	projectRoot := t.TempDir()

	handler := WriteTool(runtimedefaults.NormalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{RepoPath: projectRoot},
	}))
	_, output, err := handler(context.Background(), nil, WriteInput{Path: "config/values.yaml", Content: dangerousExecContent})
	if err != nil {
		t.Fatalf("writeTool failed: %v", err)
	}
	if output.Write == nil {
		t.Fatalf("expected Write result, got %+v", output)
	}
	// macOS t.TempDir() is under /var while the tool reports /private/var.
	wantPath := filepath.Join(projectRoot, "config", "values.yaml")
	if resolved, evalErr := filepath.EvalSymlinks(wantPath); evalErr == nil {
		wantPath = resolved
	}
	if output.Write.Path != wantPath {
		t.Fatalf("Path = %q, want %q", output.Write.Path, wantPath)
	}
	if output.Write.Bytes != int64(len(dangerousExecContent)) {
		t.Fatalf("Bytes = %d, want %d", output.Write.Bytes, len(dangerousExecContent))
	}
	got, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != dangerousExecContent {
		t.Fatalf("written content = %q, want byte-identical %q", got, dangerousExecContent)
	}

	_, _, err = handler(context.Background(), nil, WriteInput{Path: "../escape.txt", Content: "x"})
	if err == nil {
		t.Fatalf("expected refusal for a path outside the repo root")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(projectRoot), "escape.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file to land outside the repo root")
	}
}

// A lexical containment check does not follow symlinks, so assert on the filesystem.
func TestWriteToolRefusesWriteThroughInTreeSymlink(t *testing.T) {
	projectRoot := t.TempDir()
	outside := t.TempDir()
	escapeTarget := filepath.Join(outside, "pwned.txt")
	if err := os.Symlink(outside, filepath.Join(projectRoot, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	handler := WriteTool(runtimedefaults.NormalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{RepoPath: projectRoot},
	}))
	_, _, err := handler(context.Background(), nil, WriteInput{Path: "escape/pwned.txt", Content: "pwned"})
	if err == nil {
		t.Fatalf("expected refusal writing through a symlinked directory component")
	}
	if _, statErr := os.Stat(escapeTarget); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file to land outside the repo root through the symlink")
	}
}

func TestCommitToolCommitsAndRefusesBranchMismatch(t *testing.T) {
	projectRoot := t.TempDir()
	runGitTestCommand(t, projectRoot, "init", "-b", "main")
	runGitTestCommand(t, projectRoot, "config", "user.email", "codex@example.com")
	runGitTestCommand(t, projectRoot, "config", "user.name", "Codex")
	runGitTestCommand(t, projectRoot, "commit", "--allow-empty", "-m", "initial")
	if err := os.WriteFile(filepath.Join(projectRoot, "app.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write app.txt: %v", err)
	}

	handler := CommitTool(runtimedefaults.NormalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{RepoPath: projectRoot},
	}))

	_, _, err := handler(context.Background(), nil, CommitInput{Branch: "not-main", Message: dangerousExecContent})
	if err == nil {
		t.Fatalf("expected refusal when the declared branch does not match the current branch")
	}

	_, output, err := handler(context.Background(), nil, CommitInput{Branch: "main", Message: dangerousExecContent})
	if err != nil {
		t.Fatalf("commitTool failed: %v", err)
	}
	assertCommitToolOutput(t, output, projectRoot)
}

func assertCommitToolOutput(t *testing.T, output jobenvelope.JobEnvelopeOutput, projectRoot string) {
	t.Helper()

	if output.Commit == nil {
		t.Fatalf("expected Commit result, got %+v", output)
	}
	if output.Commit.Branch != "main" || output.Commit.Commit == "" {
		t.Fatalf("unexpected commit result: %+v", output.Commit)
	}
	if len(output.Commit.Files) != 1 || output.Commit.Files[0] != "app.txt" {
		t.Fatalf("unexpected committed files: %+v", output.Commit.Files)
	}

	messageCmd := exec.Command("git", "log", "-1", "--format=%B")
	messageCmd.Dir = projectRoot
	messageOut, err := messageCmd.Output()
	if err != nil {
		t.Fatalf("read commit message: %v", err)
	}
	if !strings.Contains(string(messageOut), "`echo pwned` $(echo pwned) \"quoted\" 'quoted'") {
		t.Fatalf("commit message lost dangerous content verbatim: %q", messageOut)
	}
}

func gitStatusPorcelain(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	return string(out)
}

func mustWriteTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// seedCommitToolScopedPathsRepo leaves unrelated.txt dirty beside values.yaml.
func seedCommitToolScopedPathsRepo(t *testing.T) string {
	t.Helper()
	projectRoot := t.TempDir()
	runGitTestCommand(t, projectRoot, "init", "-b", "main")
	runGitTestCommand(t, projectRoot, "config", "user.email", "codex@example.com")
	runGitTestCommand(t, projectRoot, "config", "user.name", "Codex")
	runGitTestCommand(t, projectRoot, "commit", "--allow-empty", "-m", "initial")
	mustWriteTestFile(t, filepath.Join(projectRoot, "values.yaml"), "typo: fixed\n")
	mustWriteTestFile(t, filepath.Join(projectRoot, "unrelated.txt"), "someone else's in-flight work\n")
	return projectRoot
}

func TestCommitToolScopedPathsCommitsOnlyTheDeclaredFile(t *testing.T) {
	projectRoot := seedCommitToolScopedPathsRepo(t)
	if err := os.Remove(filepath.Join(projectRoot, "unrelated.txt")); err != nil {
		t.Fatalf("remove unrelated.txt: %v", err)
	}
	handler := CommitTool(runtimedefaults.NormalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{RepoPath: projectRoot},
	}))

	_, output, err := handler(context.Background(), nil, CommitInput{Branch: "main", Message: "fix the values typo", Paths: []string{"values.yaml"}})
	if err != nil {
		t.Fatalf("commitTool failed: %v", err)
	}
	if output.Commit == nil {
		t.Fatalf("expected Commit result, got %+v", output)
	}
	if got := output.Commit.Files; len(got) != 1 || got[0] != "values.yaml" {
		t.Fatalf("unexpected committed files: %+v", got)
	}
}

// Blank-only Paths must not degrade to committing everything.
func TestCommitToolRejectsBlankPathEntries(t *testing.T) {
	projectRoot := seedCommitToolScopedPathsRepo(t)
	handler := CommitTool(runtimedefaults.NormalizeRuntimeConfig(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{RepoPath: projectRoot},
	}))

	_, _, err := handler(context.Background(), nil, CommitInput{Branch: "main", Message: "fix the values typo", Paths: []string{"", ""}})
	if err == nil {
		t.Fatalf("expected refusal for a paths list of blank entries")
	}
	if strings.TrimSpace(gitStatusPorcelain(t, projectRoot)) == "" {
		t.Fatalf("expected both files to remain uncommitted after refusal")
	}
}
