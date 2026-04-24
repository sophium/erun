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
			if strings.Join(args, " ") != "diff --no-color --no-ext-diff" {
				t.Fatalf("unexpected git args: %+v", args)
			}
			_, _ = io.WriteString(stdout, "diff --git a/a.txt b/a.txt\n")
			return nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"exec", "diff"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if stdout.String() != "diff --git a/a.txt b/a.txt\n" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if stderr.String() != "" {
		t.Fatalf("unexpected stderr: %q", stderr.String())
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
