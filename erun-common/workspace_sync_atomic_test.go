package eruncommon

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The mirror is the surface an orchestrator runs host-native artifacts from, so
// a reader of it must never see a prefix of a file that is still arriving. These
// tests drive a whole pass against the stub `ssh` from workspace_sync_pass_test.go
// and park it mid-transfer, so the partial state is a fact of the test rather
// than something it waits and hopes for.

const (
	workspaceSyncTestMTime    = 1700000000
	workspaceSyncTestBigBytes = 8 << 20
)

// TestSyncWorkspaceOnceNeverPublishesAPartialFile parks a pass with half the
// archive delivered and asserts the mirror still holds the previous, complete
// file. Before staging, tar extracted in place and this is exactly the window in
// which a copy came back short with no error from any step.
func TestSyncWorkspaceOnceNeverPublishesAPartialFile(t *testing.T) {
	mirror := t.TempDir()
	previous := []byte("the previous complete artifact")
	writeWorkspaceSyncFile(t, filepath.Join(mirror, "app", "big.bin"), previous)

	incoming := bytes.Repeat([]byte{0x7f}, workspaceSyncTestBigBytes)
	archive := writeWorkspaceSyncArchive(t, map[string][]byte{"app/big.bin": incoming})
	gate := filepath.Join(t.TempDir(), "gate")

	stubWorkspaceSyncSSH(t, []string{"app/big.bin"}, nil)
	t.Setenv(workspaceSyncStubArchiveEnv, archive)
	t.Setenv(workspaceSyncStubGateEnv, gate)

	done := make(chan error, 1)
	go func() {
		_, err := SyncWorkspaceOnce(context.Background(), WorkspaceSyncParams{
			HostAlias:  "pod",
			RemotePath: "/workspace",
			LocalPath:  mirror,
		})
		done <- err
	}()

	// The stub announces only after handing the extractor the first half, then
	// holds the rest until the gate opens — so every assertion below runs against
	// a transfer that has plainly started and provably cannot finish.
	awaitWorkspaceSyncFile(t, gate+".reached")
	if got := readWorkspaceSyncFile(t, filepath.Join(mirror, "app", "big.bin")); !bytes.Equal(got, previous) {
		t.Fatalf("the mirror published a file that is still arriving: %d bytes at the final path, want the previous %d", len(got), len(previous))
	}
	// The one partial state is in a directory that says so by name, and the
	// source lane refuses to treat it as mirror content.
	awaitWorkspaceSyncSize(t, filepath.Join(mirror, workspaceSyncStagingSubdir, "app", "big.bin"), 1<<20)
	if SafeWorkspaceSyncPath(workspaceSyncStagingSubdir + "/app/big.bin") {
		t.Fatal("staged bytes must not be a path the mirror lanes accept as content")
	}

	writeWorkspaceSyncFile(t, gate, []byte("go"))
	if err := <-done; err != nil {
		t.Fatalf("sync pass: %v", err)
	}
	if got := readWorkspaceSyncFile(t, filepath.Join(mirror, "app", "big.bin")); !bytes.Equal(got, incoming) {
		t.Fatalf("published file is %d bytes, want the complete %d", len(got), len(incoming))
	}
	requireNoWorkspaceSyncStaging(t, mirror)
}

