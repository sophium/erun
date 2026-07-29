package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
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

func TestStartWorkspaceSyncForSelectionRequiresRemoteSSHDWorkspaceSync(t *testing.T) {
	called := make(chan workspaceSyncParams, 1)
	app := NewApp(erunUIDeps{
		store: workspaceSyncStore(true),
		canConnectLocalPort: func(int) bool {
			return true
		},
		workspaceSyncReady: func(context.Context, string) error {
			return nil
		},
		syncWorkspace: func(_ context.Context, params workspaceSyncParams) (workspaceSyncResult, error) {
			called <- params
			return workspaceSyncResult{FilesCopied: 2}, nil
		},
		workspaceSyncInterval: time.Hour,
	})
	defer app.shutdown(context.Background())

	app.startWorkspaceSyncForSelection(uiSelection{Tenant: "frs", Environment: "dev"})

	select {
	case params := <-called:
		if params.HostAlias != "erun-frs-dev" || params.RemotePath != "/home/erun/git/frs" || params.LocalPath != "/tmp/frs-local" {
			t.Fatalf("unexpected sync params: %+v", params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected workspace sync to start")
	}
}

func TestStartWorkspaceSyncForSelectionSkipsWhenSSHDWorkspaceSyncDisabled(t *testing.T) {
	called := make(chan workspaceSyncParams, 1)
	app := NewApp(erunUIDeps{
		store: workspaceSyncStore(false),
		canConnectLocalPort: func(int) bool {
			return true
		},
		workspaceSyncReady: func(context.Context, string) error {
			return nil
		},
		syncWorkspace: func(_ context.Context, params workspaceSyncParams) (workspaceSyncResult, error) {
			called <- params
			return workspaceSyncResult{}, nil
		},
		workspaceSyncInterval: time.Hour,
	})
	defer app.shutdown(context.Background())

	app.startWorkspaceSyncForSelection(uiSelection{Tenant: "frs", Environment: "dev"})

	select {
	case params := <-called:
		t.Fatalf("did not expect workspace sync to start, got %+v", params)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStartSessionStartsConfiguredWorkspaceSync(t *testing.T) {
	called := make(chan workspaceSyncParams, 1)
	app := NewApp(erunUIDeps{
		store:          workspaceSyncStore(true),
		resolveCLIPath: func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		canConnectLocalPort: func(int) bool {
			return true
		},
		workspaceSyncReady: func(context.Context, string) error {
			return nil
		},
		syncWorkspace: func(_ context.Context, params workspaceSyncParams) (workspaceSyncResult, error) {
			called <- params
			return workspaceSyncResult{FilesCopied: 2}, nil
		},
		workspaceSyncInterval: time.Hour,
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartSession(uiSelection{Tenant: "frs", Environment: "dev"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	select {
	case params := <-called:
		if params.HostAlias != "erun-frs-dev" || params.RemotePath != "/home/erun/git/frs" || params.LocalPath != "/tmp/frs-local" {
			t.Fatalf("unexpected sync params: %+v", params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected opening a configured environment to start workspace sync")
	}
}

func TestSaveEnvironmentConfigPersistsWorkspaceSyncLocalPath(t *testing.T) {
	// A plain (non-git) directory is a valid sync target: the mirror is a
	// one-way copy the sync owns and needs no local git.
	localRoot := t.TempDir()
	store := workspaceSyncStore(true)
	env := store.envs["frs/dev"]
	env.SSHD.WorkspaceSync.LocalPath = ""
	store.envs["frs/dev"] = env
	started := make(chan workspaceSyncParams, 1)
	app := NewApp(erunUIDeps{
		store: store,
		canConnectLocalPort: func(int) bool {
			return true
		},
		workspaceSyncReady: func(context.Context, string) error {
			return nil
		},
		syncWorkspace: func(_ context.Context, params workspaceSyncParams) (workspaceSyncResult, error) {
			started <- params
			return workspaceSyncResult{}, nil
		},
		workspaceSyncInterval: time.Hour,
	})
	defer app.shutdown(context.Background())

	config, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "dev"})
	requireWorkspaceSyncNoError(t, err, "load config")
	config.SSHD.WorkspaceSyncEnabled = true
	config.SSHD.WorkspaceSyncLocalPath = localRoot

	if _, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "dev"}, config); err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	stored := store.envs["frs/dev"]
	if !stored.SSHD.WorkspaceSync.Enabled || stored.SSHD.WorkspaceSync.LocalPath != localRoot {
		t.Fatalf("unexpected stored workspace sync config: %+v", stored.SSHD.WorkspaceSync)
	}
	select {
	case params := <-started:
		if params.LocalPath != localRoot {
			t.Fatalf("unexpected sync params: %+v", params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected saving enabled workspace sync to start sync")
	}
}

func TestSaveEnvironmentConfigRejectsEnabledWorkspaceSyncWithoutLocalPath(t *testing.T) {
	store := workspaceSyncStore(true)
	env := store.envs["frs/dev"]
	env.SSHD.WorkspaceSync.LocalPath = ""
	store.envs["frs/dev"] = env
	app := NewApp(erunUIDeps{store: store})
	defer app.shutdown(context.Background())

	config, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "dev"})
	requireWorkspaceSyncNoError(t, err, "load config")
	config.SSHD.WorkspaceSyncEnabled = true

	if _, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "dev"}, config); err == nil {
		t.Fatal("expected missing local sync folder error")
	}
}

func TestSaveEnvironmentConfigAllowsDisabledWorkspaceSyncWithoutLocalPath(t *testing.T) {
	store := workspaceSyncStore(true)
	env := store.envs["frs/dev"]
	env.SSHD.WorkspaceSync.Enabled = false
	env.SSHD.WorkspaceSync.LocalPath = ""
	store.envs["frs/dev"] = env
	app := NewApp(erunUIDeps{store: store})
	defer app.shutdown(context.Background())

	config, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "dev"})
	requireWorkspaceSyncNoError(t, err, "load config")

	if _, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "dev"}, config); err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
}

