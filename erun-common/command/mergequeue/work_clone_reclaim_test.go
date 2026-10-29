package mergequeue

import (
	"os"
	"path/filepath"
	"testing"
)

func writeAndCommit(t *testing.T, dir, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGitForTest(t, dir, "add", name)
	runGitForTest(t, dir, "commit", "-q", "-m", message)
}

// newRepoWithBareRemote returns both paths, for scenarios needing a second clone of the remote.
func newRepoWithBareRemote(t *testing.T) (repo, bare string) {
	t.Helper()
	repo = newAgentJobTestRepo(t)
	bare = newBareRemoteForTest(t, repo)
	return repo, bare
}
