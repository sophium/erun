package main

import (
	"context"
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestStartSessionStartsConfiguredWorkspaceSync(t *testing.T) {
	called := make(chan workspace.WorkspaceSyncParams, 1)
	app := NewApp(erunUIDeps{
		store:          workspaceSyncStore(true),
		resolveCLIPath: func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		canConnectLocalPort: func(int) bool {
			return true
		},
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		workspaceSyncReady: func(context.Context, string) error {
			return nil
		},
		syncWorkspace: func(_ context.Context, params workspace.WorkspaceSyncParams) (model.WorkspaceSyncResult, error) {
			called <- params
			return model.WorkspaceSyncResult{FilesCopied: 2}, nil
		},
		workspaceSyncInterval: time.Hour,
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartSession(uimodel.Selection{Tenant: "frs", Environment: "dev"}, 0, 80, 24); err != nil {
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
	// A mirror needs no local git.
	localRoot := t.TempDir()
	store := workspaceSyncStore(true)
	env := store.envs["frs/dev"]
	env.SSHD.WorkspaceSync.LocalPath = ""
	store.envs["frs/dev"] = env
	started := make(chan workspace.WorkspaceSyncParams, 1)
	app := NewApp(erunUIDeps{
		store: store,
		canConnectLocalPort: func(int) bool {
			return true
		},
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		workspaceSyncReady: func(context.Context, string) error {
			return nil
		},
		syncWorkspace: func(_ context.Context, params workspace.WorkspaceSyncParams) (model.WorkspaceSyncResult, error) {
			started <- params
			return model.WorkspaceSyncResult{}, nil
		},
		workspaceSyncInterval: time.Hour,
	})
	defer app.shutdown(context.Background())

	config, err := app.LoadEnvironmentConfig(uimodel.Selection{Tenant: "frs", Environment: "dev"})
	requireWorkspaceSyncNoError(t, err, "load config")
	config.SSHD.WorkspaceSyncEnabled = true
	config.SSHD.WorkspaceSyncLocalPath = localRoot

	if _, err := app.SaveEnvironmentConfig(uimodel.Selection{Tenant: "frs", Environment: "dev"}, config); err != nil {
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

	config, err := app.LoadEnvironmentConfig(uimodel.Selection{Tenant: "frs", Environment: "dev"})
	requireWorkspaceSyncNoError(t, err, "load config")
	config.SSHD.WorkspaceSyncEnabled = true

	if _, err := app.SaveEnvironmentConfig(uimodel.Selection{Tenant: "frs", Environment: "dev"}, config); err == nil {
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

	config, err := app.LoadEnvironmentConfig(uimodel.Selection{Tenant: "frs", Environment: "dev"})
	requireWorkspaceSyncNoError(t, err, "load config")

	if _, err := app.SaveEnvironmentConfig(uimodel.Selection{Tenant: "frs", Environment: "dev"}, config); err != nil {
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

	config, err := app.LoadEnvironmentConfig(uimodel.Selection{Tenant: "frs", Environment: "dev"})
	requireWorkspaceSyncNoError(t, err, "load config")
	if config.SSHD.WorkspaceSyncEnabled {
		t.Fatalf("expected workspace sync to load as off without local path, got %+v", config.SSHD)
	}
}

func workspaceSyncStore(enabled bool) stubUIStore {
	return stubUIStore{
		tenants: map[string]model.TenantConfig{
			"frs": {Name: "frs", DefaultEnvironment: "dev"},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/dev": {
				Name:              "dev",
				LocalRepoPath:     "/home/erun/git/frs",
				KubernetesContext: "cluster-dev",
				Type:              configpkg.EnvironmentTypeRuntime,
				SSHD: configpkg.SSHDConfig{
					Enabled: true,
					WorkspaceSync: model.SSHDWorkspaceSyncConfig{
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
