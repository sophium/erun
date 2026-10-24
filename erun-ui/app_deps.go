package main

import (
	"context"
	"os"
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
	"github.com/sophium/erun/erun-common/primitive/deploy"
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
		deps.resolveCLIPath = resolveCLIExecutable
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

// withDefaultCloudDeps supplies the one cloud default erun-common cannot fill
// itself: CloudSecretStore has no usable zero value (it needs a real backing
// directory), so Cloudflare token operations fail unless it is wired here.
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
		deps.loadResourceStatus = loadRuntimeResourceStatus
	}
	if deps.loadClusterRegistry == nil {
		deps.loadClusterRegistry = loadClusterRegistry
	}
	if deps.loadHostedRegistry == nil {
		deps.loadHostedRegistry = loadHostedRegistryWithTestOverride
	}
	if deps.checkRuntimeDeployed == nil {
		deps.checkRuntimeDeployed = checkRuntimeDeployed
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
	if deps.resolveGatewayCredential == nil {
		deps.resolveGatewayCredential = deploy.ResolveGatewayCredential
	}
	return deps
}

// withDefaultReachabilityDeps supplies the two distinct liveness questions the
// app asks: whether a local port is held at all, and whether an MCP port
// actually carries traffic. They are separate because a stale port-forward
// answers the first yes and the second no.
//
// Both probe a raw TCP port on the host, which is not namespaced by HOME/XDG:
// the headless Playwright harness's isolated config store computes local
// port ranges purely from its own (seeded) tenant/env list, so a seeded env
// can land on the same port range a real environment on the same host has
// genuinely bound (its own MCP/SSH forwards). Unforced, that reads as a real
// answer about a seeded env that was never deployed.
// ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE pins both probes to a fixed answer
// for exactly that reason; it is set only by playwright/fixtures/seedRoot.ts,
// never in production.
func withDefaultReachabilityDeps(deps erunUIDeps) erunUIDeps {
	if deps.canConnectLocalPort == nil {
		deps.canConnectLocalPort = func(port int) bool {
			if override, ok := localPortReachabilityOverride(); ok {
				return override
			}
			return canConnectLocalTCP(port)
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

// localPortReachabilityOverride parses ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE
// ("0" or "1"). See withDefaultReachabilityDeps for why the harness needs it.
func localPortReachabilityOverride() (bool, bool) {
	raw := strings.TrimSpace(os.Getenv("ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE"))
	if raw == "" {
		return false, false
	}
	return raw == "1", true
}

// loadHostedRegistryWithTestOverride is the default loadHostedRegistry dep:
// the test override wins when set, otherwise it defers to the real probe.
func loadHostedRegistryWithTestOverride(ctx context.Context) uiHostedRegistryStatus {
	available, ok := hostedRegistryReachabilityOverride()
	if !ok {
		return loadHostedRegistry(ctx)
	}
	status := uiHostedRegistryStatus{Host: config.HostedRegistryHost, Available: available}
	if !available {
		status.Reason = "does not resolve"
		status.Recovery = "Choose a different registry instead."
	}
	return status
}

// hostedRegistryReachabilityOverride parses ERUN_HOSTED_REGISTRY_PROBE_OVERRIDE
// ("0" or "1"), pinning the new-environment dialog's hosted-registry probe to
// a fixed answer instead of a real network call to registry.erunpaas.com.
// Set only by playwright/fixtures/seedRoot.ts, never in production — the
// headless harness has no route to stub a Go-side outbound HTTP call the way
// page.route stubs a Wails method, so without this every spec that opens the
// dialog would depend on real DNS/network behavior.
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
			return ensureMCPViaOpenCommand(ctx, deps.resolveCLIPath(), result)
		}
	}
	if deps.reconnectMCP == nil {
		deps.reconnectMCP = func(ctx context.Context, result config.OpenResult, onLine func(string)) error {
			return runOpenForReconnect(ctx, deps.resolveCLIPath(), result, onLine)
		}
	}
	if deps.ensureSSHD == nil {
		deps.ensureSSHD = func(ctx context.Context, result config.OpenResult) error {
			return ensureSSHDViaOpenCommand(ctx, deps.resolveCLIPath(), result)
		}
	}
	if deps.startTerminal == nil {
		deps.startTerminal = startTerminalSession
	}
	if deps.runIDECommand == nil {
		deps.runIDECommand = runIDECommand
	}
	if deps.launchHostArtifact == nil {
		deps.launchHostArtifact = launchHostArtifactDetached
	}
	if deps.launchHostOpener == nil {
		deps.launchHostOpener = launchHostOpenerDetached
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

// withDefaultOrchestratorDeps fills in what an orchestrator's own state —
// where its durable records live, and the GitHub boundary its claim label is
// provisioned through — defaults to.
func withDefaultOrchestratorDeps(deps erunUIDeps) erunUIDeps {
	if deps.orchestratorRestoreDir == "" {
		deps.orchestratorRestoreDir = defaultOrchestratorRestoreDir()
	}
	if deps.orchestratorOpenPath == "" {
		deps.orchestratorOpenPath = defaultOrchestratorOpenPath()
	}
	if deps.orchestratorNudgeHistoryPath == "" {
		deps.orchestratorNudgeHistoryPath = defaultOrchestratorNudgeHistoryPath()
	}
	if deps.runOrchestratorLabelCommand == nil {
		deps.runOrchestratorLabelCommand = execWorkingIssueCommand
	}
	return deps
}

func withDefaultWorkspaceDeps(deps erunUIDeps) erunUIDeps {
	if deps.savePastedFile == nil {
		deps.savePastedFile = savePastedFileToRuntime
	}
	if deps.listAgentOutputs == nil {
		deps.listAgentOutputs = listAgentOutputsViaRuntime
	}
	if deps.downloadAgentOutput == nil {
		deps.downloadAgentOutput = downloadAgentOutputViaRuntime
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
		deps.workspaceSyncInterval = defaultWorkspaceSyncInterval
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
		deps.execRuntimePod = func(ctx context.Context, selection uiSelection, script string) (string, error) {
			return execInRuntimePodViaKubectl(ctx, selection, deps.store, script)
		}
	}
	if deps.loadRuntimeUsage == nil {
		deps.loadRuntimeUsage = func(ctx context.Context, selection uiSelection) (uiRuntimeUsage, error) {
			return loadRuntimeUsageViaKubectl(ctx, deps.store, selection)
		}
	}
	if deps.environmentUsageHistoryPath == "" {
		deps.environmentUsageHistoryPath = defaultEnvironmentUsageHistoryPath()
	}
	if deps.recordActivity == nil {
		deps.recordActivity = activity.RecordEnvironmentActivity
	}
	if deps.stopCloudContext == nil {
		deps.stopCloudContext = func(_ context.Context, name string) (config.CloudContextStatus, error) {
			return cloud.StopCloudContext(core.Context{}, deps.store, cloud.CloudContextParams{Name: name}, deps.cloudContextDeps)
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
		deps.contributeStatePath = defaultContributeStatePath()
	}
	if deps.interruptedActivityPath == "" {
		deps.interruptedActivityPath = defaultInterruptedActivityPath()
	}
	if deps.desktopControlMarkerPath == "" {
		deps.desktopControlMarkerPath = core.DefaultDesktopControlMarkerPath()
	}
	return deps
}
