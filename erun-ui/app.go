package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/shared"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	terminalOutputEvent           = "terminal-output"
	terminalExitEvent             = "terminal-exit"
	appStatusEvent                = "app-status"
	appNotificationEvent          = "app-notification"
	appNotificationClearEvent     = "app-notification-clear"
	mcpReconnectLineEvent         = "mcp-reconnect-line"
	environmentInitializedEvent   = "environment-initialized"
	environmentInitFailedEvent    = "environment-init-failed"
	environmentDeployedEvent      = "environment-deployed"
	environmentsChangedEvent      = "environments-changed"
	hostedDefinitionUploadedEvent = "hosted-definition-uploaded"
	doctorCompletedEvent          = "doctor-completed"
	sshdInitCompletedEvent        = "sshd-init-completed"
	aiActivityEvent               = "ai-activity"
	orchestratorShellEvent        = "orchestrator-shell-activity"
	envStatusEvent                = "env-status"
	envActivityEvent              = "env-activity"
	envUsageEvent                 = "env-usage"
	envNodeEvent                  = "env-node"
	appCloseGateEvent             = "app-close-gate"
	appSessionEnvVar              = orchestrator.DesktopSessionEnvVar
)

type erunUIStore interface {
	lifecycle.ListStore
	SaveERunConfig(config.ERunConfig) error
	SaveTenantConfig(model.TenantConfig) error
	SaveEnvConfig(string, config.EnvConfig) error
}

type projectConfigStore interface {
	LoadProjectConfig(string) (config.ProjectConfig, string, error)
	SaveProjectConfig(string, config.ProjectConfig) error
}

