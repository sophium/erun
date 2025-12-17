package config

import (
	"errors"
	"os"
	"path/filepath"
)

var ErrGitNotFound = errors.New("could not find .git directory")

// FindGitRoot walks up from startPath to locate a .git directory or file.
func FindGitRoot(startPath string) (string, error) {
	if startPath == "" {
		startPath = "."
	}

	current, err := filepath.Abs(startPath)
	if err != nil {
		return "", err
	}

	for {
		gitPath := filepath.Join(current, ".git")
		if info, err := os.Stat(gitPath); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return current, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", ErrGitNotFound
		}
		current = parent
	}
}
