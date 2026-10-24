package release

import (
	"testing"
)

func repoRootForDockerignoreTest(t *testing.T) string {
	t.Helper()
	return erunRepoRoot(t)
}
