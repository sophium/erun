package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
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
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil || !result.RemoteRepo() || !result.EnvConfig.SSHD.Enabled || !result.EnvConfig.SSHD.WorkspaceSync.Enabled {
		return
	}
	localPath := eruncommon.WorkspaceSyncLocalPath(result, a.deps.findProjectRoot)
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

// reconcileWorkspaceSyncForConfiguredEnvs settles the running sync workers onto
// what the config asks for right now, in both directions. It runs on the
// heartbeat rather than only at boot because linking is done from the running
// app: an env enabled afterwards never got a worker and its mirror stayed
// silently empty, and an env disabled afterwards kept its worker and went on
// writing into a mirror the operator had already detached.
// startWorkspaceSyncForSelection re-validates each env and dedups an
// already-running poller, so this settles to a no-op once the two agree.
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
	// Stopping is only safe on a complete picture: a partial read would tear
	// down workers for envs that are still perfectly well configured.
	if !complete {
		return
	}
	for _, key := range a.runningWorkspaceSyncKeys() {
		if _, keep := desired[key]; !keep {
			a.stopWorkspaceSyncForKey(key)
		}
	}
}

// desiredWorkspaceSyncSelections reports the envs that should be mirroring right
// now, and whether that picture is complete. Incomplete matters: a tenant that
// could not be read says nothing about its envs, and reading silence as "not
// wanted" would tear down healthy workers.
func (a *App) desiredWorkspaceSyncSelections(tenants []eruncommon.TenantConfig) (map[string]uiSelection, bool) {
	desired := map[string]uiSelection{}
	complete := true
	for _, tenant := range tenants {
		envs, err := a.deps.store.ListEnvConfigs(tenant.Name)
		if err != nil {
			complete = false
			continue
		}
		for _, env := range envs {
			// Cheap pre-filter; startWorkspaceSyncForSelection re-validates and
			// gates on RemoteRepo(), so a non-remote env here is a no-op.
			if !env.SSHD.Enabled || !env.SSHD.WorkspaceSync.Enabled {
				continue
			}
			selection := uiSelection{Tenant: tenant.Name, Environment: env.Name}
			desired[selectionKey(selection)] = selection
		}
	}
	return desired, complete
}

// runningWorkspaceSyncKeys snapshots the workers currently polling, so the
// reconcile can compare against the desired set without holding the lock across
// the stops it decides on.
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

func (a *App) runWorkspaceSyncLoop(ctx context.Context, key string, selection uiSelection, result eruncommon.OpenResult, localPath string) {
	params := eruncommon.WorkspaceSyncParams{
		HostAlias:  eruncommon.SSHConnectionInfoForResult(result).HostAlias,
		RemotePath: eruncommon.SSHConnectionInfoForResult(result).WorkspacePath,
		LocalPath:  localPath,
	}
	for {
		a.setWorkspaceSyncStatus(key, workspaceSyncStatus{Status: "syncing", Message: "Syncing workspace"})
		switch a.prepareWorkspaceSyncPass(ctx, key, result, params) {
		case workspaceSyncPassStop:
			return
		case workspaceSyncPassRetry:
			continue
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

// prepareWorkspaceSyncPass sleeps one interval itself before signalling retry, so
// the sync loop's continue does not busy-spin on an unreachable SSHD.
func (a *App) prepareWorkspaceSyncPass(ctx context.Context, key string, result eruncommon.OpenResult, params eruncommon.WorkspaceSyncParams) workspaceSyncPassOutcome {
	if a.deps.canConnectLocalPort != nil && !a.deps.canConnectLocalPort(eruncommon.SSHLocalPortForResult(result)) && a.deps.ensureSSHD != nil {
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
