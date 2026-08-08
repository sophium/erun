package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The host mirror deletes exactly what the pod's remote listing omits, so these
// tests drive a whole sync pass against a stub `ssh` — the level the mirror
// actually failed at. A helper-level test cannot see it: the delete helper was
// always correct, and the listing it was handed was not.

const (
	workspaceSyncSSHStubEnv     = "ERUN_UI_TEST_SSH_STUB"
	workspaceSyncStubIndexEnv   = "ERUN_UI_TEST_SSH_STUB_INDEX"
	workspaceSyncStubMissingEnv = "ERUN_UI_TEST_SSH_STUB_MISSING"
)

// TestMain doubles as the stub `ssh` binary: the test executable is copied onto
// a PATH the pass sees, and re-entering it with the stub env var set answers the
// remote commands instead of running the suite. Compiling the stub from the test
// binary keeps it executable on every host, which a shell script is not.
func TestMain(m *testing.M) {
	if os.Getenv(workspaceSyncSSHStubEnv) != "" {
		os.Exit(runWorkspaceSyncSSHStub(os.Args))
	}
	os.Exit(m.Run())
}

func runWorkspaceSyncSSHStub(args []string) int {
	script := ""
	if len(args) > 0 {
		script = args[len(args)-1]
	}
	switch {
	case strings.Contains(script, "git ls-files -coz"):
		// The index listing, which is what a worktree deletion does not change.
		writeNULList(os.Getenv(workspaceSyncStubIndexEnv))
		return 0
	case strings.Contains(script, "git ls-files -dz"):
		writeNULList(os.Getenv(workspaceSyncStubMissingEnv))
		return 0
	case strings.Contains(script, "git ls-files -sz"):
		return 0
	case strings.Contains(script, "stat -c"):
		// No fingerprints, so every remote path counts as changed and the pass
		// reaches the fetch step.
		return 0
	case strings.Contains(script, "tar --null"):
		// The fetch fails: deletions must still propagate.
		_, _ = os.Stderr.WriteString("stub: remote archive unavailable\n")
		return 1
	}
	// Anything else — the outputs listing — reports "nothing there yet".
	return 1
}

func writeNULList(commaSeparated string) {
	for _, item := range strings.Split(commaSeparated, ",") {
		if strings.TrimSpace(item) == "" {
			continue
		}
		_, _ = os.Stdout.WriteString(item + "\x00")
	}
}

func stubWorkspaceSyncSSH(t *testing.T, index, missing []string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	body, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	stubDir := t.TempDir()
	name := "ssh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(stubDir, name), body, 0o755); err != nil {
		t.Fatalf("write ssh stub: %v", err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(workspaceSyncSSHStubEnv, "1")
	t.Setenv(workspaceSyncStubIndexEnv, strings.Join(index, ","))
	t.Setenv(workspaceSyncStubMissingEnv, strings.Join(missing, ","))
}

func seedWorkspaceMirror(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, file := range files {
		full := filepath.Join(root, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", file, err)
		}
		if err := os.WriteFile(full, []byte(file), 0o644); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
	}
}

// TestSyncWorkspaceOncePropagatesAnUnstagedWorktreeDeletion is the regression:
// `git ls-files -c` reports the pod's index, so a file removed with a plain `rm`
// keeps appearing as a remote file and the mirror kept it indefinitely. The pass
// must reconcile against the worktree, and must do so even when the fetch step
// fails — a stranded fetch must never stall deletions.
func TestSyncWorkspaceOncePropagatesAnUnstagedWorktreeDeletion(t *testing.T) {
	mirror := t.TempDir()
	seedWorkspaceMirror(t, mirror, "app/keep.go", "app/gone.go")
	// gone.go is still in the pod's index and no longer in its worktree.
	stubWorkspaceSyncSSH(t, []string{"app/keep.go", "app/gone.go"}, []string{"app/gone.go"})

	result, err := syncWorkspaceOnce(context.Background(), workspaceSyncParams{
		HostAlias:  "pod",
		RemotePath: "/workspace",
		LocalPath:  mirror,
	})
	if err == nil {
		t.Fatal("expected the failing fetch to surface as an error")
	}
	if result.FilesDeleted != 1 {
		t.Fatalf("FilesDeleted = %d, want 1 despite the fetch failure", result.FilesDeleted)
	}
	if _, statErr := os.Stat(filepath.Join(mirror, "app", "gone.go")); !os.IsNotExist(statErr) {
		t.Fatalf("the pod-removed file is still in the mirror: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(mirror, "app", "keep.go")); statErr != nil {
		t.Fatalf("a file the pod still has was deleted from the mirror: %v", statErr)
	}
}

// A file present in both the index and the worktree is never deleted, so the
// worktree reconciliation cannot turn into a mirror that empties itself.
func TestSyncWorkspaceOnceKeepsFilesThePodStillHas(t *testing.T) {
	mirror := t.TempDir()
	seedWorkspaceMirror(t, mirror, "app/keep.go")
	stubWorkspaceSyncSSH(t, []string{"app/keep.go"}, nil)

	result, err := syncWorkspaceOnce(context.Background(), workspaceSyncParams{
		HostAlias:  "pod",
		RemotePath: "/workspace",
		LocalPath:  mirror,
	})
	if err == nil {
		t.Fatal("expected the failing fetch to surface as an error")
	}
	if result.FilesDeleted != 0 {
		t.Fatalf("FilesDeleted = %d, want 0", result.FilesDeleted)
	}
	if _, statErr := os.Stat(filepath.Join(mirror, "app", "keep.go")); statErr != nil {
		t.Fatalf("mirror file was deleted: %v", statErr)
	}
}
