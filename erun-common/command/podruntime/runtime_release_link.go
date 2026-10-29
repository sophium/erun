package podruntime

import (
	"os"
	"path/filepath"
)

// RuntimeReleaseDir is outside /home/erun because the home volume would shadow it.
const RuntimeReleaseDir = "/opt/erun/release"

// LinkRuntimeRelease symlinks repoDir to releaseDir. It never clobbers source: a populated
// directory is left untouched. It reports whether repoDir now links to releaseDir.
func LinkRuntimeRelease(repoDir, releaseDir string) (bool, error) {
	if !isDirectory(releaseDir) {
		return false, nil
	}
	replaceable, err := clearRuntimeReleaseLinkTarget(repoDir)
	if err != nil || !replaceable {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		return false, err
	}
	return true, os.Symlink(releaseDir, repoDir)
}

func clearRuntimeReleaseLinkTarget(repoDir string) (bool, error) {
	info, err := os.Lstat(repoDir)
	switch {
	case os.IsNotExist(err):
		return true, nil
	case err != nil:
		return false, err
	case info.Mode()&os.ModeSymlink != 0:
		return true, os.Remove(repoDir)
	case info.IsDir() && dirIsEmpty(repoDir):
		return os.Remove(repoDir) == nil, nil
	}
	return false, nil
}

func dirIsEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) == 0
}
