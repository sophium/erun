package spec

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// erunRepoRoot walks up to the directory holding both erun-common and erun-devops.
func erunRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	for dir := filepath.Dir(file); ; dir = filepath.Dir(dir) {
		if isDir(filepath.Join(dir, "erun-common")) && isDir(filepath.Join(dir, "erun-devops")) {
			return dir
		}
		if filepath.Dir(dir) == dir {
			t.Fatalf("no directory above %s holds erun-common and erun-devops", file)
		}
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
