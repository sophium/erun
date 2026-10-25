package main

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/deploy/gateway"
	activitysvc "github.com/sophium/erun/erun-ui/internal/activity"
	"github.com/sophium/erun/erun-ui/internal/contribute"
	"github.com/sophium/erun/erun-ui/internal/envops"
	orchestratorsvc "github.com/sophium/erun/erun-ui/internal/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/runtimeview"
	settingssvc "github.com/sophium/erun/erun-ui/internal/settings"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func withDefaultCoreDeps(deps erunUIDeps) erunUIDeps {
	if deps.store == nil {
		deps.store = config.ConfigStore{}
	}
	if deps.findProjectRoot == nil {
		deps.findProjectRoot = config.FindProjectRoot
	}
	if deps.resolveCLIPath == nil {
		deps.resolveCLIPath = terminal.ResolveCLIExecutable
	}
	if deps.resolveBuildInfo == nil {
		deps.resolveBuildInfo = func() model.BuildInfo {
			return resolveCurrentBuildInfo(deps.resolveCLIPath)
		}
	}
	if deps.resolveImageRegistry == nil {
		deps.resolveImageRegistry = registry.ResolveRuntimeImageRegistryVersions
	}
	if deps.deleteNamespace == nil {
		deps.deleteNamespace = kube.DeleteKubernetesNamespace
	}
	return deps
}

// withDefaultCloudDeps wires CloudSecretStore, which has no usable zero value.
func withDefaultCloudDeps(deps erunUIDeps) erunUIDeps {
	if deps.cloudDeps.CloudSecretStore == nil {
		deps.cloudDeps.CloudSecretStore = cloud.DefaultCloudDependencies().CloudSecretStore
	}
	return deps
}

func withDefaultRuntimeDeps(deps erunUIDeps) erunUIDeps {
	deps = withDefaultRuntimeResolutionDeps(deps)
	deps = withDefaultRuntimeSessionDeps(deps)
	deps = withDefaultOrchestratorLaunchDeps(deps)
	return deps
}

func withDefaultRuntimeResolutionDeps(deps erunUIDeps) erunUIDeps {
	if deps.listKubeContexts == nil {
		deps.listKubeContexts = listKubernetesContexts
	}
	if deps.loadResourceStatus == nil {
		deps.loadResourceStatus = runtimeview.ReadResourceStatus
	}
	if deps.loadClusterRegistry == nil {
		deps.loadClusterRegistry = runtimeview.ReadClusterRegistry
	}
	if deps.loadHostedRegistry == nil {
		deps.loadHostedRegistry = loadHostedRegistryWithTestOverride
	}
	if deps.checkRuntimeDeployed == nil {
		deps.checkRuntimeDeployed = runtimeview.CheckRuntimeDeployed
	}
	if deps.stopEnvironmentRuntime == nil {
		deps.stopEnvironmentRuntime = lifecycle.RunStopEnvironment
	}
	if deps.readRuntimeRunState == nil {
		deps.readRuntimeRunState = lifecycle.ReadRuntimeRunState
	}
	deps = withDefaultReachabilityDeps(deps)
	if deps.setRemoteCloudAlias == nil {
		deps.setRemoteCloudAlias = setEnvironmentCloudAliasViaMCP
	}
	return deps
}

func withDefaultOrchestratorLaunchDeps(deps erunUIDeps) erunUIDeps {
	if deps.resolveOrchestratorLaunch == nil {
		deps.resolveOrchestratorLaunch = orchestratorLaunchCommand
	}
	if deps.claudeAgents == nil {
		deps.claudeAgents = newDefaultClaudeAgents()
	}
	if deps.readGatewayRouting == nil {
		deps.readGatewayRouting = lifecycle.ReadGatewayRouting
	}
	if deps.resolveGatewayCredential == nil {
		deps.resolveGatewayCredential = gateway.ResolveGatewayCredential
	}
	return deps
}

// withDefaultReachabilityDeps supplies the port-held and MCP-traffic probes; a
// stale port-forward answers yes to the first and no to the second.
// ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE (set only by the Playwright harness) pins
// both, since seeded envs can share port ranges with real ones on the host.
func withDefaultReachabilityDeps(deps erunUIDeps) erunUIDeps {
	if deps.canConnectLocalPort == nil {
		deps.canConnectLocalPort = func(port int) bool {
			if override, ok := localPortReachabilityOverride(); ok {
				return override
			}
			return settingssvc.CanConnectLocalTCP(port)
		}
	}
	if deps.canReachMCPEndpoint == nil {
		deps.canReachMCPEndpoint = func(port int) bool {
			if override, ok := localPortReachabilityOverride(); ok {
				return override
			}
			return mcp.CanReachLocalMCPEndpoint(port)
		}
	}
	return deps
}