type erunUIDeps struct {
	store                  erunUIStore
	findProjectRoot        shared.ProjectFinderFunc
	resolveCLIPath         func() string
	resolveBuildInfo       func() model.BuildInfo
	resolveImageRegistry   func(context.Context, string, string) (model.RuntimeRegistryVersions, error)
	cloudDeps              cloud.CloudDependencies
	cloudContextDeps       cloud.CloudContextDependencies
	deleteNamespace        lifecycle.NamespaceDeleterFunc
	listKubeContexts       func() ([]string, error)
	loadResourceStatus     func(context.Context, uiRuntimeResourceInput) (uiRuntimeResourceStatus, error)
	loadClusterRegistry    func(context.Context, uiRuntimeResourceInput) (uiClusterRegistryStatus, error)
	loadHostedRegistry     func(context.Context) uiHostedRegistryStatus
	checkRuntimeDeployed   func(context.Context, string, string, string) (bool, error)
	stopEnvironmentRuntime func(core.Context, lifecycle.StopEnvironmentParams) (model.StopEnvironmentResult, error)
	readRuntimeRunState    func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error)
	ensureMCP              func(context.Context, config.OpenResult) error
	reconnectMCP           func(context.Context, config.OpenResult, func(string)) error
	ensureSSHD             func(context.Context, config.OpenResult) error
	canConnectLocalPort    func(int) bool
	// canReachMCPEndpoint checks traffic, not a dial: a stale port-forward accepts and never answers.
	canReachMCPEndpoint       func(int) bool
	setRemoteCloudAlias       func(context.Context, string, string, string, string, string) (config.EnvConfig, error)
	startTerminal             func(startTerminalSessionParams) (terminalSession, error)
	runIDECommand             func(context.Context, startTerminalSessionParams) (string, error)
	launchHostArtifact        func(exePath, dir string) error
	launchHostOpener          func(executable string, args []string) error
	resolveOrchestratorLaunch func(launch orchestrator.OrchestratorLaunch, agentID, initialPrompt, mcpConfigPath string) (string, []string, error)
	claudeAgents              claudeAgentCLI
	resolveGatewayCredential  func(*config.OpenRouterConfig) (string, error)
	savePastedFile            func(pastedFileSaveParams) (string, error)
	listAgentOutputs          func(config.OpenResult, model.RuntimeOutputsParams) (model.RuntimeOutputsListResult, error)
	downloadAgentOutput       func(config.OpenResult, model.RuntimeOutputDownloadParams) (model.RuntimeOutputResult, error)
	loadDiff                  func(context.Context, string, string, uiDiffOptions) (model.DiffResult, error)
	execCommit                func(ctx context.Context, endpoint, bearer, branch, message string) (model.CommitWorkingTreeResult, error)
	execPush                  func(ctx context.Context, endpoint, bearer, branch, remote string) (model.PushWorkingTreeBranchResult, error)
	loadIdleStatus            func(context.Context, string, string) (model.EnvironmentIdleStatus, error)
	loadEnvironmentJobs       func(context.Context, string, string) ([]shared.EnvironmentJob, error)
	readEnvironmentJobOutput  func(context.Context, string, string, model.ReadEnvironmentJobOutputParams) (jobs.EnvironmentJobOutput, error)
	cancelEnvironmentJob      func(context.Context, string, string, model.CancelEnvironmentJobParams) (jobs.CancelEnvironmentJobResult, error)
	whipEnvironment           func(context.Context, string, string, string, string) (model.WhipResult, error)
	loadAPILog                func(context.Context, uiTenantDashboardInput) (string, error)
	workspaceSyncReady        func(context.Context, string) error
	syncWorkspace             func(context.Context, workspace.WorkspaceSyncParams) (model.WorkspaceSyncResult, error)
	workspaceSyncInterval     time.Duration
	recordActivity            func(model.EnvironmentActivityParams) error
	runWorkingIssueCommand    workingIssueCommandRunner
	loadPodBranch             func(context.Context, string, string) (string, error)
	runPodRaw                 func(context.Context, string, string, []string) (string, error)
	execRuntimePod            func(context.Context, uiSelection, string) (string, error)
	loadRuntimeUsage          func(context.Context, uiSelection) (uiRuntimeUsage, error)
	stopCloudContext          func(context.Context, string) (model.CloudContextStatus, error)
	windowStatePath           string
	windowMaximised           func(context.Context) bool
	// skipLoginShellEnv is set for --headless, whose caller supplies its environment deliberately.
	skipLoginShellEnv            bool
	cloneERun                    func(context.Context, string, string) error
	contributeStatePath          string
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

	mu         sync.Mutex
	nextSerial int
	sessions   map[string]*managedTerminal
	// sessionWG lets shutdown wait for every stream reader goroutine to exit.
	sessionWG        sync.WaitGroup
	idleStops        map[string]struct{}
	intentionalStops map[string]struct{}
	// runtimeStops is separate from intentionalStops because their recoveries differ.
	runtimeStops map[string]struct{}
	// sessionHeartbeats is the latest pod observation of live sessions per environment.
	sessionHeartbeats map[string]sessionHeartbeat
	aiSessionReadings map[string]aiSessionReading
	envActivity       map[string]environmentActivityState
	envUsage          map[string]environmentUsageReading
	forwardRepairs    map[string]forwardRepairEpisode
	edgeOutages       map[string]orchestratorEdgeOutage
	busyEnvs          map[string]int
	workspaceSyncs    map[string]*workspaceSyncWorker
	orchestrators     map[string]*orchestratorSession
	// unmanagedPacingReason dedups pacing log lines for orchestrators without a session. Guarded by a.mu.
	unmanagedPacingReason map[string]orchestratorPacingReason
	// investigations has its own lock; never call into it while holding a.mu.
	investigations *investigationRegistry
	// skillsSourceReported warns once per run about unresolvable shipped skills.
	skillsSourceReported      bool
	agentsSourceReported      bool
	credentialRefreshers      map[string]*cloudCredentialsRefresher
	activityQueue             *activityQueueStore
	activityStatusPoller      func(activityQueueEntry)
	activityPollersStop       chan struct{}
	activityWatchedContextsMu sync.Mutex
	activityWatchedContexts   map[string]struct{}
	actionQueueMu             sync.Mutex
	actionQueues              map[string]*envActionQueue
	actionCancels             map[string]context.CancelFunc
	envEnsureMu               sync.Mutex
	envEnsureInflight         map[string]*envEnsureRun
	envEnsureDone             map[string]time.Time
	envEnsureFailNotified     map[string]struct{}
	// initEmitted fires environment-initialized once per env: a Windows ConPTY repaint
	// re-sends "==> Initialized", which would loop create→deploy.
	initEmittedMu sync.Mutex
	initEmitted   map[string]struct{}
	configWatcher *configWatcher
	// definitionWriteOrigin keeps the config watcher from firing on this desktop's own writes.
	definitionWriteOrigin definitionWriteOrigin
	contribute            *contributeStore
	contributeApps        *contributeAppForwards

	// cloudContextStatuses is the live power state; config no longer persists it.
	cloudContextStatusesMu sync.RWMutex
	cloudContextStatuses   map[string]cloudContextCacheEntry
	cloudContextPollerStop chan struct{}

	envNodesMu sync.Mutex
	envNodes   map[string]uiEnvironmentNodeSnapshot

	// closeConfirmed lets the second beforeClose pass from wailsruntime.Quit proceed without prompting.
	closeConfirmed bool

	workingIssueMu    sync.Mutex
	workingIssueCache map[string]workingIssueCacheEntry

	// emitMu guards emitFn: the notify loop and session streamers may emit while SetEmitter runs.
	emitMu sync.RWMutex
	emitFn func(name string, args ...any)

	// restartControlMarkerMu keeps an adoption from republishing an endpoint after shutdown removed it.
	restartControlMarkerMu       sync.Mutex
	restartControlMarkerReleased bool

	// restartControl is nil when the bind failed or startup has not run.
	restartControl *restartControlServer
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

