package eruncommon

import (
	"bytes"
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

	if result.Summary.FileCount != 2 || result.Summary.Additions != 2 || result.Summary.Deletions != 1 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	if len(result.Files) != 2 {
		t.Fatalf("unexpected files: %+v", result.Files)
	}

	app := result.Files[0]
	if app.Path != "pkg/app.go" || app.Status != "modified" || app.Additions != 2 || app.Deletions != 1 {
		t.Fatalf("unexpected app file: %+v", app)
	}
	if len(app.Hunks) != 1 {
		t.Fatalf("unexpected hunks: %+v", app.Hunks)
	}
	hunk := app.Hunks[0]
	if hunk.OldStart != 1 || hunk.OldLines != 4 || hunk.NewStart != 1 || hunk.NewLines != 5 {
		t.Fatalf("unexpected hunk range: %+v", hunk)
	}
	if got := hunk.Lines[1]; got.Kind != "delete" || got.OldLine != 2 || got.NewLine != 0 {
		t.Fatalf("unexpected delete line: %+v", got)
	}
	if got := hunk.Lines[2]; got.Kind != "add" || got.NewLine != 2 || got.OldLine != 0 {
		t.Fatalf("unexpected add line: %+v", got)
	}

	logo := result.Files[1]
	if logo.Path != "assets/logo.png" || logo.Status != "added" || !logo.Binary {
		t.Fatalf("unexpected binary file: %+v", logo)
	}
	if len(result.Tree) != 4 {
		t.Fatalf("unexpected tree: %+v", result.Tree)
	}
	if result.Tree[0].Name != "pkg" || result.Tree[1].Path != "pkg/app.go" || result.Tree[1].Depth != 1 {
		t.Fatalf("unexpected first tree nodes: %+v", result.Tree[:2])
	}
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
	var gotArgs []string
	result, err := ResolveGitDiff("/tmp/project", func(dir string, stdout, stderr io.Writer, args ...string) error {
		gotDir = dir
		gotArgs = append([]string{}, args...)
		_, _ = io.WriteString(stdout, "diff --git a/a.txt b/a.txt\n")
		return nil
	})
	if err != nil {
		t.Fatalf("ResolveGitDiff failed: %v", err)
	}
	if gotDir != "/tmp/project" || strings.Join(gotArgs, " ") != "diff --no-color --no-ext-diff" {
		t.Fatalf("unexpected git invocation: dir=%q args=%+v", gotDir, gotArgs)
	}
	if result.WorkingDirectory != "/tmp/project" || result.RawDiff == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
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