// A fetch that dies mid-stream must leave the mirror on its previous content and
// must not litter it with debris a later pass would read as content.
func TestSyncWorkspaceOnceLeavesNoDebrisWhenTheFetchFails(t *testing.T) {
	mirror := t.TempDir()
	previous := []byte("the previous complete artifact")
	writeWorkspaceSyncFile(t, filepath.Join(mirror, "app", "big.bin"), previous)

	incoming := bytes.Repeat([]byte{0x7f}, workspaceSyncTestBigBytes)
	archive := writeWorkspaceSyncArchive(t, map[string][]byte{"app/big.bin": incoming})

	stubWorkspaceSyncSSH(t, []string{"app/big.bin"}, nil)
	t.Setenv(workspaceSyncStubArchiveEnv, archive)
	t.Setenv(workspaceSyncStubTruncateEnv, strconv.Itoa(workspaceSyncTestBigBytes/2))

	_, err := SyncWorkspaceOnce(context.Background(), WorkspaceSyncParams{
		HostAlias:  "pod",
		RemotePath: "/workspace",
		LocalPath:  mirror,
	})
	if err == nil {
		t.Fatal("expected a fetch that ended early to surface as an error")
	}
	if got := readWorkspaceSyncFile(t, filepath.Join(mirror, "app", "big.bin")); !bytes.Equal(got, previous) {
		t.Fatalf("a failed fetch corrupted the previous content: %d bytes, want %d", len(got), len(previous))
	}
	requireNoWorkspaceSyncStaging(t, mirror)
	requireWorkspaceSyncMirrorFiles(t, mirror, []string{"app/big.bin"})
}

// A cancelled context kills the transfer wherever it happens to be, and the
// mirror must come out of it the same way a failed fetch leaves it.
func TestSyncWorkspaceOnceLeavesNoDebrisWhenTheContextIsCancelled(t *testing.T) {
	mirror := t.TempDir()
	previous := []byte("the previous complete artifact")
	writeWorkspaceSyncFile(t, filepath.Join(mirror, "app", "big.bin"), previous)

	incoming := bytes.Repeat([]byte{0x7f}, workspaceSyncTestBigBytes)
	archive := writeWorkspaceSyncArchive(t, map[string][]byte{"app/big.bin": incoming})
	gate := filepath.Join(t.TempDir(), "gate")

	stubWorkspaceSyncSSH(t, []string{"app/big.bin"}, nil)
	t.Setenv(workspaceSyncStubArchiveEnv, archive)
	t.Setenv(workspaceSyncStubGateEnv, gate)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := SyncWorkspaceOnce(ctx, WorkspaceSyncParams{
			HostAlias:  "pod",
			RemotePath: "/workspace",
			LocalPath:  mirror,
		})
		done <- err
	}()

	awaitWorkspaceSyncFile(t, gate+".reached")
	awaitWorkspaceSyncSize(t, filepath.Join(mirror, workspaceSyncStagingSubdir, "app", "big.bin"), 1<<20)
	cancel()

	if err := <-done; err == nil {
		t.Fatal("expected a cancelled pass to surface as an error")
	}
	if got := readWorkspaceSyncFile(t, filepath.Join(mirror, "app", "big.bin")); !bytes.Equal(got, previous) {
		t.Fatalf("a cancelled fetch corrupted the previous content: %d bytes, want %d", len(got), len(previous))
	}
	requireNoWorkspaceSyncStaging(t, mirror)
	requireWorkspaceSyncMirrorFiles(t, mirror, []string{"app/big.bin"})
}

