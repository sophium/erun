package jobs

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestGitChildrenOfThisSuiteDetachNoMaintenance pushes to a local bare remote
// the way the release-claim and job-worktree tests do and reads git's own
// trace: neither the pushing side nor the remote's receive-pack may start
// `git maintenance run --auto` or `git gc --auto`, which would otherwise keep
// writing into a temp dir the test is about to remove.
func TestGitChildrenOfThisSuiteDetachNoMaintenance(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	cmd := exec.Command("git", "push", "-q", "-u", "origin", "main")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_TRACE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git push: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "maintenance run --auto") || strings.Contains(line, "gc --auto") {
			t.Fatalf("a git child of this suite started automatic maintenance, which can outlive the test and race its temp-dir cleanup:\n%s", line)
		}
	}
}
