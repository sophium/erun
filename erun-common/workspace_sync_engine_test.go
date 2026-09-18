package eruncommon

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseWorkspaceSyncPathListFiltersUnsafePaths(t *testing.T) {
	got := parseWorkspaceSyncPathList([]byte("app/main.go\x00.git/config\x00../outside\x00node_modules/pkg/index.js\x00app/main.go\x00space name.go\x00"))
	want := []string{"app/main.go", "node_modules/pkg/index.js", "space name.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected paths: got %+v want %+v", got, want)
	}
}

func TestWorkspaceSyncSSHArgsAreNonInteractiveAndDoNotPolluteKnownHosts(t *testing.T) {
	got := workspaceSyncSSHArgs(" erun-frs-dev ", "true")
	want := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"erun-frs-dev",
		"true",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected ssh args: got %+v want %+v", got, want)
	}
}

func TestDeleteLocalWorkspaceFilesNotInRemoteOnlyDeletesGitVisiblePaths(t *testing.T) {
	root := t.TempDir()
	requireWorkspaceSyncNoError(t, os.MkdirAll(filepath.Join(root, "app"), 0o755), "mkdir app")
	requireWorkspaceSyncNoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755), "mkdir .git")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, "app", "keep.go"), []byte("keep"), 0o644), "write keep")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, "app", "delete.go"), []byte("delete"), 0o644), "write delete")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, ".git", "config"), []byte("git"), 0o644), "write git config")

	deleted, err := deleteLocalWorkspaceFilesNotInRemote(root, []string{"app/keep.go", "app/delete.go", ".git/config"}, []string{"app/keep.go"})
	requireWorkspaceSyncNoError(t, err, "delete local files")
	if deleted != 1 {
		t.Fatalf("expected one deleted file, got %d", deleted)
	}
	if _, err := os.Stat(filepath.Join(root, "app", "keep.go")); err != nil {
		t.Fatalf("expected keep file to remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "app", "delete.go")); !os.IsNotExist(err) {
		t.Fatalf("expected delete file to be removed, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "config")); err != nil {
		t.Fatalf("expected .git config to remain: %v", err)
	}
}

func TestSafeWorkspaceSyncPathRejectsArtifactMirrorSubdir(t *testing.T) {
	for _, p := range []string{WorkspaceSyncArtifactsSubdir, WorkspaceSyncArtifactsSubdir + "/erun-app.exe"} {
		if SafeWorkspaceSyncPath(p) {
			t.Errorf("expected source lane to reject artifact-mirror path %q", p)
		}
	}
	// A distinct sibling that merely shares the prefix stays in the source lane.
	if !SafeWorkspaceSyncPath(".erun-outputs-notes/readme.md") {
		t.Error("expected a distinct sibling path to remain allowed")
	}
}

func TestListLocalArtifactFiles(t *testing.T) {
	root := t.TempDir()
	requireWorkspaceSyncNoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755), "mkdir sub")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, "erun-app.exe"), []byte("x"), 0o644), "write exe")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, "sub", "report.txt"), []byte("y"), 0o644), "write report")

	got, err := ListLocalArtifactFiles(root)
	requireWorkspaceSyncNoError(t, err, "list artifacts")
	want := []string{"erun-app.exe", "sub/report.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected artifact files: got %+v want %+v", got, want)
	}
}

func TestListLocalArtifactFilesMissingRootIsEmpty(t *testing.T) {
	got, err := ListLocalArtifactFiles(filepath.Join(t.TempDir(), "does-not-exist"))
	requireWorkspaceSyncNoError(t, err, "list missing artifacts")
	if len(got) != 0 {
		t.Fatalf("expected no files for a missing root, got %+v", got)
	}
}

func TestPruneLocalArtifactsRemovesStaleAndKeepsPresent(t *testing.T) {
	root := t.TempDir()
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, "keep.exe"), []byte("k"), 0o644), "write keep")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, "stale.exe"), []byte("s"), 0o644), "write stale")

	requireWorkspaceSyncNoError(t, pruneLocalArtifacts(root, []string{"keep.exe"}), "prune")
	if _, err := os.Stat(filepath.Join(root, "keep.exe")); err != nil {
		t.Fatalf("expected keep.exe to remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "stale.exe")); !os.IsNotExist(err) {
		t.Fatalf("expected stale.exe to be pruned, got %v", err)
	}
}

