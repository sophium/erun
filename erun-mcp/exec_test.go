package erunmcp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExecRawResolveDir(t *testing.T) {
	repoRoot := t.TempDir()
	subdir := filepath.Join(repoRoot, "sub")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := RuntimeConfig{Context: RuntimeContext{RepoPath: repoRoot}}

	t.Run("empty dir defaults to the repo root", func(t *testing.T) {
		got, err := execRawResolveDir(cfg, "")
		if err != nil {
			t.Fatalf("execRawResolveDir: %v", err)
		}
		if got != repoRoot {
			t.Fatalf("got %q, want repo root %q", got, repoRoot)
		}
	})

	t.Run("relative dir resolves against the repo root", func(t *testing.T) {
		got, err := execRawResolveDir(cfg, "sub")
		if err != nil {
			t.Fatalf("execRawResolveDir: %v", err)
		}
		if got != subdir {
			t.Fatalf("got %q, want %q", got, subdir)
		}
	})

	t.Run("absolute dir passes through unchanged", func(t *testing.T) {
		got, err := execRawResolveDir(cfg, subdir)
		if err != nil {
			t.Fatalf("execRawResolveDir: %v", err)
		}
		if got != subdir {
			t.Fatalf("got %q, want %q", got, subdir)
		}
	})
}

func TestRawToolRunsFromTheGivenDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	repoRoot := t.TempDir()
	subdir := filepath.Join(repoRoot, "sub")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	handler := rawTool(normalizeRuntimeConfig(RuntimeConfig{Context: RuntimeContext{RepoPath: repoRoot}}))

	_, output, err := handler(context.Background(), nil, RawInput{
		Command: []string{"pwd"},
		Dir:     "sub",
	})
	if err != nil {
		t.Fatalf("rawTool: %v", err)
	}
	if !output.Wait {
		t.Fatalf("a plain call must run in the foreground, got %+v", output)
	}
	got := subdir
	if resolved, evalErr := filepath.EvalSymlinks(got); evalErr == nil {
		got = resolved
	}
	stdout := output.Stdout
	if resolved, evalErr := filepath.EvalSymlinks(stdout); evalErr == nil {
		stdout = resolved
	}
	if trimmed := trimTrailingNewline(stdout); trimmed != got {
		t.Fatalf("pwd printed %q, want %q", trimmed, got)
	}
}

func TestRawToolRefusesEnvInTheForeground(t *testing.T) {
	handler := rawTool(normalizeRuntimeConfig(RuntimeConfig{Context: RuntimeContext{RepoPath: t.TempDir()}}))

	_, _, err := handler(context.Background(), nil, RawInput{
		Command: []string{"true"},
		Env:     map[string]string{"FOO": "bar"},
	})
	if err == nil {
		t.Fatal("expected a refusal: env only applies to a backgrounded command")
	}
}

func trimTrailingNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