func NewApp(deps erunUIDeps) *App {
	deps = withDefaultCoreDeps(deps)
	deps = withDefaultCloudDeps(deps)
	deps = withDefaultRuntimeDeps(deps)
	deps = withDefaultUIDeps(deps)
	app := &App{
		deps:                  deps,
		sessions:              make(map[string]*managedTerminal),
		idleStops:             make(map[string]struct{}),
		intentionalStops:      make(map[string]struct{}),
		runtimeStops:          make(map[string]struct{}),
		sessionHeartbeats:     make(map[string]sessionHeartbeat),
		busyEnvs:              make(map[string]int),
		workspaceSyncs:        make(map[string]*workspaceSyncWorker),
		orchestrators:         make(map[string]*orchestratorSession),
		unmanagedPacingReason: make(map[string]orchestratorPacingReason),
		credentialRefreshers:  make(map[string]*cloudCredentialsRefresher),
		workingIssueCache:     make(map[string]workingIssueCacheEntry),
		envUsage:              loadPersistedEnvironmentUsage(deps.environmentUsageHistoryPath),
	}
	app.investigations = newInvestigationRegistry(defaultInvestigationReportDir())
	app.investigations.live = func(id string) bool {
		_, running := app.runningOrchestratorInfo(id)
		return running
	}
	app.investigations.onExpire = app.finishExpiredInvestigation
	app.activityQueue = newActivityQueueStore(
		func(entry activityQueueEntry) {
			app.emitActivityState(entry)
		},
		nil,
	)
	app.activityStatusPoller = func(entry activityQueueEntry) {
		go app.pollActivityContainerStatuses(context.Background(), entry)
	}
	app.contribute = newContributeStore(deps.contributeStatePath)
	if app.identity == nil {
		app.identity = newDesktopIdentity(core.DefaultDesktopIdentityDir())
	}
	return app
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
	a.startCloudContextStatusPoller()
	a.startConfigWatcher()
	a.startRestartControl()
	go a.catchUpHostedDefinitions()
	// Off the startup path so per-env I/O does not delay first paint.
	go a.reconcileWorkspaceSyncForConfiguredEnvs()
}

func (a *App) shutdown(context.Context) {
	a.stopConfigWatcher()
	a.stopActivityPollers()
	a.stopCloudContextStatusPoller()
	a.stopActionRunners()
	a.investigations.stopTimers()
	a.restartControl.Close()
	a.releaseRestartControlMarker()
	a.mu.Lock()
	a.stopAllWorkspaceSyncsLocked()
	a.stopAllCloudCredentialsRefreshersLocked()
	a.closeAllSessionsLocked()
	a.mu.Unlock()
	// Readers take a.mu on exit, so wait outside the lock.
	a.sessionWG.Wait()
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
