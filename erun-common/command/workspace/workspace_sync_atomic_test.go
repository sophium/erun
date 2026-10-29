package workspace

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// These tests park a pass mid-transfer via the stub `ssh` so a partial state is deterministic.

const (
	workspaceSyncTestMTime    = 1700000000
	workspaceSyncTestBigBytes = 8 << 20
)

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

	// The stub announces after delivering half the archive and holds the rest until the gate opens.
	awaitWorkspaceSyncFile(t, gate+".reached")
	if got := readWorkspaceSyncFile(t, filepath.Join(mirror, "app", "big.bin")); !bytes.Equal(got, previous) {
		t.Fatalf("the mirror published a file that is still arriving: %d bytes at the final path, want the previous %d", len(got), len(previous))
	}
	staging := findWorkspaceSyncStagingDir(t, mirror)
	awaitWorkspaceSyncSize(t, filepath.Join(staging, "app", "big.bin"), 1<<20)
	stagingRel, relErr := filepath.Rel(mirror, staging)
	if relErr != nil {
		t.Fatalf("relativize staging dir: %v", relErr)
	}
	if SafeWorkspaceSyncPath(filepath.ToSlash(stagingRel) + "/app/big.bin") {
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
	staging := findWorkspaceSyncStagingDir(t, mirror)
	awaitWorkspaceSyncSize(t, filepath.Join(staging, "app", "big.bin"), 1<<20)
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

// A second pass must overwrite the read-only copy it wrote itself, which Windows would refuse.
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

// Parks a real tar mid-extraction while a concurrent pass prepares its own staging.
func TestPrepareWorkspaceSyncStagingNeverClobbersAConcurrentPass(t *testing.T) {
	mirror := t.TempDir()

	files := make(map[string][]byte, 200)
	paths := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("dir%d/file.bin", i)
		files[name] = bytes.Repeat([]byte{0x2a}, 4096)
		paths = append(paths, name)
	}
	archive := writeWorkspaceSyncArchive(t, files)
	gate := filepath.Join(t.TempDir(), "gate")

	stubWorkspaceSyncSSH(t, nil, nil)
	t.Setenv(workspaceSyncStubArchiveEnv, archive)
	t.Setenv(workspaceSyncStubGateEnv, gate)

	firstStaging, err := prepareWorkspaceSyncStaging(mirror)
	if err != nil {
		t.Fatalf("prepare first pass's staging: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- streamRemoteWorkspaceArchive(context.Background(), "pod", "/workspace", firstStaging, paths)
	}()

	awaitWorkspaceSyncFile(t, gate+".reached")

	if _, err := prepareWorkspaceSyncStaging(mirror); err != nil {
		t.Fatalf("prepare second, concurrent pass's staging: %v", err)
	}

	writeWorkspaceSyncFile(t, gate, []byte("go"))
	if err := <-done; err != nil {
		t.Fatalf("a concurrent pass starting up clobbered this pass's in-flight extraction: %v", err)
	}
	for name := range files {
		full := filepath.Join(firstStaging, filepath.FromSlash(name))
		if _, statErr := os.Stat(full); statErr != nil {
			t.Fatalf("first pass's staged file %s is missing after a concurrent pass started: %v", name, statErr)
		}
	}
}

func TestSweepStaleWorkspaceSyncStagingDirsOnlyRemovesTrulyOrphanedOnes(t *testing.T) {
	mirror := t.TempDir()

	stale := filepath.Join(mirror, workspaceSyncStagingSubdir+"-stale")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatalf("create stale staging dir: %v", err)
	}
	old := time.Now().Add(-2 * workspaceSyncStagingStaleAge)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("backdate stale staging dir: %v", err)
	}

	fresh := filepath.Join(mirror, workspaceSyncStagingSubdir+"-fresh")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatalf("create fresh staging dir: %v", err)
	}

	sweepStaleWorkspaceSyncStagingDirs(mirror)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale staging dir from a killed pass was not swept: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh staging dir belonging to a live pass was swept: %v", err)
	}
}

// Fixed mtime: a publish that rewrote rather than renamed would re-fetch every pass.
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

// These wait on the condition; the deadline only turns a hang into a failure.
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
	matches, err := filepath.Glob(filepath.Join(root, workspaceSyncStagingSubdir+"*"))
	if err != nil {
		t.Fatalf("glob staging debris in %s: %v", root, err)
	}
	if len(matches) > 0 {
		t.Fatalf("staging debris left in %s: %v", root, matches)
	}
}

// Staging dir names carry a random suffix.
func findWorkspaceSyncStagingDir(t *testing.T, root string) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		matches, err := filepath.Glob(filepath.Join(root, workspaceSyncStagingSubdir+"*"))
		if err == nil && len(matches) > 0 {
			return matches[0]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for a staging directory to appear under %s", root)
	return ""
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
