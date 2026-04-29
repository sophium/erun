package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestParseGitDiffBuildsFilesHunksAndTree(t *testing.T) {
	raw := strings.Join([]string{
		"diff --git a/pkg/app.go b/pkg/app.go",
		"index 1111111..2222222 100644",
		"--- a/pkg/app.go",
		"+++ b/pkg/app.go",
		"@@ -1,4 +1,5 @@",
		" package pkg",
		"-var oldValue = 1",
		"+var newValue = 2",
		"+var anotherValue = 3",
		" func run() {}",
		"diff --git a/assets/logo.png b/assets/logo.png",
		"new file mode 100644",
		"index 0000000..3333333",
		"Binary files /dev/null and b/assets/logo.png differ",
		"",
	}, "\n")

	result := ParseGitDiff(raw)

	requireParsedGitDiffSummary(t, result)
	requireParsedGitDiffFiles(t, result)
	requireParsedGitDiffTree(t, result)
}

func requireParsedGitDiffSummary(t *testing.T, result DiffResult) {
	t.Helper()
	requireCondition(t, result.Summary.FileCount == 2 && result.Summary.Additions == 2 && result.Summary.Deletions == 1, "unexpected summary: %+v", result.Summary)
	requireEqual(t, len(result.Files), 2, "file count")
}

func requireParsedGitDiffFiles(t *testing.T, result DiffResult) {
	t.Helper()
	app := result.Files[0]
	requireCondition(t, app.Path == "pkg/app.go" && app.Status == "modified" && app.Additions == 2 && app.Deletions == 1, "unexpected app file: %+v", app)
	requireEqual(t, len(app.Hunks), 1, "hunk count")
	hunk := app.Hunks[0]
	requireCondition(t, hunk.OldStart == 1 && hunk.OldLines == 4 && hunk.NewStart == 1 && hunk.NewLines == 5, "unexpected hunk range: %+v", hunk)
	requireDiffLine(t, hunk.Lines[1], "delete", 2, 0)
	requireDiffLine(t, hunk.Lines[2], "add", 0, 2)

	logo := result.Files[1]
	requireCondition(t, logo.Path == "assets/logo.png" && logo.Status == "added" && logo.Binary, "unexpected binary file: %+v", logo)
}

func requireDiffLine(t *testing.T, line DiffLine, kind string, oldLine, newLine int) {
	t.Helper()
	requireCondition(t, line.Kind == kind && line.OldLine == oldLine && line.NewLine == newLine, "unexpected %s line: %+v", kind, line)
}

func requireParsedGitDiffTree(t *testing.T, result DiffResult) {
	t.Helper()
	requireEqual(t, len(result.Tree), 4, "tree node count")
	requireCondition(t, result.Tree[0].Name == "pkg" && result.Tree[1].Path == "pkg/app.go" && result.Tree[1].Depth == 1, "unexpected first tree nodes: %+v", result.Tree[:2])
}

func TestParseGitDiffDetectsDeletedAndRenamedFiles(t *testing.T) {
	raw := strings.Join([]string{
		"diff --git a/old.txt b/old.txt",
		"deleted file mode 100644",
		"index 1111111..0000000",
		"--- a/old.txt",
		"+++ /dev/null",
		"@@ -1 +0,0 @@",
		"-gone",
		"diff --git a/old-name.txt b/new-name.txt",
		"similarity index 91%",
		"rename from old-name.txt",
		"rename to new-name.txt",
		"--- a/old-name.txt",
		"+++ b/new-name.txt",
		"@@ -1 +1 @@",
		"-before",
		"+after",
		"",
	}, "\n")

	result := ParseGitDiff(raw)

	if len(result.Files) != 2 {
		t.Fatalf("unexpected files: %+v", result.Files)
	}
	if result.Files[0].Status != "deleted" || result.Files[0].Path != "old.txt" || result.Files[0].Deletions != 1 {
		t.Fatalf("unexpected deleted file: %+v", result.Files[0])
	}
	if result.Files[1].Status != "renamed" || result.Files[1].OldPath != "old-name.txt" || result.Files[1].NewPath != "new-name.txt" {
		t.Fatalf("unexpected renamed file: %+v", result.Files[1])
	}
}

