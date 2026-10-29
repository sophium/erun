package workspace

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests drive a whole pass against a stub `ssh`, since the defect was in the listing
// the delete step was handed.

const (
	workspaceSyncSSHStubEnv           = "ERUN_COMMON_TEST_SSH_STUB"
	workspaceSyncStubIndexEnv         = "ERUN_COMMON_TEST_SSH_STUB_INDEX"
	workspaceSyncStubMissingEnv       = "ERUN_COMMON_TEST_SSH_STUB_MISSING"
	workspaceSyncStubStatEnv          = "ERUN_COMMON_TEST_SSH_STUB_STAT"
	workspaceSyncStubOutputsEnv       = "ERUN_COMMON_TEST_SSH_STUB_OUTPUTS"
	workspaceSyncStubOutputsExitEnv   = "ERUN_COMMON_TEST_SSH_STUB_OUTPUTS_EXIT"
	workspaceSyncStubOutputsStderrEnv = "ERUN_COMMON_TEST_SSH_STUB_OUTPUTS_STDERR"
	workspaceSyncStubArchiveEnv       = "ERUN_COMMON_TEST_SSH_STUB_ARCHIVE"
	workspaceSyncStubGateEnv          = "ERUN_COMMON_TEST_SSH_STUB_GATE"
	workspaceSyncStubTruncateEnv      = "ERUN_COMMON_TEST_SSH_STUB_TRUNCATE"
	workspaceSyncStubFetchMarkerEnv   = "ERUN_COMMON_TEST_SSH_STUB_FETCH_MARKER"
)

func init() {
	reentryHelpers = append(reentryHelpers, func() (int, bool) {
		if os.Getenv(workspaceSyncSSHStubEnv) == "" {
			return 0, false
		}
		// Re-exec the test binary as the stub; a shell script is not executable on every host.
		return runWorkspaceSyncSSHStub(os.Args), true
	})
}

func runWorkspaceSyncSSHStub(args []string) int {
	script := ""
	if len(args) > 0 {
		script = args[len(args)-1]
	}
	switch {
	// The fingerprint listing contains the index listing, so match it first.
	case strings.Contains(script, "stat -c"):
		_, _ = os.Stdout.WriteString(os.Getenv(workspaceSyncStubStatEnv))
		return 0
	case strings.Contains(script, "git ls-files -coz"):
		writeNULList(os.Getenv(workspaceSyncStubIndexEnv))
		return 0
	case strings.Contains(script, "git ls-files -dz"):
		writeNULList(os.Getenv(workspaceSyncStubMissingEnv))
		return 0
	case strings.Contains(script, "git ls-files -sz"):
		return 0
	case strings.Contains(script, "sha256sum"):
		return runWorkspaceSyncSSHStubOutputsHash()
	case strings.Contains(script, "find . -type f"):
		return runWorkspaceSyncSSHStubOutputsListing()
	case strings.Contains(script, "tar --null"):
		return streamWorkspaceSyncStubArchive()
	}
	return 1
}

// runWorkspaceSyncSSHStubOutputsHash hashes real archive bytes so tests prove the hash is
// content-derived; a missing path hashes its name, which never equals a content hash.
func runWorkspaceSyncSSHStubOutputsHash() int {
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 1
	}
	contents := readStubArchiveContents(os.Getenv(workspaceSyncStubArchiveEnv))
	for _, part := range bytes.Split(stdin, []byte{0}) {
		if len(part) == 0 {
			continue
		}
		path := string(part)
		body, ok := contents[path]
		if !ok {
			body = []byte(path)
		}
		sum := sha256.Sum256(body)
		if _, err := os.Stdout.WriteString(hex.EncodeToString(sum[:]) + "  " + path + "\x00"); err != nil {
			return 1
		}
	}
	return 0
}

func readStubArchiveContents(path string) map[string][]byte {
	contents := make(map[string][]byte)
	if path == "" {
		return contents
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return contents
	}
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if err != nil {
			break
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			break
		}
		contents[header.Name] = body
	}
	return contents
}

// runWorkspaceSyncSSHStubOutputsListing: workspaceSyncStubOutputsExitEnv overrides the exit
// code; an empty listing reports the absent sentinel.
func runWorkspaceSyncSSHStubOutputsListing() int {
	if code := strings.TrimSpace(os.Getenv(workspaceSyncStubOutputsExitEnv)); code != "" {
		n, convErr := strconv.Atoi(code)
		if convErr != nil {
			return 1
		}
		if stderr := os.Getenv(workspaceSyncStubOutputsStderrEnv); stderr != "" {
			_, _ = os.Stderr.WriteString(stderr)
		}
		return n
	}
	outputs := os.Getenv(workspaceSyncStubOutputsEnv)
	if outputs == "" {
		return remoteOutputsDirAbsentExitCode
	}
	writeNULList(outputs)
	return 0
}

// streamWorkspaceSyncStubArchive's gate and truncate knobs park a pass mid-transfer or kill it there.
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
	_ = os.WriteFile(gate+".reached", []byte("reached"), 0o644)
	if !awaitWorkspaceSyncStubGate(gate) {
		return 1
	}
	_, _ = os.Stdout.Write(body[half:])
	return 0
}

// The interval is backoff; the stub proceeds on the gate file existing.
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

// Deletions must propagate from the worktree, not the index, even when the fetch fails.
func TestSyncWorkspaceOncePropagatesAnUnstagedWorktreeDeletion(t *testing.T) {
	mirror := t.TempDir()
	seedWorkspaceMirror(t, mirror, "app/keep.go", "app/gone.go")
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
