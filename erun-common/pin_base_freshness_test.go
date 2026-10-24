package eruncommon

import (
	"os/exec"
	"strings"
	"testing"
)

// gitOutputForTest returns git's trimmed stdout, so a scenario asserts the
// state it actually reached rather than assuming it.
func gitOutputForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}