func TestArtifactReadOnlyRoundTrip(t *testing.T) {
	root := t.TempDir()
	artifact := filepath.Join(root, "erun-app.exe")
	requireWorkspaceSyncNoError(t, os.WriteFile(artifact, []byte("x"), 0o644), "write exe")

	requireWorkspaceSyncNoError(t, markArtifactsReadOnly(root, []string{"erun-app.exe"}), "mark read-only")
	info, err := os.Stat(artifact)
	requireWorkspaceSyncNoError(t, err, "stat after read-only")
	if info.Mode().Perm()&0o200 != 0 {
		t.Fatalf("expected write bit cleared on the read-only mirror, got mode %v", info.Mode().Perm())
	}

	requireWorkspaceSyncNoError(t, makeArtifactsWritable(root, []string{"erun-app.exe"}), "make writable")
	info, err = os.Stat(artifact)
	requireWorkspaceSyncNoError(t, err, "stat after writable")
	if info.Mode().Perm()&0o200 == 0 {
		t.Fatalf("expected write bit restored before refresh, got mode %v", info.Mode().Perm())
	}
}

func TestEnsureLocalWorkspaceSyncTargetAcceptsPlainDirAndCreatesMissing(t *testing.T) {
	// An existing plain directory is accepted without any local git.
	existing := t.TempDir()
	requireWorkspaceSyncNoError(t, EnsureLocalWorkspaceSyncTarget(existing), "accept plain dir")

	// A missing directory is created.
	missing := filepath.Join(t.TempDir(), "nested", "mirror")
	requireWorkspaceSyncNoError(t, EnsureLocalWorkspaceSyncTarget(missing), "create missing dir")
	info, err := os.Stat(missing)
	requireWorkspaceSyncNoError(t, err, "stat created dir")
	if !info.IsDir() {
		t.Fatal("expected the created path to be a directory")
	}

	// A path that is a file, not a directory, is rejected.
	file := filepath.Join(t.TempDir(), "afile")
	requireWorkspaceSyncNoError(t, os.WriteFile(file, []byte("x"), 0o644), "write file")
	if err := EnsureLocalWorkspaceSyncTarget(file); err == nil {
		t.Fatal("expected a non-directory path to be rejected")
	}
}

func TestLocalWorkspaceSourceFileMetaSkipsGitAndArtifacts(t *testing.T) {
	root := t.TempDir()
	requireWorkspaceSyncNoError(t, os.MkdirAll(filepath.Join(root, "app"), 0o755), "mkdir app")
	requireWorkspaceSyncNoError(t, os.MkdirAll(filepath.Join(root, ".git", "refs"), 0o755), "mkdir .git")
	requireWorkspaceSyncNoError(t, os.MkdirAll(filepath.Join(root, WorkspaceSyncArtifactsSubdir), 0o755), "mkdir outputs")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("readme"), 0o644), "write readme")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, "app", "main.go"), []byte("m"), 0o644), "write main")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, ".git", "config"), []byte("g"), 0o644), "write git config")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, ".git", "refs", "head"), []byte("h"), 0o644), "write git ref")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, WorkspaceSyncArtifactsSubdir, "erun-app.exe"), []byte("e"), 0o644), "write artifact")

	meta, err := localWorkspaceSourceFileMeta(root)
	requireWorkspaceSyncNoError(t, err, "fingerprint source files")
	want := []string{"README.md", "app/main.go"}
	if got := sortedWorkspaceFileMetaKeys(meta); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected source files: got %+v want %+v", got, want)
	}
	if meta["README.md"].Size != int64(len("readme")) {
		t.Fatalf("unexpected README.md size: got %d want %d", meta["README.md"].Size, len("readme"))
	}
}

func TestLocalWorkspaceSourceFileMetaMissingRootIsEmpty(t *testing.T) {
	meta, err := localWorkspaceSourceFileMeta(filepath.Join(t.TempDir(), "does-not-exist"))
	requireWorkspaceSyncNoError(t, err, "fingerprint missing root")
	if len(meta) != 0 {
		t.Fatalf("expected no files for a missing root, got %+v", meta)
	}
}

