package main

import (
	"context"
	"runtime"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	activitysvc "github.com/sophium/erun/erun-ui/internal/activity"
	"github.com/sophium/erun/erun-ui/internal/envops"
	tenantsvc "github.com/sophium/erun/erun-ui/internal/tenant"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// The environment operations surface; the behavior lives in internal/envops.
// Native dialogs stay here.

func (a *App) CancelEnvironmentJob(input envops.CancelJobInput) (envops.EnvironmentJob, error) {
	return a.envops.CancelEnvironmentJob(input)
}

func (a *App) DiffReviewStatus(input envops.DiffReviewStatusInput) (envops.DiffReviewStatus, error) {
	return a.envops.DiffReviewStatus(input)
}

// DownloadAgentOutput saves one runtime output via a Save dialog; "" means cancelled.
func (a *App) DownloadAgentOutput(selection uimodel.Selection, name string) (string, error) {
	return a.envops.DownloadAgentOutput(selection, name, a.saveFileDialog("Save agent output"))
}

func (a *App) ExecCommit(selection uimodel.Selection, input envops.ExecCommitInput) (model.CommitWorkingTreeResult, error) {
	return a.envops.ExecCommit(selection, input)
}

func (a *App) ExecPush(selection uimodel.Selection, input envops.ExecPushInput) (model.PushWorkingTreeBranchResult, error) {
	return a.envops.ExecPush(selection, input)
}

func (a *App) ExposeEnvironmentService(selection uimodel.Selection, input envops.ExposeServiceInput) (envops.ExposedService, error) {
	return a.envops.ExposeEnvironmentService(selection, input)
}

func (a *App) ListAgentOutputs(selection uimodel.Selection) (model.RuntimeOutputsListResult, error) {
	return a.envops.ListAgentOutputs(selection)
}

func (a *App) ListEnvironmentExposures(selection uimodel.Selection) (envops.ExposureList, error) {
	return a.envops.ListEnvironmentExposures(selection)
}

func (a *App) ListEnvironmentServices(selection uimodel.Selection) (envops.EnvironmentServiceList, error) {
	return a.envops.ListEnvironmentServices(selection)
}

func (a *App) ListHostArtifacts(selection uimodel.Selection) ([]envops.HostArtifact, error) {
	return a.envops.ListHostArtifacts(selection)
}

func (a *App) LoadDirectoryDiff(directory string, options uimodel.DiffOptions) (model.DiffResult, error) {
	return a.envops.LoadDirectoryDiff(directory, options)
}

func (a *App) LoadEnvironmentJobs(tenant, environment string) ([]envops.EnvironmentJob, error) {
	return a.envops.LoadEnvironmentJobs(tenant, environment)
}

func (a *App) LoadHostDiff(selection uimodel.Selection, options uimodel.DiffOptions) (model.DiffResult, error) {
	return a.envops.LoadHostDiff(selection, options)
}

func (a *App) LoadRuntimeRunState(selection uimodel.Selection) (envops.RuntimeRunState, error) {
	return a.envops.LoadRuntimeRunState(selection)
}

func (a *App) OpenHostIDE(selection uimodel.Selection, ide string) error {
	return a.envops.OpenHostIDE(selection, ide)
}

func (a *App) OpenHostPath(hostPath string) error {
	return a.envops.OpenHostPath(hostPath)
}

func (a *App) ReadEnvironmentJobOutput(input envops.JobOutputInput) (envops.EnvironmentJobOutput, error) {
	return a.envops.ReadEnvironmentJobOutput(input)
}

func (a *App) ResolveEnvironmentHostPath(selection uimodel.Selection, podPath string) (envops.PodPathResolution, error) {
	return a.envops.ResolveEnvironmentHostPath(selection, podPath)
}

func (a *App) ResolveUpgradePlan() (lifecycle.UpgradePlan, error) {
	return a.envops.ResolveUpgradePlan()
}

func (a *App) RevealHostPath(hostPath string) error {
	return a.envops.RevealHostPath(hostPath)
}

func (a *App) RunHostArtifact(selection uimodel.Selection, relPath string) error {
	return a.envops.RunHostArtifact(selection, relPath)
}

func (a *App) StopEnvironment(selection uimodel.Selection) (envops.EnvironmentStopResult, error) {
	return a.envops.StopEnvironment(selection)
}

func (a *App) UnexposeEnvironment(selection uimodel.Selection) (envops.UnexposeResult, error) {
	return a.envops.UnexposeEnvironment(selection)
}

// saveFileDialog runs the native Save dialog the operations hand their output to.
func (a *App) saveFileDialog(title string) func(defaultName string) (string, error) {
	return func(defaultName string) (string, error) {
		return wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
			Title:           title,
			DefaultFilename: defaultName,
		})
	}
}

