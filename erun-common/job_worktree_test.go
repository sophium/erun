package eruncommon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The reproduction this closes: an agent job's own exit status says nothing
// about the state of the working tree it ran in. Six lanes in one session
// each ended a turn with substantial work sitting uncommitted, and every one
// of them read as a plain "exited 0" -- indistinguishable from a lane that
// committed everything it did. A test that only proves the clean-finish case
// still passes would leave every one of those cases shipping silently; the
// dirty case below is the one that has to fail loudly.

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// newAgentJobTestRepo creates a real git working tree with one commit on
// branch "main", so tests exercise the actual git plumbing job_worktree.go
// runs rather than a mocked stand-in for it.
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
