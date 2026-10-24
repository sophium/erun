package mergequeue

import (
	"os"
	"path/filepath"
	"testing"
)

// writeAndCommit writes name/content into dir and commits it, for building up
// the exact commit graphs the decision tests below compare against.
func writeAndCommit(t *testing.T, dir, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGitForTest(t, dir, "add", name)
	runGitForTest(t, dir, "commit", "-q", "-m", message)
}

// newRepoWithBareRemote is newAgentJobTestRepo plus newBareRemoteForTest,
// returning both paths for scenarios that need a second clone of the same
// remote (simulating a separate actor merging the branch).
func newRepoWithBareRemote(t *testing.T) (repo, bare string) {
	t.Helper()
	repo = newAgentJobTestRepo(t)
	bare = newBareRemoteForTest(t, repo)
	return repo, bare
}