func TestLoadEnvironmentConfigTreatsEnabledWorkspaceSyncWithoutLocalPathAsOff(t *testing.T) {
	store := workspaceSyncStore(true)
	env := store.envs["frs/dev"]
	env.SSHD.WorkspaceSync.LocalPath = ""
	store.envs["frs/dev"] = env
	app := NewApp(erunUIDeps{store: store})
	defer app.shutdown(context.Background())

	config, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "dev"})
	requireWorkspaceSyncNoError(t, err, "load config")
	if config.SSHD.WorkspaceSyncEnabled {
		t.Fatalf("expected workspace sync to load as off without local path, got %+v", config.SSHD)
	}
}

func TestSafeWorkspaceSyncPathRejectsArtifactMirrorSubdir(t *testing.T) {
	for _, p := range []string{workspaceSyncArtifactsSubdir, workspaceSyncArtifactsSubdir + "/erun-app.exe"} {
		if safeWorkspaceSyncPath(p) {
			t.Errorf("expected source lane to reject artifact-mirror path %q", p)
		}
	}
	// A distinct sibling that merely shares the prefix stays in the source lane.
	if !safeWorkspaceSyncPath(".erun-outputs-notes/readme.md") {
		t.Error("expected a distinct sibling path to remain allowed")
	}
}

func TestListLocalArtifactFiles(t *testing.T) {
	root := t.TempDir()
	requireWorkspaceSyncNoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755), "mkdir sub")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, "erun-app.exe"), []byte("x"), 0o644), "write exe")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, "sub", "report.txt"), []byte("y"), 0o644), "write report")

	got, err := listLocalArtifactFiles(root)
	requireWorkspaceSyncNoError(t, err, "list artifacts")
	want := []string{"erun-app.exe", "sub/report.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected artifact files: got %+v want %+v", got, want)
	}
}

func TestListLocalArtifactFilesMissingRootIsEmpty(t *testing.T) {
	got, err := listLocalArtifactFiles(filepath.Join(t.TempDir(), "does-not-exist"))
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

	requireWorkspaceSyncNoError(t, makeArtifactsWritable(root), "make writable")
	info, err = os.Stat(artifact)
	requireWorkspaceSyncNoError(t, err, "stat after writable")
	if info.Mode().Perm()&0o200 == 0 {
		t.Fatalf("expected write bit restored before refresh, got mode %v", info.Mode().Perm())
	}
}

