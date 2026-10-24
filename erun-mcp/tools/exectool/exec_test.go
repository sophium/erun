package exectool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

func TestExecRawResolveDir(t *testing.T) {
	repoRoot := t.TempDir()
	subdir := filepath.Join(repoRoot, "sub")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{RepoPath: repoRoot}}

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
