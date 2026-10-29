package projectroot

import (
	"os"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

// FindProjectRoot prefers ERUN_REPO_PATH when it names a directory: a sourceless
// runtime pod has the release tree there with no .git to walk up to.
func FindProjectRoot() (string, string, error) {
	if root := strings.TrimSpace(os.Getenv("ERUN_REPO_PATH")); root != "" {
		root = filepath.Clean(root)
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return filepath.Base(root), root, nil
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	return FindProjectRootFromDir(dir)
}

func FindProjectRootFromDir(dir string) (string, string, error) {
	dir = filepath.Clean(dir)
	for {
		gitDir := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			repoName := filepath.Base(dir)
			return repoName, dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", configpkg.ErrNotInGitRepository
		}
		dir = parent
	}
}