// localPortReachabilityOverride parses ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE ("0" or "1").
func localPortReachabilityOverride() (bool, bool) {
	raw := strings.TrimSpace(os.Getenv("ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE"))
	if raw == "" {
		return false, false
	}
	return raw == "1", true
}

// loadHostedRegistryWithTestOverride prefers the test override over the real probe.
func loadHostedRegistryWithTestOverride(ctx context.Context) runtimeview.HostedRegistryStatus {
	available, ok := hostedRegistryReachabilityOverride()
	if !ok {
		return runtimeview.ProbeHostedRegistry(ctx)
	}
	status := runtimeview.HostedRegistryStatus{Host: config.HostedRegistryHost, Available: available}
	if !available {
		status.Reason = "does not resolve"
		status.Recovery = "Choose a different registry instead."
	}
	return status
}

// hostedRegistryReachabilityOverride parses ERUN_HOSTED_REGISTRY_PROBE_OVERRIDE
// ("0" or "1"); set only by the Playwright harness, which cannot stub Go-side HTTP.
func hostedRegistryReachabilityOverride() (bool, bool) {
	raw := strings.TrimSpace(os.Getenv("ERUN_HOSTED_REGISTRY_PROBE_OVERRIDE"))
	if raw == "" {
		return false, false
	}
	return raw == "1", true
}

func withDefaultRuntimeSessionDeps(deps erunUIDeps) erunUIDeps {
	if deps.ensureMCP == nil {
		deps.ensureMCP = func(ctx context.Context, result config.OpenResult) error {
			return terminal.EnsureMCPViaOpenCommand(ctx, deps.resolveCLIPath(), result)
		}
	}
	if deps.reconnectMCP == nil {
		deps.reconnectMCP = func(ctx context.Context, result config.OpenResult, onLine func(string)) error {
			return terminal.RunOpenForReconnect(ctx, deps.resolveCLIPath(), result, onLine)
		}
	}
	if deps.ensureSSHD == nil {
		deps.ensureSSHD = func(ctx context.Context, result config.OpenResult) error {
			return terminal.EnsureSSHDViaOpenCommand(ctx, deps.resolveCLIPath(), result)
		}
	}
	if deps.startTerminal == nil {
		deps.startTerminal = terminal.StartPTY
	}
	if deps.runIDECommand == nil {
		deps.runIDECommand = terminal.RunIDECommand
	}
	if deps.launchHostArtifact == nil {
		deps.launchHostArtifact = terminal.LaunchHostArtifactDetached
	}
	if deps.launchHostOpener == nil {
		deps.launchHostOpener = envops.LaunchHostOpenerDetached
	}
	return deps
}

func withDefaultUIDeps(deps erunUIDeps) erunUIDeps {
	deps = withDefaultWorkspaceDeps(deps)
	deps = withDefaultPodDeps(deps)
	deps = withDefaultWindowAndContributeDeps(deps)
	deps = withDefaultOrchestratorDeps(deps)
	return deps
}

// withDefaultOrchestratorDeps fills in orchestrator state and GitHub defaults.
func withDefaultOrchestratorDeps(deps erunUIDeps) erunUIDeps {
	if deps.orchestratorRestoreDir == "" {
		deps.orchestratorRestoreDir = defaultOrchestratorRestoreDir()
	}
	if deps.orchestratorOpenPath == "" {
		deps.orchestratorOpenPath = orchestratorsvc.DefaultOpenPath()
	}
	if deps.orchestratorNudgeHistoryPath == "" {
		deps.orchestratorNudgeHistoryPath = orchestratorsvc.DefaultNudgeHistoryPath()
	}
	if deps.runOrchestratorLabelCommand == nil {
		deps.runOrchestratorLabelCommand = execWorkingIssueCommand
	}
	return deps
}

