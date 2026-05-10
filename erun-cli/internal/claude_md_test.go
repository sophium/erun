package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureGlobalAgentInstructionsCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	if err := ensureGlobalClaudeMDWithHomeDir(dir); err != nil {
		t.Fatalf("claude: unexpected error: %v", err)
	}
	if err := ensureGlobalCodexInstructionsWithHomeDir(dir); err != nil {
		t.Fatalf("codex: unexpected error: %v", err)
	}
	for _, tc := range []struct {
		path string
	}{
		{filepath.Join(dir, ".claude", "CLAUDE.md")},
		{filepath.Join(dir, ".codex", "instructions.md")},
	} {
		data, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("expected %s to be created: %v", tc.path, err)
		}
		content := string(data)
		if !strings.Contains(content, claudeMDMarker) {
			t.Fatalf("expected %s to contain marker, got:\n%s", tc.path, content)
		}
		if !strings.Contains(content, "AGENTS.md") {
			t.Fatalf("expected %s to mention AGENTS.md, got:\n%s", tc.path, content)
		}
	}
}

func TestEnsureGlobalAgentInstructionsAppendsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	existing := "# Existing content\n\nSome instructions here.\n"
	for _, tc := range []struct {
		dir      string
		filename string
		ensureFn func(string) error
	}{
		{".claude", "CLAUDE.md", ensureGlobalClaudeMDWithHomeDir},
		{".codex", "instructions.md", ensureGlobalCodexInstructionsWithHomeDir},
	} {
		subDir := filepath.Join(dir, tc.dir)
		if err := os.MkdirAll(subDir, 0o700); err != nil {
			t.Fatalf("setup %s: %v", tc.dir, err)
		}
		if err := os.WriteFile(filepath.Join(subDir, tc.filename), []byte(existing), 0o600); err != nil {
			t.Fatalf("setup %s: %v", tc.dir, err)
		}
		if err := tc.ensureFn(dir); err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.dir, err)
		}
		data, err := os.ReadFile(filepath.Join(subDir, tc.filename))
		if err != nil {
			t.Fatalf("read %s: %v", tc.filename, err)
		}
		content := string(data)
		if !strings.HasPrefix(content, existing) {
			t.Fatalf("%s: expected existing content preserved, got:\n%s", tc.filename, content)
		}
		if !strings.Contains(content, claudeMDMarker) {
			t.Fatalf("%s: expected marker after append, got:\n%s", tc.filename, content)
		}
	}
}

func TestEnsureGlobalAgentInstructionsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		if err := ensureGlobalClaudeMDWithHomeDir(dir); err != nil {
			t.Fatalf("claude call %d: unexpected error: %v", i+1, err)
		}
		if err := ensureGlobalCodexInstructionsWithHomeDir(dir); err != nil {
			t.Fatalf("codex call %d: unexpected error: %v", i+1, err)
		}
	}
	openingTag := "<!-- erun-agents-md-hook -->"
	for _, path := range []string{
		filepath.Join(dir, ".claude", "CLAUDE.md"),
		filepath.Join(dir, ".codex", "instructions.md"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if count := strings.Count(string(data), openingTag); count != 1 {
			t.Fatalf("%s: expected opening tag exactly once, got %d in:\n%s", path, count, data)
		}
	}
}
