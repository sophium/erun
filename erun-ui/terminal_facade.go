package main

import (
	"context"
	"errors"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The terminal session surface; the behavior lives in internal/terminal.

func (a *App) CancelPendingIdleStop(selection uimodel.Selection) error {
	return a.terminal.CancelPendingIdleStop(selection)
}

func (a *App) CloseEnvironmentSessions(selection uimodel.Selection) ([]int, error) {
	return a.terminal.CloseEnvironmentSessions(selection)
}

func (a *App) CloseSession(sessionID int) error {
	return a.terminal.CloseSession(sessionID)
}

func (a *App) DeleteEnvironment(selection uimodel.Selection, confirmation string) (terminal.DeleteEnvironmentResult, error) {
	return a.terminal.DeleteEnvironment(selection, confirmation)
}

func (a *App) EndAISessions(selection uimodel.Selection) (bool, error) {
	return a.terminal.EndAISessions(selection)
}

func (a *App) ListRemoteAppSessions(selection uimodel.Selection) []string {
	return a.terminal.ListRemoteAppSessions(selection)
}

func (a *App) LoadDiff(selection uimodel.Selection, options uimodel.DiffOptions) (model.DiffResult, error) {
	return a.terminal.LoadDiff(selection, options)
}

func (a *App) LoadIdleStatus(selection uimodel.Selection) (terminal.IdleStatus, error) {
	return a.terminal.LoadIdleStatus(selection)
}

func (a *App) LoadStopHistory(selection uimodel.Selection) ([]terminal.LastStopEvent, error) {
	return a.terminal.LoadStopHistory(selection)
}

func (a *App) OpenIDE(selection uimodel.Selection, ide string) error {
	return a.terminal.OpenIDE(selection, ide)
}

func (a *App) ReconnectMCP(selection uimodel.Selection) error {
	return a.terminal.ReconnectMCP(selection)
}

func (a *App) RepaintSession(sessionID int) error {
	return a.terminal.RepaintSession(sessionID)
}

func (a *App) ResizeSession(sessionID, cols, rows int) error {
	return a.terminal.ResizeSession(sessionID, cols, rows)
}

func (a *App) SavePastedFile(sessionID int, payload terminal.PastedFilePayload) (terminal.PastedFileResult, error) {
	return a.terminal.SavePastedFile(sessionID, payload)
}

func (a *App) SendSessionInput(sessionID int, data string) error {
	return a.terminal.SendSessionInput(sessionID, data)
}

func (a *App) StartAISession(selection uimodel.Selection, slot, cols, rows int, confirmed bool) (uimodel.StartSessionResult, error) {
	return a.terminal.StartAISession(selection, slot, cols, rows, confirmed)
}

func (a *App) StartCloudInitAWSSession(cols, rows int) (uimodel.StartSessionResult, error) {
	return a.terminal.StartCloudInitAWSSession(cols, rows)
}

func (a *App) StartCloudInitCloudflareSession(cols, rows int) (uimodel.StartSessionResult, error) {
	return a.terminal.StartCloudInitCloudflareSession(cols, rows)
}

func (a *App) StartCreateVersionSession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	return a.terminal.StartCreateVersionSession(selection, cols, rows)
}

func (a *App) StartDeploySession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	return a.terminal.StartDeploySession(selection, cols, rows)
}

func (a *App) StartDoctorSession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	return a.terminal.StartDoctorSession(selection, cols, rows)
}

func (a *App) StartForceDeploySession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	return a.terminal.StartForceDeploySession(selection, cols, rows)
}

func (a *App) StartInitSession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	return a.terminal.StartInitSession(selection, cols, rows)
}

func (a *App) StartInitialDeploySession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	return a.terminal.StartInitialDeploySession(selection, cols, rows)
}

func (a *App) StartLocalSession(selection uimodel.Selection, slot, cols, rows int) (uimodel.StartSessionResult, error) {
	return a.terminal.StartLocalSession(selection, slot, cols, rows)
}

func (a *App) StartSSHDInitSession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	return a.terminal.StartSSHDInitSession(selection, cols, rows)
}

func (a *App) StartSession(selection uimodel.Selection, slot, cols, rows int) (uimodel.StartSessionResult, error) {
	return a.terminal.StartSession(selection, slot, cols, rows)
}

func (a *App) StartUpgradeEnvironmentSession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	return a.terminal.StartUpgradeEnvironmentSession(selection, cols, rows)
}

