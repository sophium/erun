package eruncommon

import (
	"os"
	"path/filepath"
)

// RuntimeReleaseDir is where a tenant's <tenant>-devops image bakes its release
// artifacts (terraform, ...). It lives outside /home/erun on purpose: the home
// volume is mounted over /home/erun and would shadow anything baked under it.
const RuntimeReleaseDir = "/opt/erun/release"

// LinkRuntimeRelease surfaces an image-baked release tree in a sourceless
// runtime environment's git folder by making repoDir a symlink to releaseDir,
// so the tree always reflects the deployed image. It never clobbers source:
// a missing releaseDir changes nothing, an existing symlink (ours, or stale
// from an earlier image) is relinked, an empty directory (the boot stub) is
// replaced, and a populated directory -- a mounted or cloned worktree -- is
// left untouched. It reports whether repoDir now links to releaseDir.
func LinkRuntimeRelease(repoDir, releaseDir string) (bool, error) {
	if info, err := os.Stat(releaseDir); err != nil || !info.IsDir() {
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

// clearRuntimeReleaseLinkTarget removes what may be replaced by the link -- an
// existing symlink or an empty directory -- and reports whether the path is now
// free. A populated directory, or anything else, is never touched.
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