func TestResolveGitDiffRunsNoColorDiff(t *testing.T) {
	var gotDir string
	calls := make([]string, 0, 2)
	result, err := ResolveGitDiff("/tmp/project", func(dir string, stdout, stderr io.Writer, args ...string) error {
		gotDir = dir
		calls = append(calls, strings.Join(args, " "))
		return writeResolveGitDiffOutput(t, stdout, calls, args)
	})
	requireNoError(t, err, "ResolveGitDiff failed")
	requireCondition(t, gotDir == "/tmp/project" && len(calls) == 2 && calls[0] == "diff --no-color --no-ext-diff" && calls[1] == "ls-files --others --exclude-standard -z", "unexpected git invocation: dir=%q calls=%+v", gotDir, calls)
	requireCondition(t, result.WorkingDirectory == "/tmp/project" && result.RawDiff != "", "unexpected result: %+v", result)
}

func writeResolveGitDiffOutput(t *testing.T, stdout io.Writer, calls []string, args []string) error {
	t.Helper()
	switch len(calls) {
	case 1:
		_, _ = io.WriteString(stdout, "diff --git a/a.txt b/a.txt\n")
	case 2:
		_, _ = io.WriteString(stdout, "")
	default:
		t.Fatalf("unexpected git call: %v", args)
	}
	return nil
}

func TestResolveGitDiffIncludesUntrackedFiles(t *testing.T) {
	result, err := ResolveGitDiff("/tmp/project", resolveGitDiffWithUntrackedFiles(t))
	requireNoError(t, err, "ResolveGitDiff failed")
	requireCondition(t, result.Summary.FileCount == 3 && result.Summary.Additions == 2, "unexpected summary: %+v files=%+v", result.Summary, result.Files)
	requireCondition(t, result.Files[1].Path == "new.txt" && result.Files[1].Status == "added", "expected untracked file in diff result, got %+v", result.Files)
	requireCondition(t, result.Files[2].Path == "nested/newer.txt" && result.Files[2].Status == "added", "expected nested untracked file in diff result, got %+v", result.Files)
}

func resolveGitDiffWithUntrackedFiles(t *testing.T) GitCommandRunnerFunc {
	t.Helper()
	return func(dir string, stdout, stderr io.Writer, args ...string) error {
		return writeGitDiffWithUntrackedOutput(t, stdout, args)
	}
}

func writeGitDiffWithUntrackedOutput(t *testing.T, stdout io.Writer, args []string) error {
	t.Helper()
	switch strings.Join(args, " ") {
	case "diff --no-color --no-ext-diff":
		_, _ = io.WriteString(stdout, "diff --git a/existing.txt b/existing.txt\n")
		return nil
	case "ls-files --others --exclude-standard -z":
		_, _ = io.WriteString(stdout, "new.txt\x00nested/newer.txt\x00")
		return nil
	case "diff --no-color --no-ext-diff --no-index -- /dev/null new.txt":
		_, _ = io.WriteString(stdout, untrackedFileDiff("new.txt", "+new"))
		return fmt.Errorf("exit status 1")
	case "diff --no-color --no-ext-diff --no-index -- /dev/null nested/newer.txt":
		_, _ = io.WriteString(stdout, untrackedFileDiff("nested/newer.txt", "+newer"))
		return fmt.Errorf("exit status 1")
	default:
		t.Fatalf("unexpected git call: %v", args)
		return nil
	}
}

func untrackedFileDiff(path, line string) string {
	return strings.Join([]string{
		"diff --git a/" + path + " b/" + path,
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/" + path,
		"@@ -0,0 +1 @@",
		line,
		"",
	}, "\n")
}

func TestWriteRawDiff(t *testing.T) {
	stdout := new(bytes.Buffer)
	if err := WriteRawDiff(stdout, DiffResult{RawDiff: "diff body"}); err != nil {
		t.Fatalf("WriteRawDiff failed: %v", err)
	}
	if stdout.String() != "diff body" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}