func TestChangedWorkspaceSyncPathsFetchesNewChangedAndUnknown(t *testing.T) {
	// remotePaths order is preserved in the output.
	remotePaths := []string{"changed.txt", "new.txt", "same.txt", "unknown-remote.txt"}
	remoteMeta := map[string]workspaceFileMeta{
		"changed.txt": {Size: 10, MTime: 200},
		"new.txt":     {Size: 5, MTime: 100},
		"same.txt":    {Size: 7, MTime: 150},
		// unknown-remote.txt intentionally absent → fetch when unsure.
	}
	localMeta := map[string]workspaceFileMeta{
		"changed.txt":        {Size: 10, MTime: 100}, // mtime differs → fetch
		"same.txt":           {Size: 7, MTime: 150},  // identical → skip
		"unknown-remote.txt": {Size: 1, MTime: 1},
		// new.txt absent locally → fetch.
	}
	got := changedWorkspaceSyncPaths(remotePaths, remoteMeta, localMeta)
	want := []string{"changed.txt", "new.txt", "unknown-remote.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected changed paths: got %+v want %+v", got, want)
	}
}

func TestChangedWorkspaceSyncPathsSkipsWhenFingerprintsMatch(t *testing.T) {
	remotePaths := []string{"a.txt", "b.txt"}
	meta := map[string]workspaceFileMeta{
		"a.txt": {Size: 3, MTime: 42},
		"b.txt": {Size: 4, MTime: 43},
	}
	if got := changedWorkspaceSyncPaths(remotePaths, meta, meta); len(got) != 0 {
		t.Fatalf("expected no fetches when every fingerprint matches, got %+v", got)
	}
}

func TestParseWorkspaceFileMeta(t *testing.T) {
	out := "12 1700000000 README.md\n34 1700000005 dir/with space.txt\nbad line\n56 1700000009 .git/config\n"
	meta := parseWorkspaceFileMeta(out)
	if len(meta) != 2 {
		t.Fatalf("expected 2 valid entries (bad line + .git skipped), got %d: %+v", len(meta), meta)
	}
	if meta["README.md"] != (workspaceFileMeta{Size: 12, MTime: 1700000000}) {
		t.Fatalf("unexpected README.md meta: %+v", meta["README.md"])
	}
	if meta["dir/with space.txt"] != (workspaceFileMeta{Size: 34, MTime: 1700000005}) {
		t.Fatalf("unexpected spaced-path meta: %+v", meta["dir/with space.txt"])
	}
	if _, ok := meta[".git/config"]; ok {
		t.Fatal("expected .git path to be skipped by SafeWorkspaceSyncPath")
	}
}

func TestParseGitLsFilesSymlinkPaths(t *testing.T) {
	// `git ls-files -sz` records: "<mode> <object> <stage>\t<path>", NUL-delimited.
	out := []byte("120000 aaa 0\tCLAUDE.md\x00100644 bbb 0\tREADME.md\x00120000 ccc 0\terun-ui/CLAUDE.md\x00100755 ddd 0\tscripts/run.sh\x00")
	got := parseGitLsFilesSymlinkPaths(out)
	if len(got) != 2 {
		t.Fatalf("expected 2 symlinks, got %d: %v", len(got), got)
	}
	for _, want := range []string{"CLAUDE.md", "erun-ui/CLAUDE.md"} {
		if _, ok := got[want]; !ok {
			t.Errorf("expected symlink %q in set", want)
		}
	}
	if _, ok := got["README.md"]; ok {
		t.Error("regular file README.md must not be treated as a symlink")
	}
}

func TestExcludeWorkspaceSyncPaths(t *testing.T) {
	paths := []string{"AGENTS.md", "CLAUDE.md", "erun-ui/CLAUDE.md", "erun-ui/app.go"}
	symlinks := map[string]struct{}{"CLAUDE.md": {}, "erun-ui/CLAUDE.md": {}}
	got := excludeWorkspaceSyncPaths(paths, symlinks)
	want := []string{"AGENTS.md", "erun-ui/app.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected filtered paths: got %v want %v", got, want)
	}
	// An empty symlink set is a no-op (returns the input).
	if got := excludeWorkspaceSyncPaths(paths, nil); !reflect.DeepEqual(got, paths) {
		t.Fatalf("nil symlink set must be a no-op, got %v", got)
	}
}

func requireWorkspaceSyncNoError(t *testing.T, err error, context string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", context, err)
	}
}
