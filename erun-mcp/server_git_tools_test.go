package erunmcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-mcp/tools/diff"
	"github.com/sophium/erun/erun-mcp/tools/exectool"
	sharedpkg "github.com/sophium/erun/erun-mcp/tools/shared"
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

	handler := diff.DiffTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{RepoPath: projectRoot},
	}))
	_, output, err := handler(context.Background(), nil, diff.DiffInput{})
	if err != nil {
		t.Fatalf("diffTool failed: %v", err)
	}

	assertStructuredDiffOutput(t, output, projectRoot)
}

func assertStructuredDiffOutput(t *testing.T, output lifecycle.DiffResult, projectRoot string) {
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

// dangerousExecContent carries the constructs a shell would reinterpret —
// backticks, command substitution, embedded quotes, a trailing newline — so a
// round trip through write/commit demonstrates the property that justifies
// bypassing raw for these two operations: nothing here is ever shell-parsed.
const dangerousExecContent = "line one\n`echo pwned` $(echo pwned) \"quoted\" 'quoted'\ntrailing\n\n"

func TestWriteToolWritesContentByteIdenticallyAndRefusesOutsideRoot(t *testing.T) {
	projectRoot := t.TempDir()

	handler := exectool.WriteTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{RepoPath: projectRoot},
	}))
	_, output, err := handler(context.Background(), nil, exectool.WriteInput{Path: "config/values.yaml", Content: dangerousExecContent})
	if err != nil {
		t.Fatalf("writeTool failed: %v", err)
	}
	if output.Write == nil {
		t.Fatalf("expected Write result, got %+v", output)
	}
	// Canonical, for the same reason exec_test.go compares canonical paths: on
	// macOS t.TempDir() returns the TMPDIR spelling (/var/folders/...) while the
	// tool reports the real one (/private/var/folders/...).
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

	_, _, err = handler(context.Background(), nil, exectool.WriteInput{Path: "../escape.txt", Content: "x"})
	if err == nil {
		t.Fatalf("expected refusal for a path outside the repo root")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(projectRoot), "escape.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file to land outside the repo root")
	}
}

// TestWriteToolRefusesWriteThroughInTreeSymlink proves the write tool cannot
// be pointed outside the repo root through a symlink planted inside it — a
// lexical containment check alone does not follow symlinks, so this asserts
// on the actual filesystem outcome, not just the handler's error, since the
// whole defect is that a lexical check reports success incorrectly.
func TestWriteToolRefusesWriteThroughInTreeSymlink(t *testing.T) {
	projectRoot := t.TempDir()
	outside := t.TempDir()
	escapeTarget := filepath.Join(outside, "pwned.txt")
	if err := os.Symlink(outside, filepath.Join(projectRoot, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	handler := exectool.WriteTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{RepoPath: projectRoot},
	}))
	_, _, err := handler(context.Background(), nil, exectool.WriteInput{Path: "escape/pwned.txt", Content: "pwned"})
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

	handler := exectool.CommitTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{RepoPath: projectRoot},
	}))

	_, _, err := handler(context.Background(), nil, exectool.CommitInput{Branch: "not-main", Message: dangerousExecContent})
	if err == nil {
		t.Fatalf("expected refusal when the declared branch does not match the current branch")
	}

	_, output, err := handler(context.Background(), nil, exectool.CommitInput{Branch: "main", Message: dangerousExecContent})
	if err != nil {
		t.Fatalf("commitTool failed: %v", err)
	}
	assertCommitToolOutput(t, output, projectRoot)
}

func assertCommitToolOutput(t *testing.T, output sharedpkg.JobEnvelopeOutput, projectRoot string) {
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

// seedCommitToolScopedPathsRepo prepares the motivating scenario: the caller
// wrote values.yaml and wants to commit only that, but the tree also carries
// unrelated.txt from some other writer. Before path scoping existed, the tool
// had no way to express "only these paths" at all, so `git add -A` would
// have swept both in.
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

func TestCommitToolScopedPathsRefusesUnrelatedDirtyFile(t *testing.T) {
	projectRoot := seedCommitToolScopedPathsRepo(t)
	handler := exectool.CommitTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{RepoPath: projectRoot},
	}))

	_, _, err := handler(context.Background(), nil, exectool.CommitInput{Branch: "main", Message: "fix the values typo", Paths: []string{"values.yaml"}})
	if err == nil {
		t.Fatalf("expected refusal when the tree has changes outside the declared paths")
	}
	if !strings.Contains(err.Error(), "unrelated.txt") {
		t.Fatalf("expected the refusal to name the unrelated file, got: %v", err)
	}
	if strings.TrimSpace(gitStatusPorcelain(t, projectRoot)) == "" {
		t.Fatalf("expected both files to remain uncommitted after refusal")
	}
}

func TestCommitToolScopedPathsCommitsOnlyTheDeclaredFile(t *testing.T) {
	projectRoot := seedCommitToolScopedPathsRepo(t)
	if err := os.Remove(filepath.Join(projectRoot, "unrelated.txt")); err != nil {
		t.Fatalf("remove unrelated.txt: %v", err)
	}
	handler := exectool.CommitTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{RepoPath: projectRoot},
	}))

	_, output, err := handler(context.Background(), nil, exectool.CommitInput{Branch: "main", Message: "fix the values typo", Paths: []string{"values.yaml"}})
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

// TestCommitToolRejectsBlankPathEntries proves a Paths list of only blank
// strings is refused rather than silently degrading to the unscoped
// "commit everything" behavior — the exact failure path scoping exists to
// prevent, reintroduced through a caller passing blanks instead of
// omitting Paths entirely.
func TestCommitToolRejectsBlankPathEntries(t *testing.T) {
	projectRoot := seedCommitToolScopedPathsRepo(t)
	handler := exectool.CommitTool(normalizeRuntimeConfig(sharedpkg.RuntimeConfig{
		Context: sharedpkg.RuntimeContext{RepoPath: projectRoot},
	}))

	_, _, err := handler(context.Background(), nil, exectool.CommitInput{Branch: "main", Message: "fix the values typo", Paths: []string{"", ""}})
	if err == nil {
		t.Fatalf("expected refusal for a paths list of blank entries")
	}
	if strings.TrimSpace(gitStatusPorcelain(t, projectRoot)) == "" {
		t.Fatalf("expected both files to remain uncommitted after refusal")
	}
}
