package podruntime

import (
	"os"
	"os/exec"
)

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// isDirectoryNotSymlink is isDirectory without following a final symlink.
func isDirectoryNotSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func readFileIfReadable(path string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	return data, err == nil
}
