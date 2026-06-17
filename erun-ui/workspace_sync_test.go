package main

import (
	"context"
	"os"
	"os/exec"
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
	localRoot := t.TempDir()
	requireWorkspaceSyncNoError(t, exec.Command("git", "-C", localRoot, "init").Run(), "init local git repo")
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
