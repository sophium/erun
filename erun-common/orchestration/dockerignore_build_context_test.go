package orchestration

import (
	"testing"
)

// repoRootForDockerignoreTest returns the repo root.
func repoRootForDockerignoreTest(t *testing.T) string {
	t.Helper()
	return erunRepoRoot(t)
}
