package main

import (
	"context"
	"errors"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

func TestStartWorkspaceSyncForSelectionRequiresRemoteSSHDWorkspaceSync(t *testing.T) {
	called := make(chan eruncommon.WorkspaceSyncParams, 1)
	app := NewApp(erunUIDeps{
		store: workspaceSyncStore(true),
		canConnectLocalPort: func(int) bool {
			return true
		},
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		workspaceSyncReady: func(context.Context, string) error {
			return nil
		},
		syncWorkspace: func(_ context.Context, params eruncommon.WorkspaceSyncParams) (eruncommon.WorkspaceSyncResult, error) {
			called <- params
			return eruncommon.WorkspaceSyncResult{FilesCopied: 2}, nil
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
	called := make(chan eruncommon.WorkspaceSyncParams, 1)
	app := NewApp(erunUIDeps{
		store: workspaceSyncStore(false),
		canConnectLocalPort: func(int) bool {
			return true
		},
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		workspaceSyncReady: func(context.Context, string) error {
			return nil
		},
		syncWorkspace: func(_ context.Context, params eruncommon.WorkspaceSyncParams) (eruncommon.WorkspaceSyncResult, error) {
			called <- params
			return eruncommon.WorkspaceSyncResult{}, nil
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
	called := make(chan eruncommon.WorkspaceSyncParams, 1)
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
		syncWorkspace: func(_ context.Context, params eruncommon.WorkspaceSyncParams) (eruncommon.WorkspaceSyncResult, error) {
			called <- params
			return eruncommon.WorkspaceSyncResult{FilesCopied: 2}, nil
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
	started := make(chan eruncommon.WorkspaceSyncParams, 1)
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
		syncWorkspace: func(_ context.Context, params eruncommon.WorkspaceSyncParams) (eruncommon.WorkspaceSyncResult, error) {
			started <- params
			return eruncommon.WorkspaceSyncResult{}, nil
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

func TestStartWorkspaceSyncForConfiguredEnvsStartsEnabledEnvs(t *testing.T) {
	called := make(chan eruncommon.WorkspaceSyncParams, 1)
	app := NewApp(erunUIDeps{
		store:                 workspaceSyncStore(true),
		canConnectLocalPort:   func(int) bool { return true },
		canReachMCPEndpoint:   func(int) bool { return true },
		workspaceSyncReady:    func(context.Context, string) error { return nil },
		workspaceSyncInterval: time.Hour,
		syncWorkspace: func(_ context.Context, params eruncommon.WorkspaceSyncParams) (eruncommon.WorkspaceSyncResult, error) {
			called <- params
			return eruncommon.WorkspaceSyncResult{FilesCopied: 1}, nil
		},
	})
	defer app.shutdown(context.Background())

	app.reconcileWorkspaceSyncForConfiguredEnvs()

	select {
	case params := <-called:
		if params.HostAlias != "erun-frs-dev" || params.LocalPath != "/tmp/frs-local" {
			t.Fatalf("unexpected sync params: %+v", params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected startup to start workspace sync for a configured env without opening it")
	}
}

// Detaching a mirror is done the same way linking is — by editing config while
// the app runs — so a worker whose env is no longer linked has to stop. Left
// running it keeps writing into a directory the operator has stopped treating as
// a mirror.
func TestReconcileWorkspaceSyncForConfiguredEnvsStopsAnUnlinkedEnv(t *testing.T) {
	store := workspaceSyncStore(true)
	passed := make(chan struct{}, 8)
	app := NewApp(erunUIDeps{
		store:                 store,
		canConnectLocalPort:   func(int) bool { return true },
		canReachMCPEndpoint:   func(int) bool { return true },
		workspaceSyncReady:    func(context.Context, string) error { return nil },
		workspaceSyncInterval: time.Millisecond,
		syncWorkspace: func(context.Context, eruncommon.WorkspaceSyncParams) (eruncommon.WorkspaceSyncResult, error) {
			select {
			case passed <- struct{}{}:
			default:
			}
			return eruncommon.WorkspaceSyncResult{}, nil
		},
	})
	defer app.shutdown(context.Background())

	app.reconcileWorkspaceSyncForConfiguredEnvs()
	select {
	case <-passed:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the linked env to start syncing")
	}

	// The operator unlinks it outside the app, which is where links are made.
	env := store.envs["frs/dev"]
	env.SSHD.WorkspaceSync.Enabled = false
	store.envs["frs/dev"] = env

	app.reconcileWorkspaceSyncForConfiguredEnvs()

	if keys := app.runningWorkspaceSyncKeys(); len(keys) != 0 {
		t.Fatalf("expected the worker to stop once its env was unlinked, still running: %v", keys)
	}
}

func TestStartWorkspaceSyncForConfiguredEnvsSkipsDisabledEnvs(t *testing.T) {
	called := make(chan eruncommon.WorkspaceSyncParams, 1)
	app := NewApp(erunUIDeps{
		store:                 workspaceSyncStore(false),
		canConnectLocalPort:   func(int) bool { return true },
		canReachMCPEndpoint:   func(int) bool { return true },
		workspaceSyncReady:    func(context.Context, string) error { return nil },
		workspaceSyncInterval: time.Hour,
		syncWorkspace: func(_ context.Context, params eruncommon.WorkspaceSyncParams) (eruncommon.WorkspaceSyncResult, error) {
			called <- params
			return eruncommon.WorkspaceSyncResult{}, nil
		},
	})
	defer app.shutdown(context.Background())

	app.reconcileWorkspaceSyncForConfiguredEnvs()

	select {
	case params := <-called:
		t.Fatalf("did not expect sync for a workspace-sync-disabled env, got %+v", params)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestStartWorkspaceSyncForSelectionEmitsWarningWithoutLocalPath locks the
// reclassification of the "workspace sync has no local path" notice: it used
// to go out on the unclassified emitAppStatus channel, and now goes out as a
// classified, env-tagged warning.
func TestStartWorkspaceSyncForSelectionEmitsWarningWithoutLocalPath(t *testing.T) {
	store := workspaceSyncStore(true)
	env := store.envs["frs/dev"]
	env.SSHD.WorkspaceSync.LocalPath = ""
	store.envs["frs/dev"] = env
	emits := newCapturedEmits()
	app := NewApp(erunUIDeps{
		store:               store,
		canConnectLocalPort: func(int) bool { return true },
		canReachMCPEndpoint: func(int) bool { return true },
		// Without this, the default findProjectRoot walks the real
		// filesystem from the test binary's cwd and resolves a real project
		// root, masking the "no local path" case this test exercises.
		findProjectRoot: func() (string, string, error) { return "", "", errors.New("not found") },
	})
	app.emitFn = emits.fn()
	defer app.shutdown(context.Background())

	app.startWorkspaceSyncForSelection(uiSelection{Tenant: "frs", Environment: "dev"})

	if got := emits.events(appStatusEvent); len(got) != 0 {
		t.Fatalf("expected no unclassified app-status emit, got %+v", got)
	}
	events := emits.events(appNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected one classified notification, got %+v", events)
	}
	payload, ok := events[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if payload.Kind != "warning" {
		t.Fatalf("kind = %q, want warning", payload.Kind)
	}
	if payload.Tenant != "frs" || payload.Environment != "dev" {
		t.Fatalf("unexpected identity: tenant=%q environment=%q", payload.Tenant, payload.Environment)
	}
	if payload.Source != notificationSourceWorkspaceSyncNoPath {
		t.Fatalf("source = %q, want %q", payload.Source, notificationSourceWorkspaceSyncNoPath)
	}
}

// TestSetWorkspaceSyncErrorEmitsClassifiedError locks the reclassification of
// a workspace sync failure from the unclassified emitAppStatus channel to a
// classified, env-tagged error.
func TestSetWorkspaceSyncErrorEmitsClassifiedError(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{emitFn: emits.fn(), workspaceSyncs: map[string]*workspaceSyncWorker{
		"frs/dev": {status: workspaceSyncStatus{Status: "syncing"}},
	}}

	app.setWorkspaceSyncError("frs/dev", "frs", "dev", errors.New("connection refused"))

	events := emits.events(appNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected one classified notification, got %+v", events)
	}
	payload, ok := events[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if payload.Kind != "error" {
		t.Fatalf("kind = %q, want error", payload.Kind)
	}
	if payload.Tenant != "frs" || payload.Environment != "dev" {
		t.Fatalf("unexpected identity: tenant=%q environment=%q", payload.Tenant, payload.Environment)
	}
	if payload.Source != notificationSourceWorkspaceSyncFailed {
		t.Fatalf("source = %q, want %q", payload.Source, notificationSourceWorkspaceSyncFailed)
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