// newTerminalService reads the deps funcs and sibling services per call, so a
// test that builds App by hand or swaps a dep after NewApp still reaches its fake.
func (a *App) newTerminalService() *terminal.Service {
	return terminal.New(terminal.Deps{
		Store:           a.deps.store,
		FindProjectRoot: func() (string, string, error) { return a.deps.findProjectRoot() },
		Context:         a.context,
		Started:         func() bool { return a.ctx != nil },
		Emit:            a.emit,
		AppSessionEnv:   appSessionEnvVar + "=1",
		ResolveCLIPath:  func() string { return a.deps.resolveCLIPath() },
		StartTerminal: func(params terminal.StartParams) (terminal.PTY, error) {
			return a.deps.startTerminal(params)
		},
		RunIDECommand: func(ctx context.Context, params terminal.StartParams) (string, error) {
			return a.deps.runIDECommand(ctx, params)
		},
		RecordEnvironmentActivity: func(params model.EnvironmentActivityParams) error {
			if a.deps.recordActivity == nil {
				return nil
			}
			return a.deps.recordActivity(params)
		},
		SavePastedFile: func(params terminal.PasteParams) (string, error) {
			return a.deps.savePastedFile(params)
		},
		LoadDiff: func(ctx context.Context, endpoint, bearer string, options uimodel.DiffOptions) (model.DiffResult, error) {
			return a.deps.loadDiff(ctx, endpoint, bearer, options)
		},
		LoadIdleStatus: func(ctx context.Context, endpoint, bearer string) (model.EnvironmentIdleStatus, error) {
			return a.deps.loadIdleStatus(ctx, endpoint, bearer)
		},
		EnsureMCP: func(ctx context.Context, result config.OpenResult) error {
			if a.deps.ensureMCP == nil {
				return nil
			}
			return a.deps.ensureMCP(ctx, result)
		},
		ReconnectMCP: func(ctx context.Context, result config.OpenResult, onLine func(string)) error {
			if a.deps.reconnectMCP == nil {
				return errors.New("reconnect is not configured")
			}
			return a.deps.reconnectMCP(ctx, result, onLine)
		},
		DeleteNamespace: func(kubernetesContext, namespace string) error {
			return a.deps.deleteNamespace(kubernetesContext, namespace)
		},
		ExecRuntimePod:   a.execInRuntimePod,
		ProbePodSessions: a.probePodSessions,
		CanConnectLocalPort: func(port int) bool {
			return a.deps.canConnectLocalPort != nil && a.deps.canConnectLocalPort(port)
		},
		// A missing probe reads as answering, so an unknown never triggers a restart.
		CanReachMCPEndpoint: func(port int) bool {
			return a.deps.canReachMCPEndpoint == nil || a.deps.canReachMCPEndpoint(port)
		},
		MCPEndpoint:        mcpEndpointForOpenResult,
		MCPBearer:          a.mcpBearer,
		WrapMCPUnreachable: wrapMCPUnreachableErrorWithKind,
		IsMCPDialFailure:   isMCPDialFailure,
		MCPTransport: func(endpoint, bearer string) mcpsdk.Transport {
			return mcpClientTransport(endpoint, bearer, false)
		},
		ClientVersion: func() string { return currentBuildInfo().Version },
		LinkedCloudContext: func(env config.EnvConfig) (model.CloudContextStatus, bool, error) {
			return a.settings.LinkedCloudContext(env)
		},
		EnsureLinkedCloudContextRunning: a.ensureLinkedCloudContextRunning,
		StopCloudContext:                a.stopCloudContext,
		StartCloudRefresher:             func(selection uimodel.Selection) { a.cloudContexts.StartRefresher(selection) },
		StopCloudRefresher:              func(selection uimodel.Selection) { a.cloudContexts.StopRefresher(selection) },
		EnsureRuntimeOnce:               func(selection uimodel.Selection) { a.envops.EnsureRuntimeOnce(selection) },
		RuntimeStopped:                  func(selection uimodel.Selection) bool { return a.envops.RuntimeStopped(selection) },
		ClearRuntimeStopped:             func(selection uimodel.Selection) { a.envops.ClearRuntimeStopped(selection) },
		StartWorkspaceSync:              func(selection uimodel.Selection) { a.envops.StartWorkspaceSync(selection) },
		StopWorkspaceSync:               func(selection uimodel.Selection) { a.envops.StopWorkspaceSync(selection) },
		ClearInitEmitted:                func(tenant, environment string) { a.envops.ClearInitEmitted(tenant, environment) },
		EnvironmentJobIDs:               a.environmentJobIDs,
		EnqueueGated: func(selection uimodel.Selection, kind string, start terminal.GatedStart) (uimodel.StartSessionResult, error) {
			return a.activity.EnqueueGatedSession(selection, kind, gatedStart(start))
		},
		OnOutput:                   a.feedActivityTraceFromTerminal,
		RememberKubeContext:        func(name string) { a.activity.RememberKubeContext(name) },
		LockIfDeployInFlight:       func(selection uimodel.Selection, serial int) { a.activity.LockIfDeployInFlight(selection, serial) },
		LatestDeployFailed:         func(tenant, environment string) bool { return a.activity.LatestDeployFailed(tenant, environment) },
		AppendMCPAuthPublicKeyFlag: a.appendMCPAuthPublicKeyFlag,
		RunDeployOrchestration: func(ctx context.Context, selection uimodel.Selection, result config.OpenResult, force bool) error {
			return a.activity.RunDeployOrchestration(ctx, selection, result, force)
		},
		MaybeStartDeployOrchestration: func(selection uimodel.Selection, force bool) (uimodel.StartSessionResult, bool) {
			return a.activity.MaybeStartDeployOrchestration(selection, force)
		},
		RearmOrchestratorPacing:      func(id string) { a.orchestrator.RearmOrchestratorPacing(id) },
		OrchestratorRegisteredLocked: func(id string) bool { return a.orchestrator.OrchestratorRegisteredLocked(id) },
	})
}

// probePodSessions reads the pod's app sessions through the runtime readings.
func (a *App) probePodSessions(selection uimodel.Selection) ([]terminal.PodSession, error) {
	activity, err := a.runtime.ProbeRuntimeActivity(selection)
	if err != nil {
		return nil, err
	}
	sessions := make([]terminal.PodSession, 0, len(activity.Sessions))
	for _, session := range activity.Sessions {
		sessions = append(sessions, terminal.PodSession{ID: session.ID, Running: session.Running})
	}
	return sessions, nil
}

// environmentJobIDs lists the env's job ids through the bound job listing.
func (a *App) environmentJobIDs(tenant, environment string) ([]string, error) {
	jobs, err := a.LoadEnvironmentJobs(tenant, environment)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.ID)
	}
	return ids, nil
}
