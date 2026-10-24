package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/primitive/open"
)

const defaultWorkspaceSyncInterval = 2 * time.Second

type workspaceSyncWorker struct {
	cancel           context.CancelFunc
	status           workspaceSyncStatus
	lastErrorMessage string
}

type workspaceSyncStatus struct {
	Status     string
	Message    string
	LastSynced time.Time
	Files      int
}

func (a *App) startWorkspaceSyncForSelection(selection uiSelection) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return
	}
	result, err := open.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil || !result.RemoteRepo() || !result.EnvConfig.SSHD.Enabled || !result.EnvConfig.SSHD.WorkspaceSync.Enabled {
		return
	}
	localPath := workspace.WorkspaceSyncLocalPath(result, a.deps.findProjectRoot)
	if localPath == "" {
		a.emitEnvNotification("warning", selection.Tenant, selection.Environment, notificationSourceWorkspaceSyncNoPath,
			fmt.Sprintf("Workspace sync for %s/%s has no local path.", selection.Tenant, selection.Environment), "")
		return
	}
	key := selectionKey(selection)

	a.mu.Lock()
	if existing := a.workspaceSyncs[key]; existing != nil {
		a.mu.Unlock()
		return
	}
	ctx := context.Background()
	if a.ctx != nil {
		ctx = a.ctx
	}
	ctx, cancel := context.WithCancel(ctx)
	a.workspaceSyncs[key] = &workspaceSyncWorker{
		cancel: cancel,
		status: workspaceSyncStatus{Status: "starting", Message: "Starting workspace sync"},
	}
	a.mu.Unlock()

	a.emitEnvNotification("info", selection.Tenant, selection.Environment, "",
		fmt.Sprintf("Starting workspace sync for %s/%s...", selection.Tenant, selection.Environment), "")
	go a.runWorkspaceSyncLoop(ctx, key, selection, result, localPath)
}

// reconcileWorkspaceSyncForConfiguredEnvs runs on the heartbeat because envs
// are linked and unlinked while the app runs.
func (a *App) reconcileWorkspaceSyncForConfiguredEnvs() {
	if a.deps.store == nil {
		return
	}
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return
	}
	desired, complete := a.desiredWorkspaceSyncSelections(tenants)
	for _, selection := range desired {
		a.startWorkspaceSyncForSelection(selection)
	}
	// Stop only on a complete picture, or healthy workers get torn down.
	if !complete {
		return
	}
	for _, key := range a.runningWorkspaceSyncKeys() {
		if _, keep := desired[key]; !keep {
			a.stopWorkspaceSyncForKey(key)
		}
	}
}

// desiredWorkspaceSyncSelections reports completeness: an unreadable tenant
// must not read as "not wanted".
func (a *App) desiredWorkspaceSyncSelections(tenants []model.TenantConfig) (map[string]uiSelection, bool) {
	desired := map[string]uiSelection{}
	complete := true
	for _, tenant := range tenants {
		envs, err := a.deps.store.ListEnvConfigs(tenant.Name)
		if err != nil {
			complete = false
			continue
		}
		for _, env := range envs {
			// Cheap pre-filter; startWorkspaceSyncForSelection re-validates.
			if !env.SSHD.Enabled || !env.SSHD.WorkspaceSync.Enabled {
				continue
			}
			selection := uiSelection{Tenant: tenant.Name, Environment: env.Name}
			desired[selectionKey(selection)] = selection
		}
	}
	return desired, complete
}

// runningWorkspaceSyncKeys snapshots so stops run without holding the lock.
func (a *App) runningWorkspaceSyncKeys() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	keys := make([]string, 0, len(a.workspaceSyncs))
	for key := range a.workspaceSyncs {
		keys = append(keys, key)
	}
	return keys
}

func (a *App) reconcileWorkspaceSyncForSelection(selection uiSelection, enabled bool) {
	a.stopWorkspaceSyncForSelection(selection)
	if enabled {
		a.startWorkspaceSyncForSelection(selection)
	}
}

func (a *App) stopWorkspaceSyncForSelection(selection uiSelection) {
	a.stopWorkspaceSyncForKey(selectionKey(selection))
}

func (a *App) stopWorkspaceSyncForKey(key string) {
	a.mu.Lock()
	worker := a.workspaceSyncs[key]
	if worker != nil {
		delete(a.workspaceSyncs, key)
	}
	a.mu.Unlock()
	if worker != nil && worker.cancel != nil {
		worker.cancel()
	}
}

