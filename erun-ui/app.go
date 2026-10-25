package main

import (
	"context"
	"log"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/envstore"
	"github.com/sophium/erun/erun-common/base/seam/kubeapi"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/command/lifecycle/gatewayrouting"
	"github.com/sophium/erun/erun-common/command/lifecycle/stop"
	orchestratorpkg "github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	activitysvc "github.com/sophium/erun/erun-ui/internal/activity"
	"github.com/sophium/erun/erun-ui/internal/cloudctx"
	"github.com/sophium/erun/erun-ui/internal/contribute"
	"github.com/sophium/erun/erun-ui/internal/envops"
	"github.com/sophium/erun/erun-ui/internal/investigate"
	orchestratorsvc "github.com/sophium/erun/erun-ui/internal/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/runtimeview"
	settingssvc "github.com/sophium/erun/erun-ui/internal/settings"
	tenantsvc "github.com/sophium/erun/erun-ui/internal/tenant"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const appSessionEnvVar = orchestratorpkg.DesktopSessionEnvVar

// workingIssueCommandRunner runs a host command in dir and returns its trimmed stdout.
type workingIssueCommandRunner func(ctx context.Context, dir, name string, args ...string) (string, error)

type erunUIStore interface {
	envstore.ListStore
	SaveERunConfig(config.ERunConfig) error
	SaveTenantConfig(model.TenantConfig) error
	SaveEnvConfig(string, config.EnvConfig) error
}

type erunUIDeps struct {
	store                  erunUIStore
	findProjectRoot        projectroot.ProjectFinderFunc
	resolveCLIPath         func() string
	resolveBuildInfo       func() model.BuildInfo
	resolveImageRegistry   func(context.Context, string, string) (model.RuntimeRegistryVersions, error)
	cloudDeps              cloud.CloudDependencies
	cloudContextDeps       cloudcontext.CloudContextDependencies
	deleteNamespace        kubeapi.NamespaceDeleterFunc
	listKubeContexts       func() ([]string, error)
	loadResourceStatus     func(context.Context, runtimeview.ResourceInput) (runtimeview.ResourceStatus, error)
	loadClusterRegistry    func(context.Context, runtimeview.ResourceInput) (runtimeview.ClusterRegistryStatus, error)
	loadHostedRegistry     func(context.Context) runtimeview.HostedRegistryStatus
	checkRuntimeDeployed   func(context.Context, string, string, string) (bool, error)
	stopEnvironmentRuntime func(core.Context, stop.StopEnvironmentParams) (model.StopEnvironmentResult, error)
	readRuntimeRunState    func(core.Context, stop.RuntimeScaleTarget) (stop.RuntimeRunState, error)
	ensureMCP              func(context.Context, config.OpenResult) error
	reconnectMCP           func(context.Context, config.OpenResult, func(string)) error
	ensureSSHD             func(context.Context, config.OpenResult) error
	canConnectLocalPort    func(int) bool
	// canReachMCPEndpoint checks traffic, not a dial: a stale port-forward accepts and never answers.
	canReachMCPEndpoint       func(int) bool
	setRemoteCloudAlias       func(context.Context, string, string, string, string, string) (config.EnvConfig, error)
	startTerminal             func(terminal.StartParams) (terminal.PTY, error)
	runIDECommand             func(context.Context, terminal.StartParams) (string, error)
	launchHostArtifact        func(exePath, dir string) error
	launchHostOpener          func(executable string, args []string) error
	resolveOrchestratorLaunch func(launch orchestratorpkg.OrchestratorLaunch, agentID, initialPrompt, mcpConfigPath string) (string, []string, error)
	claudeAgents              orchestratorsvc.AgentCLI
	resolveGatewayCredential  func(*config.OpenRouterConfig) (string, error)
	savePastedFile            func(terminal.PasteParams) (string, error)
	listAgentOutputs          func(config.OpenResult, model.RuntimeOutputsParams) (model.RuntimeOutputsListResult, error)
	downloadAgentOutput       func(config.OpenResult, model.RuntimeOutputDownloadParams) (model.RuntimeOutputResult, error)
	loadDiff                  func(context.Context, string, string, uimodel.DiffOptions) (model.DiffResult, error)
	execCommit                func(ctx context.Context, endpoint, bearer, branch, message string) (model.CommitWorkingTreeResult, error)
	execPush                  func(ctx context.Context, endpoint, bearer, branch, remote string) (model.PushWorkingTreeBranchResult, error)
	loadIdleStatus            func(context.Context, string, string) (model.EnvironmentIdleStatus, error)
	loadEnvironmentJobs       func(context.Context, string, string) ([]jobstate.EnvironmentJob, error)
	readEnvironmentJobOutput  func(context.Context, string, string, model.ReadEnvironmentJobOutputParams) (jobs.EnvironmentJobOutput, error)
	cancelEnvironmentJob      func(context.Context, string, string, model.CancelEnvironmentJobParams) (jobs.CancelEnvironmentJobResult, error)
	whipEnvironment           func(context.Context, string, string, string, string) (model.WhipResult, error)
	loadAPILog                func(context.Context, tenantsvc.DashboardInput) (string, error)
	readGatewayRouting        func(context.Context, config.EnvConfig, string, *config.OpenRouterConfig) (gatewayrouting.GatewayRouting, bool, error)
	workspaceSyncReady        func(context.Context, string) error
	syncWorkspace             func(context.Context, workspace.WorkspaceSyncParams) (model.WorkspaceSyncResult, error)
	workspaceSyncInterval     time.Duration
	recordActivity            func(model.EnvironmentActivityParams) error
	runWorkingIssueCommand    workingIssueCommandRunner
	loadPodBranch             func(context.Context, string, string) (string, error)
	runPodRaw                 func(context.Context, string, string, []string) (string, error)
	execRuntimePod            func(context.Context, uimodel.Selection, string) (string, error)
	loadRuntimeUsage          func(context.Context, uimodel.Selection) (runtimeview.Usage, error)
	stopCloudContext          func(context.Context, string) (model.CloudContextStatus, error)
	windowStatePath           string
	windowMaximised           func(context.Context) bool
	// skipLoginShellEnv is set for --headless, whose caller supplies its environment deliberately.
	skipLoginShellEnv            bool
	cloneERun                    func(context.Context, string, string) error
	contributeStatePath          string
	investigationReportDir       string
	interruptedActivityPath      string
	orchestratorRestoreDir       string
	orchestratorOpenPath         string
	orchestratorNudgeHistoryPath string
	environmentUsageHistoryPath  string
	relaunchApp                  func() error
	quitApp                      func()
	desktopControlMarkerPath     string
	// runOrchestratorLabelCommand is a separately named seam so tests stub the right boundary.
	runOrchestratorLabelCommand workingIssueCommandRunner
}

type App struct {
	ctx  context.Context
	deps erunUIDeps

	// identity signs per-env MCP bearers; nil in unit tests.
	identity *desktopIdentity

	// investigations has its own lock; never call into it while holding the terminal lock.
	investigations *investigate.Service

	// closeConfirmed lets the second beforeClose pass from wailsruntime.Quit
	// proceed without prompting. It is guarded by the terminal lock
	// (terminal.WithLock) until the window-close concern moves out of main.
	closeConfirmed bool

	// emitMu guards emitFn: the notify loop and session streamers may emit while SetEmitter runs.
	emitMu sync.RWMutex
	emitFn uimodel.Emitter

	// restartControlMarkerMu keeps an adoption from republishing an endpoint after shutdown removed it.
	restartControlMarkerMu       sync.Mutex
	restartControlMarkerReleased bool

	// restartControl is nil when the bind failed or startup has not run.
	restartControl *restartControlServer

	tenant *tenantsvc.Service
	// cloudContexts is the live power state; config no longer persists it.
	cloudContexts *cloudctx.Service
	settings      *settingssvc.Service
	runtime       *runtimeview.Service
	envops        *envops.Service
	terminal      *terminal.Service
	activity      *activitysvc.Service
	contribute    *contribute.Service
	orchestrator  *orchestratorsvc.Service
}

// SetEmitter overrides event emission; headless uses it for SSE.
func (a *App) SetEmitter(emit func(name string, args ...any)) {
	a.emitMu.Lock()
	a.emitFn = emit
	a.emitMu.Unlock()
}

func (a *App) emit(name string, args ...any) {
	a.emitMu.RLock()
	emitFn := a.emitFn
	a.emitMu.RUnlock()
	if emitFn != nil {
		emitFn(name, args...)
		return
	}
	if a.ctx == nil {
		return
	}
	wailsArgs := make([]interface{}, 0, len(args))
	wailsArgs = append(wailsArgs, args...)
	runtime.EventsEmit(a.ctx, name, wailsArgs...)
}

// context is the Wails context once startup has run, and context.Background()
// before it. Services built in NewApp read it through this func, never a
// captured value, because startup (or the headless path) sets a.ctx later.
func (a *App) context() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func NewApp(deps erunUIDeps) *App {
	deps = withDefaultCoreDeps(deps)
	deps = withDefaultCloudDeps(deps)
	deps = withDefaultRuntimeDeps(deps)
	deps = withDefaultUIDeps(deps)
	app := &App{deps: deps}
	app.investigations = app.newInvestigationService(nil, nil)
	app.activity = app.newActivityService()
	app.contribute = app.newContributeService()
	app.tenant = tenantsvc.New(tenantsvc.Deps{
		Store:                 deps.store,
		Cloud:                 deps.cloudDeps,
		Context:               app.context,
		MCPBearer:             app.mcpBearer,
		LoadAPILog:            deps.loadAPILog,
		ConfiguredERunAliases: func() ([]string, error) { return app.orchestrator.ConfiguredERunAliases() },
	})
	app.cloudContexts = app.newCloudContextService(time.Now)
	app.envops = app.newEnvopsService()
	app.terminal = app.newTerminalService()
	app.settings = app.newSettingsService()
	app.runtime = app.newRuntimeService()
	app.orchestrator = app.newOrchestratorService()
	if app.identity == nil {
		app.identity = newDesktopIdentity(core.DefaultDesktopIdentityDir())
	}
	return app
}

// newCloudContextService takes the cache clock so a test can age entries.
func (a *App) newCloudContextService(now func() time.Time) *cloudctx.Service {
	return cloudctx.New(cloudctx.Deps{
		Store:        a.deps.store,
		Cloud:        a.deps.cloudDeps,
		ContextDeps:  a.deps.cloudContextDeps,
		MCPBearer:    a.mcpBearer,
		MCPReachable: a.deps.canReachMCPEndpoint,
		MCPEndpoint:  mcpEndpointForOpenResult,
		MCPTransport: func(endpoint, bearer string) mcpsdk.Transport {
			return mcpClientTransport(endpoint, bearer, false)
		},
		ClientVersion:        func() string { return currentBuildInfo().Version },
		NotifyRefreshFailure: a.notifyCredentialRefreshFailure,
		OnStatusChange:       func() { a.runtime.RefreshNodeStatuses() },
		Now:                  now,
	})
}

// newSettingsService reads the deps funcs and the tenant service per call, so a
// test that builds App by hand or swaps a dep after NewApp still reaches its fake.
func (a *App) newSettingsService() *settingssvc.Service {
	return settingssvc.New(settingssvc.Deps{
		Store:         a.deps.store,
		Cloud:         a.deps.cloudDeps,
		ContextDeps:   a.deps.cloudContextDeps,
		Context:       a.context,
		Emit:          a.emit,
		CloudStatuses: a.cloudContexts,
		StopCloudContext: func(ctx context.Context, name string) (model.CloudContextStatus, error) {
			return a.deps.stopCloudContext(ctx, name)
		},
		MarkIntentionalStop:  func(name string) { a.terminal.MarkIntentionalStopForCloudContext(name) },
		ClearIntentionalStop: func(name string) { a.terminal.ClearIntentionalStopForCloudContext(name) },
		ClearIdleStops:       func(name string) { a.terminal.ClearIdleStopsForCloudContext(name) },
		RecordManualStop: func(ctx context.Context, name string) {
			a.terminal.RecordManualStopForCloudContext(ctx, name)
		},
		EnsureMCPAvailable: func(ctx context.Context, result config.OpenResult) error {
			return a.terminal.EnsureMCPAvailable(ctx, result)
		},
		MCPEndpoint: mcpEndpointForOpenResult,
		MCPBearer:   a.mcpBearer,
		SetRemoteCloudAlias: func(ctx context.Context, endpoint, bearer, tenant, environment, alias string) (config.EnvConfig, error) {
			return a.deps.setRemoteCloudAlias(ctx, endpoint, bearer, tenant, environment, alias)
		},
		ReconcileWorkspaceSync: func(selection uimodel.Selection, enabled bool) {
			a.envops.ReconcileWorkspaceSync(selection, enabled)
		},
		WorkspaceSyncStatus: func(selection uimodel.Selection) (string, string) {
			status := a.envops.WorkspaceSyncStatus(selection)
			return status.Status, status.Message
		},
		PlatformReady: func(tenant string) bool {
			resolution, err := a.tenant.Platform(tenant, "")
			return err == nil && resolution.State == tenantsvc.StateReady
		},
		PlatformClient: func(ctx context.Context, tenant string) (*platformclient.PlatformClient, context.Context, context.CancelFunc, error) {
			return a.tenant.Client(ctx, tenant)
		},
		PlatformRequestTimeout: tenantsvc.RequestTimeout,
		ReadGatewayRouting: func(ctx context.Context, env config.EnvConfig, tenant string, catalog *config.OpenRouterConfig) (gatewayrouting.GatewayRouting, bool, error) {
			return a.deps.readGatewayRouting(ctx, env, tenant, catalog)
		},
	})
}

// mcpBearer returns "" without an identity; an auth-enabled env then answers 401.
func (a *App) mcpBearer(tenant, environment string) string {
	if a.identity == nil {
		return ""
	}
	token, err := a.identity.signToken(tenant, environment, time.Now())
	if err != nil {
		log.Printf("erun-app: sign MCP bearer for %s/%s: %v", tenant, environment, err)
		return ""
	}
	return token
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// GUI launches inherit launchd's minimal env, so import an allowlist from the
	// login shell before any shell-out.
	if !a.deps.skipLoginShellEnv {
		importLoginShellEnv()
	}
	configureAppIdentity("ERun")
	observeAppActivation()
	a.startActivityPollers()
	a.cloudContexts.Start()
	a.settings.StartConfigWatcher()
	a.startRestartControl()
	go a.settings.CatchUpHostedDefinitions()
	// Off the startup path so per-env I/O does not delay first paint.
	go a.envops.ReconcileConfiguredWorkspaceSyncs()
}

func (a *App) shutdown(context.Context) {
	a.settings.StopConfigWatcher()
	a.activity.StopPollers()
	a.cloudContexts.Stop()
	a.activity.StopActionRunners()
	a.investigations.StopTimers()
	a.restartControl.Close()
	a.releaseRestartControlMarker()
	// The refreshers' and sync workers' own locks are leaves, so stop them before taking the terminal lock.
	a.cloudContexts.StopAllRefreshers()
	a.envops.StopAllWorkspaceSyncs()
	a.terminal.CloseAll()
	a.terminal.Wait()
}

func (a *App) beforeClose(ctx context.Context) bool {
	if !a.consumeCloseConfirmed() {
		if gate := a.PrepareWindowClose(); gate.Blocked {
			return true
		}
	}
	_ = saveAppWindowState(a.deps.windowStatePath, appWindowState{
		Maximised: a.deps.windowMaximised(ctx),
	})
	return false
}
