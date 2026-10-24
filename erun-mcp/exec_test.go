package erunmcp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-mcp/tools/exectool"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

func TestRawToolRunsFromTheGivenDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	repoRoot := t.TempDir()
	subdir := filepath.Join(repoRoot, "sub")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	handler := exectool.RawTool(normalizeRuntimeConfig(shared.RuntimeConfig{Context: shared.RuntimeContext{RepoPath: repoRoot}}))

	_, output, err := handler(context.Background(), nil, exectool.RawInput{
		Command: []string{"pwd"},
		Dir:     "sub",
	})
	if err != nil {
		t.Fatalf("rawTool: %v", err)
	}
	if !output.Wait {
		t.Fatalf("a plain call must run in the foreground, got %+v", output)
	}
	// macOS TempDir and pwd spell the same directory differently (/var vs /private/var).
	// Trim the newline first or EvalSymlinks fails.
	got := subdir
	if resolved, evalErr := filepath.EvalSymlinks(got); evalErr == nil {
		got = resolved
	}
	stdout := trimTrailingNewline(output.Stdout)
	if resolved, evalErr := filepath.EvalSymlinks(stdout); evalErr == nil {
		stdout = resolved
	}
	if stdout != got {
		t.Fatalf("pwd printed %q, want %q", stdout, got)
	}
}

func TestRawToolRefusesEnvInTheForeground(t *testing.T) {
	handler := exectool.RawTool(normalizeRuntimeConfig(shared.RuntimeConfig{Context: shared.RuntimeContext{RepoPath: t.TempDir()}}))

	_, _, err := handler(context.Background(), nil, exectool.RawInput{
		Command: []string{"true"},
		Env:     map[string]string{"FOO": "bar"},
	})
	if err == nil {
		t.Fatal("expected a refusal: env only applies to a backgrounded command")
	}
}

// Foreground exec_raw must refuse a foreign target rather than run locally.
func TestRawToolRefusesForeignEnvironmentInTheForeground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	handler := exectool.RawTool(normalizeRuntimeConfig(shared.RuntimeConfig{Context: shared.RuntimeContext{Tenant: "erun", Environment: "code1", RepoPath: t.TempDir()}}))

	_, _, err := handler(context.Background(), nil, exectool.RawInput{
		Command:     []string{"true"},
		Tenant:      "erun",
		Environment: "build",
	})
	if err == nil {
		t.Fatal("exec_raw accepted a foreign environment in the foreground instead of refusing it")
	}
	if !strings.Contains(err.Error(), "erun/code1") || !strings.Contains(err.Error(), "erun/build") {
		t.Errorf("refusal should name both the server's own scope (erun/code1) and the requested one (erun/build), got: %v", err)
	}
}

// Many callers restate tenant/environment redundantly.
func TestRawToolAcceptsItsOwnScopeInTheForeground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell command")
	}
	handler := exectool.RawTool(normalizeRuntimeConfig(shared.RuntimeConfig{Context: shared.RuntimeContext{Tenant: "erun", Environment: "code1", RepoPath: t.TempDir()}}))

	for _, tc := range []struct {
		name        string
		tenant      string
		environment string
	}{
		{"omitted", "", ""},
		{"restated", "erun", "code1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := handler(context.Background(), nil, exectool.RawInput{
				Command:     []string{"true"},
				Tenant:      tc.tenant,
				Environment: tc.environment,
			})
			if err != nil {
				t.Fatalf("exec_raw refused its own scope (%s): %v", tc.name, err)
			}
		})
	}
}

func trimTrailingNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