func (a *App) runWorkspaceSyncLoop(ctx context.Context, key string, selection uiSelection, result config.OpenResult, localPath string) {
	params := workspace.WorkspaceSyncParams{
		HostAlias:  open.SSHConnectionInfoForResult(result).HostAlias,
		RemotePath: open.SSHConnectionInfoForResult(result).WorkspacePath,
		LocalPath:  localPath,
	}
	for {
		a.setWorkspaceSyncStatus(key, workspaceSyncStatus{Status: "syncing", Message: "Syncing workspace"})
		switch a.prepareWorkspaceSyncPass(ctx, key, result, params) {
		case workspaceSyncPassStop:
			return
		case workspaceSyncPassRetry:
			continue
		case workspaceSyncPassProceed:
		}
		synced, err := a.deps.syncWorkspace(ctx, params)
		if err != nil {
			a.setWorkspaceSyncError(key, result.Tenant, result.EnvConfig.Name, err)
		} else {
			message := fmt.Sprintf("Synced %d files, deleted %d", synced.FilesCopied, synced.FilesDeleted)
			if synced.ArtifactsCopied > 0 {
				message += fmt.Sprintf(", %d artifacts", synced.ArtifactsCopied)
			}
			a.setWorkspaceSyncStatus(key, workspaceSyncStatus{
				Status:     "synced",
				Message:    message,
				LastSynced: time.Now(),
				Files:      synced.FilesCopied,
			})
		}
		if !sleepWorkspaceSyncInterval(ctx, a.deps.workspaceSyncInterval) {
			return
		}
	}
}

type workspaceSyncPassOutcome int

const (
	workspaceSyncPassProceed workspaceSyncPassOutcome = iota
	workspaceSyncPassRetry
	workspaceSyncPassStop
)

// prepareWorkspaceSyncPass sleeps before signalling retry so the loop does not
// busy-spin on an unreachable SSHD.
func (a *App) prepareWorkspaceSyncPass(ctx context.Context, key string, result config.OpenResult, params workspace.WorkspaceSyncParams) workspaceSyncPassOutcome {
	if a.deps.canConnectLocalPort != nil && !a.deps.canConnectLocalPort(config.SSHLocalPortForResult(result)) && a.deps.ensureSSHD != nil {
		if err := a.deps.ensureSSHD(ctx, result); err != nil {
			a.setWorkspaceSyncError(key, result.Tenant, result.EnvConfig.Name, err)
			if !sleepWorkspaceSyncInterval(ctx, a.deps.workspaceSyncInterval) {
				return workspaceSyncPassStop
			}
			return workspaceSyncPassRetry
		}
	}
	if a.deps.workspaceSyncReady != nil {
		if err := a.deps.workspaceSyncReady(ctx, params.HostAlias); err != nil {
			a.setWorkspaceSyncStatus(key, workspaceSyncStatus{Status: "starting", Message: "Waiting for SSHD"})
			if !sleepWorkspaceSyncInterval(ctx, a.deps.workspaceSyncInterval) {
				return workspaceSyncPassStop
			}
			return workspaceSyncPassRetry
		}
	}
	return workspaceSyncPassProceed
}

func sleepWorkspaceSyncInterval(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (a *App) setWorkspaceSyncError(key, tenant, environment string, err error) {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "unknown error"
	}
	shouldEmit := false
	a.mu.Lock()
	if worker := a.workspaceSyncs[key]; worker != nil {
		shouldEmit = worker.lastErrorMessage != message
		worker.lastErrorMessage = message
		worker.status = workspaceSyncStatus{Status: "error", Message: message}
	}
	a.mu.Unlock()
	if shouldEmit {
		a.emitEnvNotification("error", tenant, environment, notificationSourceWorkspaceSyncFailed,
			"Workspace sync failed: "+message, "")
	}
}

func (a *App) setWorkspaceSyncStatus(key string, status workspaceSyncStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if worker := a.workspaceSyncs[key]; worker != nil {
		worker.status = status
		if status.Status == "synced" {
			worker.lastErrorMessage = ""
		}
	}
}

func (a *App) workspaceSyncStatus(selection uiSelection) workspaceSyncStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	if worker := a.workspaceSyncs[selectionKey(selection)]; worker != nil {
		return worker.status
	}
	return workspaceSyncStatus{Status: "stopped"}
}

func (a *App) stopAllWorkspaceSyncsLocked() {
	for key, worker := range a.workspaceSyncs {
		if worker != nil && worker.cancel != nil {
			worker.cancel()
		}
		delete(a.workspaceSyncs, key)
	}
}