// The outputs lane keeps its read-only marking and its pruning across the
// staged publish, and a second pass still overwrites the read-only copy it wrote
// itself — the rename lands on a file the previous pass stripped write access
// from, which is what Windows would otherwise refuse.
func TestSyncOutputsArtifactsPublishesCompletelyAndKeepsItsMarkingAndPruning(t *testing.T) {
	mirror := t.TempDir()
	artifacts := filepath.Join(mirror, WorkspaceSyncArtifactsSubdir)
	writeWorkspaceSyncFile(t, filepath.Join(artifacts, "bin", "stale.bin"), []byte("no longer in the pod"))

	body := bytes.Repeat([]byte{0x2a}, 1<<20)
	archive := writeWorkspaceSyncArchive(t, map[string][]byte{"bin/tool": body})

	stubWorkspaceSyncSSH(t, nil, nil)
	t.Setenv(workspaceSyncStubOutputsEnv, "bin/tool")
	t.Setenv(workspaceSyncStubArchiveEnv, archive)

	result, err := SyncWorkspaceOnce(context.Background(), WorkspaceSyncParams{
		HostAlias:  "pod",
		RemotePath: "/workspace",
		LocalPath:  mirror,
	})
	if err != nil {
		t.Fatalf("sync pass: %v", err)
	}
	if result.ArtifactsCopied != 1 {
		t.Fatalf("ArtifactsCopied = %d, want 1", result.ArtifactsCopied)
	}
	tool := filepath.Join(artifacts, "bin", "tool")
	if got := readWorkspaceSyncFile(t, tool); !bytes.Equal(got, body) {
		t.Fatalf("published artifact is %d bytes, want the complete %d", len(got), len(body))
	}
	info, statErr := os.Stat(tool)
	if statErr != nil {
		t.Fatalf("stat published artifact: %v", statErr)
	}
	if info.Mode().Perm()&0o200 != 0 {
		t.Fatalf("the mirrored artifact must stay read-only, got mode %v", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(artifacts, "bin", "stale.bin")); !os.IsNotExist(err) {
		t.Fatalf("an artifact the pod no longer has was not pruned: %v", err)
	}
	requireNoWorkspaceSyncStaging(t, artifacts)

	// A second pass republishes over the read-only copy the first one left.
	if _, err := SyncWorkspaceOnce(context.Background(), WorkspaceSyncParams{
		HostAlias:  "pod",
		RemotePath: "/workspace",
		LocalPath:  mirror,
	}); err != nil {
		t.Fatalf("second sync pass: %v", err)
	}
	if got := readWorkspaceSyncFile(t, tool); !bytes.Equal(got, body) {
		t.Fatalf("republished artifact is %d bytes, want the complete %d", len(got), len(body))
	}
	requireNoWorkspaceSyncStaging(t, artifacts)
}

// writeWorkspaceSyncArchive builds the tar the stub streams. Entries carry a
// fixed mtime because the mirror's incremental check compares it: a publish that
// rewrote the file instead of renaming it would re-fetch on every pass.
func writeWorkspaceSyncArchive(t *testing.T, files map[string][]byte) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for name, body := range files {
		header := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			ModTime:  time.Unix(workspaceSyncTestMTime, 0),
			Typeflag: tar.TypeReg,
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("write archive header %s: %v", name, err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatalf("write archive body %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	path := filepath.Join(t.TempDir(), "archive.tar")
	if err := os.WriteFile(path, buffer.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return path
}

func writeWorkspaceSyncFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readWorkspaceSyncFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return body
}

// awaitWorkspaceSyncFile and awaitWorkspaceSyncSize wait on the condition the
// test needs, never on a duration; the deadline only turns a hang into a
// readable failure.
func awaitWorkspaceSyncFile(t *testing.T, path string) {
	t.Helper()
	awaitWorkspaceSyncCondition(t, "file "+path+" to appear", func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
}

func awaitWorkspaceSyncSize(t *testing.T, path string, minimum int64) {
	t.Helper()
	awaitWorkspaceSyncCondition(t, "file "+path+" to reach "+strconv.FormatInt(minimum, 10)+" bytes", func() bool {
		info, err := os.Stat(path)
		return err == nil && info.Size() >= minimum
	})
}

func awaitWorkspaceSyncCondition(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func requireNoWorkspaceSyncStaging(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, workspaceSyncStagingSubdir)); !os.IsNotExist(err) {
		t.Fatalf("staging debris left in %s: %v", root, err)
	}
}

func requireWorkspaceSyncMirrorFiles(t *testing.T, root string, want []string) {
	t.Helper()
	meta, err := localWorkspaceSourceFileMeta(root)
	if err != nil {
		t.Fatalf("fingerprint mirror: %v", err)
	}
	got := sortedWorkspaceFileMetaKeys(meta)
	if len(got) != len(want) {
		t.Fatalf("mirror holds %v, want exactly %v", got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("mirror holds %v, want exactly %v", got, want)
		}
	}
}
