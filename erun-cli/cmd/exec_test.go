package cmd

import (
	"bytes"
	"io"
	"strings"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

func TestExecDiffPrintsRawGitDiff(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "project", "/tmp/project", nil
		},
		RunGit: func(dir string, stdout, stderr io.Writer, args ...string) error {
			if dir != "/tmp/project" {
				t.Fatalf("unexpected git dir: %q", dir)
			}
			switch strings.Join(args, " ") {
			case "diff --no-color --no-ext-diff":
				_, _ = io.WriteString(stdout, "diff --git a/a.txt b/a.txt\n")
			case "ls-files --others --exclude-standard -z":
			default:
				t.Fatalf("unexpected git args: %+v", args)
			}
			return nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"exec", "diff"})

	requireNoError(t, cmd.Execute(), "Execute failed")
	if stdout.String() != "diff --git a/a.txt b/a.txt\n" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	for _, want := range []string{
		"audit: erun exec diff",
		"cd /tmp/project && git diff --no-color --no-ext-diff",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected stderr to contain %q, got %q", want, stderr.String())
		}
	}
}

func TestExecRawRunsCommandFromProjectRootWithDefaultTrace(t *testing.T) {
	var gotDir string
	var gotName string
	var gotArgs []string
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "project", "/tmp/project", nil
		},
		RunRawCommand: func(dir, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
			gotDir = dir
			gotName = name
			gotArgs = append([]string(nil), args...)
			_, _ = io.WriteString(stdout, "ok\n")
			return nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"exec", "raw", "echo", "--token", "secret-value", "done"})

	requireNoError(t, cmd.Execute(), "Execute failed")

	if gotDir != "/tmp/project" || gotName != "echo" || strings.Join(gotArgs, " ") != "--token secret-value done" {
		t.Fatalf("unexpected raw command call: dir=%q name=%q args=%+v", gotDir, gotName, gotArgs)
	}
	if stdout.String() != "ok\n" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "audit: erun exec raw echo --token <redacted> done") || !strings.Contains(got, "cd /tmp/project && echo --token '<redacted>' done") {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

func TestRunExecDiffReturnsProjectRootError(t *testing.T) {
	err := runExecDiffCommand(common.Context{Stdout: io.Discard}, func() (string, string, error) {
		return "", "", common.ErrNotInGitRepository
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
