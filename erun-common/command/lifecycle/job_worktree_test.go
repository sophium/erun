package lifecycle

import (
	"os/exec"
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