func withDefaultWorkspaceDeps(deps erunUIDeps) erunUIDeps {
	if deps.savePastedFile == nil {
		deps.savePastedFile = terminal.SavePastedFileToRuntime
	}
	if deps.listAgentOutputs == nil {
		deps.listAgentOutputs = envops.ListAgentOutputsViaRuntime
	}
	if deps.downloadAgentOutput == nil {
		deps.downloadAgentOutput = envops.DownloadAgentOutputViaRuntime
	}
	if deps.loadDiff == nil {
		deps.loadDiff = loadDiffFromMCP
	}
	deps = withDefaultExecWriteDeps(deps)
	if deps.loadIdleStatus == nil {
		deps.loadIdleStatus = loadIdleStatusFromMCP
	}
	deps = withDefaultEnvironmentJobDeps(deps)
	if deps.loadAPILog == nil {
		deps.loadAPILog = loadAPILog
	}
	if deps.workspaceSyncReady == nil {
		deps.workspaceSyncReady = workspace.WorkspaceSyncSSHReady
	}
	if deps.syncWorkspace == nil {
		deps.syncWorkspace = workspace.SyncWorkspaceOnce
	}
	if deps.workspaceSyncInterval <= 0 {
		deps.workspaceSyncInterval = envops.DefaultWorkspaceSyncInterval
	}
	return deps
}

// withDefaultExecWriteDeps is split out to stay under the cyclomatic-complexity cap.
func withDefaultExecWriteDeps(deps erunUIDeps) erunUIDeps {
	if deps.execCommit == nil {
		deps.execCommit = execCommitViaMCP
	}
	if deps.execPush == nil {
		deps.execPush = execPushViaMCP
	}
	return deps
}

func withDefaultEnvironmentJobDeps(deps erunUIDeps) erunUIDeps {
	if deps.loadEnvironmentJobs == nil {
		deps.loadEnvironmentJobs = loadEnvironmentJobsFromMCP
	}
	if deps.readEnvironmentJobOutput == nil {
		deps.readEnvironmentJobOutput = readEnvironmentJobOutputFromMCP
	}
	if deps.cancelEnvironmentJob == nil {
		deps.cancelEnvironmentJob = cancelEnvironmentJobFromMCP
	}
	if deps.whipEnvironment == nil {
		deps.whipEnvironment = whipEnvironmentViaMCP
	}
	return deps
}

func withDefaultPodDeps(deps erunUIDeps) erunUIDeps {
	if deps.runWorkingIssueCommand == nil {
		deps.runWorkingIssueCommand = execWorkingIssueCommand
	}
	if deps.loadPodBranch == nil {
		deps.loadPodBranch = loadPodBranchFromMCP
	}
	if deps.runPodRaw == nil {
		deps.runPodRaw = runPodRawFromMCP
	}
	if deps.execRuntimePod == nil {
		deps.execRuntimePod = func(ctx context.Context, selection uimodel.Selection, script string) (string, error) {
			return execInRuntimePodViaKubectl(ctx, selection, deps.store, script)
		}
	}
	if deps.loadRuntimeUsage == nil {
		deps.loadRuntimeUsage = func(ctx context.Context, selection uimodel.Selection) (runtimeview.Usage, error) {
			return runtimeview.ReadUsageViaKubectl(ctx, deps.store, selection)
		}
	}
	if deps.environmentUsageHistoryPath == "" {
		deps.environmentUsageHistoryPath = runtimeview.DefaultUsageHistoryPath()
	}
	if deps.recordActivity == nil {
		deps.recordActivity = activity.RecordEnvironmentActivity
	}
	if deps.stopCloudContext == nil {
		deps.stopCloudContext = func(_ context.Context, name string) (model.CloudContextStatus, error) {
			return cloud.StopCloudContext(core.Context{}, deps.store, model.CloudContextParams{Name: name}, deps.cloudContextDeps)
		}
	}
	return deps
}

func withDefaultWindowAndContributeDeps(deps erunUIDeps) erunUIDeps {
	if deps.windowStatePath == "" {
		deps.windowStatePath = defaultAppWindowStatePath()
	}
	if deps.windowMaximised == nil {
		deps.windowMaximised = runtime.WindowIsMaximised
	}
	if deps.cloneERun == nil {
		deps.cloneERun = cloneERunViaMCP
	}
	if deps.contributeStatePath == "" {
		deps.contributeStatePath = contribute.DefaultStatePath()
	}
	if deps.interruptedActivityPath == "" {
		deps.interruptedActivityPath = activitysvc.DefaultInterruptedActivityPath()
	}
	if deps.desktopControlMarkerPath == "" {
		deps.desktopControlMarkerPath = core.DefaultDesktopControlMarkerPath()
	}
	return deps
}

func execWorkingIssueCommand(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	core.HideConsoleWindow(cmd)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
