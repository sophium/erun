package eruncommon

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The host mirror deletes exactly what the pod's remote listing omits, so these
// tests drive a whole sync pass against a stub `ssh` — the level the mirror
// actually failed at. A helper-level test cannot see it: the delete helper was
// always correct, and the listing it was handed was not.

const (
	workspaceSyncSSHStubEnv         = "ERUN_COMMON_TEST_SSH_STUB"
	workspaceSyncStubIndexEnv       = "ERUN_COMMON_TEST_SSH_STUB_INDEX"
	workspaceSyncStubMissingEnv     = "ERUN_COMMON_TEST_SSH_STUB_MISSING"
	workspaceSyncStubStatEnv        = "ERUN_COMMON_TEST_SSH_STUB_STAT"
	workspaceSyncStubOutputsEnv     = "ERUN_COMMON_TEST_SSH_STUB_OUTPUTS"
	workspaceSyncStubOutputsExitEnv = "ERUN_COMMON_TEST_SSH_STUB_OUTPUTS_EXIT"
	workspaceSyncStubArchiveEnv     = "ERUN_COMMON_TEST_SSH_STUB_ARCHIVE"
	workspaceSyncStubGateEnv        = "ERUN_COMMON_TEST_SSH_STUB_GATE"
	workspaceSyncStubTruncateEnv    = "ERUN_COMMON_TEST_SSH_STUB_TRUNCATE"
	workspaceSyncStubFetchMarkerEnv = "ERUN_COMMON_TEST_SSH_STUB_FETCH_MARKER"
)

// TestMain doubles as the stub `ssh` binary: the test executable is copied onto
// a PATH the pass sees, and re-entering it with the stub env var set answers the
// remote commands instead of running the suite. Compiling the stub from the test
// binary keeps it executable on every host, which a shell script is not.
func TestMain(m *testing.M) {
	if os.Getenv(workspaceSyncSSHStubEnv) != "" {
		os.Exit(runWorkspaceSyncSSHStub(os.Args))
	}
	// Only one TestMain is allowed per test binary, so the job-alive-contract
	// test's re-entered supervisor helper (job_alive_test.go) hooks in here too.
	if os.Getenv(jobAliveSupervisorHelperEnv) != "" {
		os.Exit(runJobAliveSupervisorHelper())
	}
	os.Exit(m.Run())
}

func runWorkspaceSyncSSHStub(args []string) int {
	script := ""
	if len(args) > 0 {
		script = args[len(args)-1]
	}
	switch {
	// The fingerprint listing pipes the index listing into stat, so it has to be
	// matched before the plain index listing it contains.
	case strings.Contains(script, "stat -c"):
		_, _ = os.Stdout.WriteString(os.Getenv(workspaceSyncStubStatEnv))
		return 0
	case strings.Contains(script, "git ls-files -coz"):
		// The index listing, which is what a worktree deletion does not change.
		writeNULList(os.Getenv(workspaceSyncStubIndexEnv))
		return 0
	case strings.Contains(script, "git ls-files -dz"):
		writeNULList(os.Getenv(workspaceSyncStubMissingEnv))
		return 0
	case strings.Contains(script, "git ls-files -sz"):
		return 0
	case strings.Contains(script, "find . -type f"):
		if code := strings.TrimSpace(os.Getenv(workspaceSyncStubOutputsExitEnv)); code != "" {
			n, convErr := strconv.Atoi(code)
			if convErr != nil {
				return 1
			}
			return n
		}
		outputs := os.Getenv(workspaceSyncStubOutputsEnv)
		if outputs == "" {
			// `cd` into a not-yet-created outputs dir: the script's own sentinel
			// for "confirmed absent" (see remoteOutputsDirAbsentExitCode).
			return remoteOutputsDirAbsentExitCode
		}
		writeNULList(outputs)
		return 0
	case strings.Contains(script, "tar --null"):
		return streamWorkspaceSyncStubArchive()
	}
	return 1
}

// streamWorkspaceSyncStubArchive plays back a prepared archive so a pass reaches
// its extract step for real. The gate and truncate knobs place a pass at a
// chosen point — mid-transfer, or dead mid-transfer — which is where a mirror
// that published in place handed out a prefix.
func streamWorkspaceSyncStubArchive() int {
	if marker := os.Getenv(workspaceSyncStubFetchMarkerEnv); marker != "" {
		_ = os.WriteFile(marker, []byte("fetched"), 0o644)
	}
	archive := os.Getenv(workspaceSyncStubArchiveEnv)
	if archive == "" {
		_, _ = os.Stderr.WriteString("stub: remote archive unavailable\n")
		return 1
	}
	body, err := os.ReadFile(archive)
	if err != nil {
		_, _ = os.Stderr.WriteString("stub: " + err.Error() + "\n")
		return 1
	}
	if truncate, convErr := strconv.Atoi(os.Getenv(workspaceSyncStubTruncateEnv)); convErr == nil && truncate < len(body) {
		_, _ = os.Stdout.Write(body[:truncate])
		_, _ = os.Stderr.WriteString("stub: remote archive ended early\n")
		return 1
	}
	gate := os.Getenv(workspaceSyncStubGateEnv)
	if gate == "" {
		_, _ = os.Stdout.Write(body)
		return 0
	}
	half := len(body) / 2
	if _, err := os.Stdout.Write(body[:half]); err != nil {
		return 1
	}
	// Announce only once the bytes are with the extractor, then hold until the
	// test has finished looking at the mirror.
	_ = os.WriteFile(gate+".reached", []byte("reached"), 0o644)
	if !awaitWorkspaceSyncStubGate(gate) {
		return 1
	}
	_, _ = os.Stdout.Write(body[half:])
	return 0
}

// awaitWorkspaceSyncStubGate blocks until the test opens the gate. The interval
// is backoff, not synchronisation: the stub proceeds on the file existing, never
// on elapsed time.
func awaitWorkspaceSyncStubGate(gate string) bool {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(gate); err == nil {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
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

	result, err := SyncWorkspaceOnce(context.Background(), WorkspaceSyncParams{
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

	result, err := SyncWorkspaceOnce(context.Background(), WorkspaceSyncParams{
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