// newEnvopsService reads the deps funcs and sibling services per call, so a
// test that builds App by hand or swaps a dep after NewApp still reaches its fake.
func (a *App) newEnvopsService() *envops.Service {
	return envops.New(envops.Deps{
		Store:           a.deps.store,
		Cloud:           a.deps.cloudDeps,
		FindProjectRoot: func() (string, string, error) { return a.deps.findProjectRoot() },
		Context:         a.context,
		Started:         func() bool { return a.ctx != nil },
		Emit:            a.emit,
		MCPEndpoint:     mcpEndpointForOpenResult,
		MCPBearer:       a.mcpBearer,
		CanConnectLocalPort: func(port int) bool {
			return a.deps.canConnectLocalPort(port)
		},
		// A missing probe reads as answering, so an unknown never triggers a restart.
		CanReachMCPEndpoint: func(port int) bool {
			return a.deps.canReachMCPEndpoint == nil || a.deps.canReachMCPEndpoint(port)
		},
		WrapMCPUnreachable: wrapMCPUnreachableErrorWithKind,
		IsMCPDialFailure:   isMCPDialFailure,
		ReconnectMCP: func(ctx context.Context, result config.OpenResult, onLine func(string)) error {
			return a.deps.reconnectMCP(ctx, result, onLine)
		},
		EnsureSSHD: func(ctx context.Context, result config.OpenResult) error {
			return a.deps.ensureSSHD(ctx, result)
		},
		ReconnectLineHandler: func(selection uimodel.Selection) func(string) {
			return a.activity.TraceLineHandler(selection, activitysvc.KindOpen)
		},
		DeployInFlight: func(selection uimodel.Selection) bool {
			return a.terminal.DeployInFlight(selection)
		},
		NoteEdgeAnswering: func(selection uimodel.Selection) { a.orchestrator.NoteEdgeAnswering(selection) },
		StopRuntime: func(ctx core.Context, params lifecycle.StopEnvironmentParams) (model.StopEnvironmentResult, error) {
			return a.deps.stopEnvironmentRuntime(ctx, params)
		},
		ReadRuntimeRunState: func(ctx core.Context, target lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
			return a.deps.readRuntimeRunState(ctx, target)
		},
		ResolveImageRegistry: func(ctx context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error) {
			return a.deps.resolveImageRegistry(ctx, namespace, repository)
		},
		LoadJobs: func(ctx context.Context, endpoint, bearer string) ([]jobstate.EnvironmentJob, error) {
			return a.deps.loadEnvironmentJobs(ctx, endpoint, bearer)
		},
		ReadJobOutput: func(ctx context.Context, endpoint, bearer string, params model.ReadEnvironmentJobOutputParams) (jobs.EnvironmentJobOutput, error) {
			return a.deps.readEnvironmentJobOutput(ctx, endpoint, bearer, params)
		},
		CancelJob: func(ctx context.Context, endpoint, bearer string, params model.CancelEnvironmentJobParams) (jobs.CancelEnvironmentJobResult, error) {
			return a.deps.cancelEnvironmentJob(ctx, endpoint, bearer, params)
		},
		ExecCommit: func(ctx context.Context, endpoint, bearer, branch, message string) (model.CommitWorkingTreeResult, error) {
			return a.deps.execCommit(ctx, endpoint, bearer, branch, message)
		},
		ExecPush: func(ctx context.Context, endpoint, bearer, branch, remote string) (model.PushWorkingTreeBranchResult, error) {
			return a.deps.execPush(ctx, endpoint, bearer, branch, remote)
		},
		ListAgentOutputs: func(result config.OpenResult, params model.RuntimeOutputsParams) (model.RuntimeOutputsListResult, error) {
			return a.deps.listAgentOutputs(result, params)
		},
		DownloadAgentOutput: func(result config.OpenResult, params model.RuntimeOutputDownloadParams) (model.RuntimeOutputResult, error) {
			return a.deps.downloadAgentOutput(result, params)
		},
		LaunchHostOpener: func(executable string, args []string) error {
			return a.deps.launchHostOpener(executable, args)
		},
		LaunchHostArtifact: func(exePath, dir string) error {
			return a.deps.launchHostArtifact(exePath, dir)
		},
		OpenHostIDE: a.openLocalIDE,
		WorkspaceSyncReady: func(ctx context.Context, hostAlias string) error {
			return a.deps.workspaceSyncReady(ctx, hostAlias)
		},
		SyncWorkspace: func(ctx context.Context, params workspace.WorkspaceSyncParams) (model.WorkspaceSyncResult, error) {
			return a.deps.syncWorkspace(ctx, params)
		},
		WorkspaceSyncInterval: a.deps.workspaceSyncInterval,
		ReviewPlatform: func(tenant string) (string, *platformclient.PlatformClient, error) {
			resolution, err := a.tenant.Platform(tenant, "")
			if err != nil {
				return "", nil, err
			}
			return resolution.State, resolution.Client, nil
		},
		RestrictedRead: tenantsvc.RestrictedRead,
		QueuePosition:  tenantsvc.QueuePosition,
		Review: envops.ReviewPolicy{
			ReadReviews:       tenantsvc.ReadReviews,
			ReadComments:      tenantsvc.ReadComments,
			ReadMergeQueue:    tenantsvc.ReadMergeQueue,
			AdvanceMergeQueue: tenantsvc.WriteAdvanceMergeQueue,
			NoPermissionState: tenantsvc.StateNoPermission,
			RequestTimeout:    tenantsvc.RequestTimeout,
		},
	})
}

// openLocalIDE launches ide on a host path; the IDE command line is the terminal side's.
func (a *App) openLocalIDE(ide, path string) error {
	executable, args, err := terminal.LocalOpenIDECommand(runtime.GOOS, ide, path)
	if err != nil {
		return err
	}
	output, err := a.deps.runIDECommand(a.context(), terminal.StartParams{
		Dir:        path,
		Executable: executable,
		Args:       args,
		Env:        []string{appSessionEnvVar + "=1"},
	})
	if err == nil {
		return nil
	}
	return terminal.FormatOpenIDEError(ide, output, err)
}

// reportHostArtifactSigning surfaces ad-hoc signing for the orchestrator's outputs.
func (a *App) reportHostArtifactSigning(signing model.HostArtifactSigning) {
	if kind, note := envops.SigningNotice(signing); note != "" {
		a.emitAppNotification(kind, note)
	}
}