func TestEnsureLocalWorkspaceSyncTargetAcceptsPlainDirAndCreatesMissing(t *testing.T) {
	// An existing plain directory is accepted without any local git.
	existing := t.TempDir()
	requireWorkspaceSyncNoError(t, ensureLocalWorkspaceSyncTarget(existing), "accept plain dir")

	// A missing directory is created.
	missing := filepath.Join(t.TempDir(), "nested", "mirror")
	requireWorkspaceSyncNoError(t, ensureLocalWorkspaceSyncTarget(missing), "create missing dir")
	info, err := os.Stat(missing)
	requireWorkspaceSyncNoError(t, err, "stat created dir")
	if !info.IsDir() {
		t.Fatal("expected the created path to be a directory")
	}

	// A path that is a file, not a directory, is rejected.
	file := filepath.Join(t.TempDir(), "afile")
	requireWorkspaceSyncNoError(t, os.WriteFile(file, []byte("x"), 0o644), "write file")
	if err := ensureLocalWorkspaceSyncTarget(file); err == nil {
		t.Fatal("expected a non-directory path to be rejected")
	}
}

func TestLocalWorkspaceSourceFileMetaSkipsGitAndArtifacts(t *testing.T) {
	root := t.TempDir()
	requireWorkspaceSyncNoError(t, os.MkdirAll(filepath.Join(root, "app"), 0o755), "mkdir app")
	requireWorkspaceSyncNoError(t, os.MkdirAll(filepath.Join(root, ".git", "refs"), 0o755), "mkdir .git")
	requireWorkspaceSyncNoError(t, os.MkdirAll(filepath.Join(root, workspaceSyncArtifactsSubdir), 0o755), "mkdir outputs")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("readme"), 0o644), "write readme")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, "app", "main.go"), []byte("m"), 0o644), "write main")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, ".git", "config"), []byte("g"), 0o644), "write git config")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, ".git", "refs", "head"), []byte("h"), 0o644), "write git ref")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(root, workspaceSyncArtifactsSubdir, "erun-app.exe"), []byte("e"), 0o644), "write artifact")

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
		t.Fatal("expected .git path to be skipped by safeWorkspaceSyncPath")
	}
}

func TestStartWorkspaceSyncForConfiguredEnvsStartsEnabledEnvs(t *testing.T) {
	called := make(chan workspaceSyncParams, 1)
	app := NewApp(erunUIDeps{
		store:                 workspaceSyncStore(true),
		canConnectLocalPort:   func(int) bool { return true },
		workspaceSyncReady:    func(context.Context, string) error { return nil },
		workspaceSyncInterval: time.Hour,
		syncWorkspace: func(_ context.Context, params workspaceSyncParams) (workspaceSyncResult, error) {
			called <- params
			return workspaceSyncResult{FilesCopied: 1}, nil
		},
	})
	defer app.shutdown(context.Background())

	app.startWorkspaceSyncForConfiguredEnvs()

	select {
	case params := <-called:
		if params.HostAlias != "erun-frs-dev" || params.LocalPath != "/tmp/frs-local" {
			t.Fatalf("unexpected sync params: %+v", params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected startup to start workspace sync for a configured env without opening it")
	}
}

func TestStartWorkspaceSyncForConfiguredEnvsSkipsDisabledEnvs(t *testing.T) {
	called := make(chan workspaceSyncParams, 1)
	app := NewApp(erunUIDeps{
		store:                 workspaceSyncStore(false),
		canConnectLocalPort:   func(int) bool { return true },
		workspaceSyncReady:    func(context.Context, string) error { return nil },
		workspaceSyncInterval: time.Hour,
		syncWorkspace: func(_ context.Context, params workspaceSyncParams) (workspaceSyncResult, error) {
			called <- params
			return workspaceSyncResult{}, nil
		},
	})
	defer app.shutdown(context.Background())

	app.startWorkspaceSyncForConfiguredEnvs()

	select {
	case params := <-called:
		t.Fatalf("did not expect sync for a workspace-sync-disabled env, got %+v", params)
	case <-time.After(100 * time.Millisecond):
	}
}

func workspaceSyncStore(enabled bool) stubUIStore {
	return stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs", DefaultEnvironment: "dev"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/dev": {
				Name:              "dev",
				LocalRepoPath:     "/home/erun/git/frs",
				KubernetesContext: "cluster-dev",
				Type:              eruncommon.EnvironmentTypeRuntime,
				SSHD: eruncommon.SSHDConfig{
					Enabled: true,
					WorkspaceSync: eruncommon.SSHDWorkspaceSyncConfig{
						Enabled:   enabled,
						LocalPath: "/tmp/frs-local",
					},
				},
			},
		},
	}
}

func requireWorkspaceSyncNoError(t *testing.T, err error, context string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", context, err)
	}
}
