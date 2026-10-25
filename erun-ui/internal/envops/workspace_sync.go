package envops

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-common/primitive/open/sshconn"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// DefaultWorkspaceSyncInterval is the pause between workspace sync passes.
const DefaultWorkspaceSyncInterval = 2 * time.Second

type workspaceSyncWorker struct {
	cancel           context.CancelFunc
	status           WorkspaceSyncStatus
	lastErrorMessage string
}

type WorkspaceSyncStatus struct {
	Status     string
	Message    string
	LastSynced time.Time
	Files      int
}

// StartWorkspaceSync starts the environment's sync worker when its config asks
// for one and none is running.
func (s *Service) StartWorkspaceSync(selection uimodel.Selection) {
	selection = uimodel.NormalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return
	}
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil || !result.RemoteRepo() || !result.EnvConfig.SSHD.Enabled || !result.EnvConfig.SSHD.WorkspaceSync.Enabled {
		return
	}
	localPath := workspace.WorkspaceSyncLocalPath(result, s.d.FindProjectRoot)
	if localPath == "" {
		s.notifyEnv("warning", selection.Tenant, selection.Environment, sourceWorkspaceSyncNoPath,
			fmt.Sprintf("Workspace sync for %s/%s has no local path.", selection.Tenant, selection.Environment), "")
		return
	}
	key := uimodel.SelectionKey(selection)

	// Read the context before the lock: the lock is a leaf.
	parent := s.d.Context()
	s.mu.Lock()
	if existing := s.workspaceSyncs[key]; existing != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.workspaceSyncs[key] = &workspaceSyncWorker{
		cancel: cancel,
		status: WorkspaceSyncStatus{Status: "starting", Message: "Starting workspace sync"},
	}
	s.mu.Unlock()

	s.notifyEnv("info", selection.Tenant, selection.Environment, "",
		fmt.Sprintf("Starting workspace sync for %s/%s...", selection.Tenant, selection.Environment), "")
	go s.runWorkspaceSyncLoop(ctx, key, selection, result, localPath)
}

// ReconcileConfiguredWorkspaceSyncs runs on the heartbeat because envs are
// linked and unlinked while the app runs.
func (s *Service) ReconcileConfiguredWorkspaceSyncs() {
	tenants, err := s.d.Store.ListTenantConfigs()
	if err != nil {
		return
	}
	desired, complete := s.desiredWorkspaceSyncSelections(tenants)
	for _, selection := range desired {
		s.StartWorkspaceSync(selection)
	}
	// Stop only on a complete picture, or healthy workers get torn down.
	if !complete {
		return
	}
	for _, key := range s.runningWorkspaceSyncKeys() {
		if _, keep := desired[key]; !keep {
			s.stopWorkspaceSyncForKey(key)
		}
	}
}

// desiredWorkspaceSyncSelections reports completeness: an unreadable tenant
// must not read as "not wanted".
func (s *Service) desiredWorkspaceSyncSelections(tenants []model.TenantConfig) (map[string]uimodel.Selection, bool) {
	desired := map[string]uimodel.Selection{}
	complete := true
	for _, tenant := range tenants {
		envs, err := s.d.Store.ListEnvConfigs(tenant.Name)
		if err != nil {
			complete = false
			continue
		}
		for _, env := range envs {
			// Cheap pre-filter; StartWorkspaceSync re-validates.
			if !env.SSHD.Enabled || !env.SSHD.WorkspaceSync.Enabled {
				continue
			}
			selection := uimodel.Selection{Tenant: tenant.Name, Environment: env.Name}
			desired[uimodel.SelectionKey(selection)] = selection
		}
	}
	return desired, complete
}

// runningWorkspaceSyncKeys snapshots so stops run without holding the lock.
func (s *Service) runningWorkspaceSyncKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.workspaceSyncs))
	for key := range s.workspaceSyncs {
		keys = append(keys, key)
	}
	return keys
}

// ReconcileWorkspaceSync restarts the environment's worker on a config change.
func (s *Service) ReconcileWorkspaceSync(selection uimodel.Selection, enabled bool) {
	s.StopWorkspaceSync(selection)
	if enabled {
		s.StartWorkspaceSync(selection)
	}
}

func (s *Service) StopWorkspaceSync(selection uimodel.Selection) {
	s.stopWorkspaceSyncForKey(uimodel.SelectionKey(selection))
}

func (s *Service) stopWorkspaceSyncForKey(key string) {
	s.mu.Lock()
	worker := s.workspaceSyncs[key]
	if worker != nil {
		delete(s.workspaceSyncs, key)
	}
	s.mu.Unlock()
	if worker != nil && worker.cancel != nil {
		worker.cancel()
	}
}

