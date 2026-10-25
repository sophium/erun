package envops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func workspaceSyncStore(enabled bool) fakeStore {
	return fakeStore{
		tenants: map[string]model.TenantConfig{
			"frs": {Name: "frs", DefaultEnvironment: "dev"},
		},
		envs: map[string]config.EnvConfig{
			"frs/dev": {
				Name:              "dev",
				LocalRepoPath:     "/home/erun/git/frs",
				KubernetesContext: "cluster-dev",
				Type:              config.EnvironmentTypeRuntime,
				SSHD: config.SSHDConfig{
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

func TestStartWorkspaceSyncForSelectionRequiresRemoteSSHDWorkspaceSync(t *testing.T) {
	called := make(chan workspace.WorkspaceSyncParams, 1)
	service := newTestService(Deps{
		Store: workspaceSyncStore(true),
		CanConnectLocalPort: func(int) bool {
			return true
		},
		CanReachMCPEndpoint: func(int) bool {
			return true
		},
		WorkspaceSyncReady: func(context.Context, string) error {
			return nil
		},
		SyncWorkspace: func(_ context.Context, params workspace.WorkspaceSyncParams) (model.WorkspaceSyncResult, error) {
			called <- params
			return model.WorkspaceSyncResult{FilesCopied: 2}, nil
		},
		WorkspaceSyncInterval: time.Hour,
	})
	t.Cleanup(service.StopAllWorkspaceSyncs)

	service.StartWorkspaceSync(uimodel.Selection{Tenant: "frs", Environment: "dev"})

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
	called := make(chan workspace.WorkspaceSyncParams, 1)
	service := newTestService(Deps{
		Store: workspaceSyncStore(false),
		CanConnectLocalPort: func(int) bool {
			return true
		},
		CanReachMCPEndpoint: func(int) bool {
			return true
		},
		WorkspaceSyncReady: func(context.Context, string) error {
			return nil
		},
		SyncWorkspace: func(_ context.Context, params workspace.WorkspaceSyncParams) (model.WorkspaceSyncResult, error) {
			called <- params
			return model.WorkspaceSyncResult{}, nil
		},
		WorkspaceSyncInterval: time.Hour,
	})
	t.Cleanup(service.StopAllWorkspaceSyncs)

	service.StartWorkspaceSync(uimodel.Selection{Tenant: "frs", Environment: "dev"})

	select {
	case params := <-called:
		t.Fatalf("did not expect workspace sync to start, got %+v", params)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStartWorkspaceSyncForConfiguredEnvsStartsEnabledEnvs(t *testing.T) {
	called := make(chan workspace.WorkspaceSyncParams, 1)
	service := newTestService(Deps{
		Store:                 workspaceSyncStore(true),
		CanConnectLocalPort:   func(int) bool { return true },
		CanReachMCPEndpoint:   func(int) bool { return true },
		WorkspaceSyncReady:    func(context.Context, string) error { return nil },
		WorkspaceSyncInterval: time.Hour,
		SyncWorkspace: func(_ context.Context, params workspace.WorkspaceSyncParams) (model.WorkspaceSyncResult, error) {
			called <- params
			return model.WorkspaceSyncResult{FilesCopied: 1}, nil
		},
	})
	t.Cleanup(service.StopAllWorkspaceSyncs)

	service.ReconcileConfiguredWorkspaceSyncs()

	select {
	case params := <-called:
		if params.HostAlias != "erun-frs-dev" || params.LocalPath != "/tmp/frs-local" {
			t.Fatalf("unexpected sync params: %+v", params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected startup to start workspace sync for a configured env without opening it")
	}
}

func TestReconcileWorkspaceSyncForConfiguredEnvsStopsAnUnlinkedEnv(t *testing.T) {
	store := workspaceSyncStore(true)
	passed := make(chan struct{}, 8)
	service := newTestService(Deps{
		Store:                 store,
		CanConnectLocalPort:   func(int) bool { return true },
		CanReachMCPEndpoint:   func(int) bool { return true },
		WorkspaceSyncReady:    func(context.Context, string) error { return nil },
		WorkspaceSyncInterval: time.Millisecond,
		SyncWorkspace: func(context.Context, workspace.WorkspaceSyncParams) (model.WorkspaceSyncResult, error) {
			select {
			case passed <- struct{}{}:
			default:
			}
			return model.WorkspaceSyncResult{}, nil
		},
	})
	t.Cleanup(service.StopAllWorkspaceSyncs)

	service.ReconcileConfiguredWorkspaceSyncs()
	select {
	case <-passed:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the linked env to start syncing")
	}

	env := store.envs["frs/dev"]
	env.SSHD.WorkspaceSync.Enabled = false
	store.envs["frs/dev"] = env

	service.ReconcileConfiguredWorkspaceSyncs()

	if keys := service.runningWorkspaceSyncKeys(); len(keys) != 0 {
		t.Fatalf("expected the worker to stop once its env was unlinked, still running: %v", keys)
	}
}

func TestStartWorkspaceSyncForConfiguredEnvsSkipsDisabledEnvs(t *testing.T) {
	called := make(chan workspace.WorkspaceSyncParams, 1)
	service := newTestService(Deps{
		Store:                 workspaceSyncStore(false),
		CanConnectLocalPort:   func(int) bool { return true },
		CanReachMCPEndpoint:   func(int) bool { return true },
		WorkspaceSyncReady:    func(context.Context, string) error { return nil },
		WorkspaceSyncInterval: time.Hour,
		SyncWorkspace: func(_ context.Context, params workspace.WorkspaceSyncParams) (model.WorkspaceSyncResult, error) {
			called <- params
			return model.WorkspaceSyncResult{}, nil
		},
	})
	t.Cleanup(service.StopAllWorkspaceSyncs)

	service.ReconcileConfiguredWorkspaceSyncs()

	select {
	case params := <-called:
		t.Fatalf("did not expect sync for a workspace-sync-disabled env, got %+v", params)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStartWorkspaceSyncForSelectionEmitsWarningWithoutLocalPath(t *testing.T) {
	store := workspaceSyncStore(true)
	env := store.envs["frs/dev"]
	env.SSHD.WorkspaceSync.LocalPath = ""
	store.envs["frs/dev"] = env
	emits := newCapturedEmits()
	service := newTestService(Deps{
		Store:               store,
		CanConnectLocalPort: func(int) bool { return true },
		CanReachMCPEndpoint: func(int) bool { return true },
		// Otherwise the real cwd resolves a project root and masks the case.
		FindProjectRoot: func() (string, string, error) { return "", "", errors.New("not found") },
		Emit:            emits.fn(),
	})
	t.Cleanup(service.StopAllWorkspaceSyncs)

	service.StartWorkspaceSync(uimodel.Selection{Tenant: "frs", Environment: "dev"})

	if got := emits.events(uimodel.AppStatusEvent); len(got) != 0 {
		t.Fatalf("expected no unclassified app-status emit, got %+v", got)
	}
	events := emits.events(uimodel.AppNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected one classified notification, got %+v", events)
	}
	payload, ok := events[0].(uimodel.AppNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if payload.Kind != "warning" {
		t.Fatalf("kind = %q, want warning", payload.Kind)
	}
	if payload.Tenant != "frs" || payload.Environment != "dev" {
		t.Fatalf("unexpected identity: tenant=%q environment=%q", payload.Tenant, payload.Environment)
	}
	if payload.Source != sourceWorkspaceSyncNoPath {
		t.Fatalf("source = %q, want %q", payload.Source, sourceWorkspaceSyncNoPath)
	}
}

func TestSetWorkspaceSyncErrorEmitsClassifiedError(t *testing.T) {
	emits := newCapturedEmits()
	service := newTestService(Deps{Emit: emits.fn()})
	service.workspaceSyncs["frs/dev"] = &workspaceSyncWorker{status: WorkspaceSyncStatus{Status: "syncing"}}

	service.setWorkspaceSyncError("frs/dev", "frs", "dev", errors.New("connection refused"))

	events := emits.events(uimodel.AppNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected one classified notification, got %+v", events)
	}
	payload, ok := events[0].(uimodel.AppNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if payload.Kind != "error" {
		t.Fatalf("kind = %q, want error", payload.Kind)
	}
	if payload.Tenant != "frs" || payload.Environment != "dev" {
		t.Fatalf("unexpected identity: tenant=%q environment=%q", payload.Tenant, payload.Environment)
	}
	if payload.Source != sourceWorkspaceSyncFailed {
		t.Fatalf("source = %q, want %q", payload.Source, sourceWorkspaceSyncFailed)
	}
}
