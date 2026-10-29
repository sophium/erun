package mergequeue

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An agent job's exit status says nothing about its working tree; the dirty case must fail loudly.

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func newAgentJobTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitForTest(t, dir, "init", "-q", "-b", "main")
	runGitForTest(t, dir, "config", "user.email", "test@example.com")
	runGitForTest(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGitForTest(t, dir, "add", "seed.txt")
	runGitForTest(t, dir, "commit", "-q", "-m", "seed")
	return dir
}

func newBareRemoteForTest(t *testing.T, dir string) string {
	t.Helper()
	remote := t.TempDir()
	runGitForTest(t, remote, "init", "-q", "--bare")
	runGitForTest(t, dir, "remote", "add", "origin", remote)
	return remote
}
