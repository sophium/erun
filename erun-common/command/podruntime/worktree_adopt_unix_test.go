//go:build !windows

package podruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdoptWorktreeCopyKeepsModesHardLinksAndReadOnlyDirectories(t *testing.T) {
	f := newAdoptFixture(t)
	f.seedLegacyRepo(t)
	script := filepath.Join(f.legacy, "bin", "run.sh")
	mustWriteFile(t, script, "#!/bin/sh\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	// git clone --local hard-links objects; the copy keeps them linked.
	object := filepath.Join(f.legacy, ".git", "objects", "ab", "cdef")
	mustWriteFile(t, object, "blob\n")
	if err := os.Link(object, filepath.Join(f.legacy, "linked-object")); err != nil {
		t.Fatal(err)
	}
	readOnly := filepath.Join(f.legacy, "vendor")
	mustWriteFile(t, filepath.Join(readOnly, "pinned.txt"), "pinned\n")
	if err := os.Chmod(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(f.claim, "vendor"), 0o755)
		_ = os.Chmod(filepath.Join(f.preserved(), "vendor"), 0o755)
		_ = os.Chmod(readOnly, 0o755)
	})

	f.adopt(t, false)

	assertPerm(t, filepath.Join(f.claim, "bin", "run.sh"), 0o755)
	assertSameFile(t, filepath.Join(f.claim, ".git", "objects", "ab", "cdef"), filepath.Join(f.claim, "linked-object"))
	assertPerm(t, filepath.Join(f.claim, "vendor"), 0o555)
	assertExists(t, filepath.Join(f.claim, "vendor", "pinned.txt"), "a read-only directory's contents must still be copied")
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %s, want %s", path, info.Mode().Perm(), want)
	}
}

func assertSameFile(t *testing.T, first, second string) {
	t.Helper()
	a, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(a, b) {
		t.Fatal("hard-linked files should stay one file on the worktree volume")
	}
}