func (s *Service) runWorkspaceSyncLoop(ctx context.Context, key string, selection uimodel.Selection, result config.OpenResult, localPath string) {
	params := workspace.WorkspaceSyncParams{
		HostAlias:  sshconn.SSHConnectionInfoForResult(result).HostAlias,
		RemotePath: sshconn.SSHConnectionInfoForResult(result).WorkspacePath,
		LocalPath:  localPath,
	}
	for {
		s.setWorkspaceSyncStatus(key, WorkspaceSyncStatus{Status: "syncing", Message: "Syncing workspace"})
		switch s.prepareWorkspaceSyncPass(ctx, key, result, params) {
		case workspaceSyncPassStop:
			return
		case workspaceSyncPassRetry:
			continue
		case workspaceSyncPassProceed:
		}
		synced, err := s.d.SyncWorkspace(ctx, params)
		if err != nil {
			s.setWorkspaceSyncError(key, result.Tenant, result.EnvConfig.Name, err)
		} else {
			message := fmt.Sprintf("Synced %d files, deleted %d", synced.FilesCopied, synced.FilesDeleted)
			if synced.ArtifactsCopied > 0 {
				message += fmt.Sprintf(", %d artifacts", synced.ArtifactsCopied)
			}
			s.setWorkspaceSyncStatus(key, WorkspaceSyncStatus{
				Status:     "synced",
				Message:    message,
				LastSynced: time.Now(),
				Files:      synced.FilesCopied,
			})
		}
		if !sleepWorkspaceSyncInterval(ctx, s.d.WorkspaceSyncInterval) {
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
func (s *Service) prepareWorkspaceSyncPass(ctx context.Context, key string, result config.OpenResult, params workspace.WorkspaceSyncParams) workspaceSyncPassOutcome {
	if !s.d.CanConnectLocalPort(config.SSHLocalPortForResult(result)) {
		if err := s.d.EnsureSSHD(ctx, result); err != nil {
			s.setWorkspaceSyncError(key, result.Tenant, result.EnvConfig.Name, err)
			if !sleepWorkspaceSyncInterval(ctx, s.d.WorkspaceSyncInterval) {
				return workspaceSyncPassStop
			}
			return workspaceSyncPassRetry
		}
	}
	if err := s.d.WorkspaceSyncReady(ctx, params.HostAlias); err != nil {
		s.setWorkspaceSyncStatus(key, WorkspaceSyncStatus{Status: "starting", Message: "Waiting for SSHD"})
		if !sleepWorkspaceSyncInterval(ctx, s.d.WorkspaceSyncInterval) {
			return workspaceSyncPassStop
		}
		return workspaceSyncPassRetry
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

func (s *Service) setWorkspaceSyncError(key, tenant, environment string, err error) {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "unknown error"
	}
	shouldEmit := false
	s.mu.Lock()
	if worker := s.workspaceSyncs[key]; worker != nil {
		shouldEmit = worker.lastErrorMessage != message
		worker.lastErrorMessage = message
		worker.status = WorkspaceSyncStatus{Status: "error", Message: message}
	}
	s.mu.Unlock()
	if shouldEmit {
		s.notifyEnv("error", tenant, environment, sourceWorkspaceSyncFailed,
			"Workspace sync failed: "+message, "")
	}
}

func (s *Service) setWorkspaceSyncStatus(key string, status WorkspaceSyncStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if worker := s.workspaceSyncs[key]; worker != nil {
		worker.status = status
		if status.Status == "synced" {
			worker.lastErrorMessage = ""
		}
	}
}

func (s *Service) WorkspaceSyncStatus(selection uimodel.Selection) WorkspaceSyncStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if worker := s.workspaceSyncs[uimodel.SelectionKey(selection)]; worker != nil {
		return worker.status
	}
	return WorkspaceSyncStatus{Status: "stopped"}
}

// StopAllWorkspaceSyncs cancels every worker; shutdown calls it.
func (s *Service) StopAllWorkspaceSyncs() {
	s.mu.Lock()
	workers := make([]*workspaceSyncWorker, 0, len(s.workspaceSyncs))
	for key, worker := range s.workspaceSyncs {
		workers = append(workers, worker)
		delete(s.workspaceSyncs, key)
	}
	s.mu.Unlock()
	for _, worker := range workers {
		if worker != nil && worker.cancel != nil {
			worker.cancel()
		}
	}
}
