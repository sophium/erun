package main

import (
	"context"
	"log"
	"sync"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	terminalOutputEvent         = "terminal-output"
	terminalExitEvent           = "terminal-exit"
	appStatusEvent              = "app-status"
	appNotificationEvent        = "app-notification"
	appNotificationClearEvent   = "app-notification-clear"
	mcpReconnectLineEvent       = "mcp-reconnect-line"
	environmentInitializedEvent = "environment-initialized"
	environmentInitFailedEvent  = "environment-init-failed"
	environmentDeployedEvent    = "environment-deployed"
	environmentsChangedEvent    = "environments-changed"
	aiActivityEvent             = "ai-activity"
	envStatusEvent              = "env-status"
	appSessionEnvVar            = "ERUN_UI_SESSION"
)

type erunUIStore interface {
	eruncommon.ListStore
	SaveERunConfig(eruncommon.ERunConfig) error
	SaveTenantConfig(eruncommon.TenantConfig) error
	SaveEnvConfig(string, eruncommon.EnvConfig) error
}

type projectConfigStore interface {
	LoadProjectConfig(string) (eruncommon.ProjectConfig, string, error)
	SaveProjectConfig(string, eruncommon.ProjectConfig) error
}

type erunUIDeps struct {
	store                     erunUIStore
	findProjectRoot           eruncommon.ProjectFinderFunc
	resolveCLIPath            func() string
	resolveBuildInfo          func() eruncommon.BuildInfo
	resolveImageRegistry      func(context.Context, string, string) (eruncommon.RuntimeRegistryVersions, error)
	cloudDeps                 eruncommon.CloudDependencies
	cloudContextDeps          eruncommon.CloudContextDependencies
	deleteNamespace           eruncommon.NamespaceDeleterFunc
	listKubeContexts          func() ([]string, error)
	loadResourceStatus        func(context.Context, uiRuntimeResourceInput) (uiRuntimeResourceStatus, error)
	loadClusterRegistry       func(context.Context, uiRuntimeResourceInput) (uiClusterRegistryStatus, error)
	checkRuntimeDeployed      func(context.Context, string, string, string) (bool, error)
	ensureMCP                 func(context.Context, eruncommon.OpenResult) error
	reconnectMCP              func(context.Context, eruncommon.OpenResult, func(string)) error
	ensureSSHD                func(context.Context, eruncommon.OpenResult) error
	canConnectLocalPort       func(int) bool
	setRemoteCloudAlias       func(context.Context, string, string, string, string, string) (eruncommon.EnvConfig, error)
	startTerminal             func(startTerminalSessionParams) (terminalSession, error)
	runIDECommand             func(context.Context, startTerminalSessionParams) (string, error)
	launchHostArtifact        func(exePath, dir string) error
	resolveOrchestratorLaunch func(sessionID, initialPrompt, resumePrompt, mcpConfigPath string) (string, []string, error)
	savePastedFile            func(pastedFileSaveParams) (string, error)
	listAgentOutputs          func(eruncommon.OpenResult, eruncommon.RuntimeOutputsParams) (eruncommon.RuntimeOutputsListResult, error)
	downloadAgentOutput       func(eruncommon.OpenResult, eruncommon.RuntimeOutputDownloadParams) (eruncommon.RuntimeOutputResult, error)
	loadDiff                  func(context.Context, string, string, uiDiffOptions) (eruncommon.DiffResult, error)
	loadIdleStatus            func(context.Context, string, string) (eruncommon.EnvironmentIdleStatus, error)
	loadAPILog                func(context.Context, uiTenantDashboardInput) (string, error)
	workspaceSyncReady        func(context.Context, string) error
	syncWorkspace             func(context.Context, workspaceSyncParams) (workspaceSyncResult, error)
	workspaceSyncInterval     time.Duration
	recordActivity            func(eruncommon.EnvironmentActivityParams) error
	runWorkingIssueCommand    workingIssueCommandRunner
	loadPodBranch             func(context.Context, string, string) (string, error)
	runPodRaw                 func(context.Context, string, string, []string) (string, error)
	stopCloudContext          func(context.Context, string) (eruncommon.CloudContextStatus, error)
	windowStatePath           string
	windowMaximised           func(context.Context) bool
	cloneERun                 func(context.Context, string, string) error
	contributeStatePath       string
	orchestratorRestorePath   string
	relaunchApp               func() error
	quitApp                   func()
}

type App struct {
	ctx  context.Context
	deps erunUIDeps

	// identity is the desktop's persistent signing identity: it mints the per-env
	// MCP bearer and supplies the public key deploy injects so each env's edge can
	// verify those tokens. nil in unit tests.
	identity *desktopIdentity

	mu                        sync.Mutex
	nextSerial                int
	sessions                  map[string]*managedTerminal
	idleStops                 map[string]struct{}
	intentionalStops          map[string]struct{}
	busyEnvs                  map[string]int
	workspaceSyncs            map[string]*workspaceSyncWorker
	orchestrators             map[string]*orchestratorSession
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
	envEnsureInflight         map[string]struct{}
	envEnsureDone             map[string]time.Time
	envEnsureFailNotified     map[string]struct{}
	// initEmitted dedups the environment-initialized signal per env. `erun init`
	// emits "==> Initialized" once, but on Windows a ConPTY repaint (triggered by
	// writing the follow-up deploy command into the same Local shell) re-sends the
	// buffered line as fresh output, so the trace scanner would re-fire the event —
	// each re-fire composes another deploy, whose write repaints again: an endless
	// create→deploy loop. Fire at most once per env; reset on init-failure/delete.
	initEmittedMu  sync.Mutex
	initEmitted    map[string]struct{}
	configWatcher  *configWatcher
	contribute     *contributeStore
	contributeApps *contributeAppForwards

	// cloudContextStatuses caches the live AWS-observed power state per cloud
	// context. The persisted config no longer carries Status (it is operational
	// state, not configuration), so any code path that needs "is this context
	// running right now?" must consult this map.
	cloudContextStatusesMu sync.RWMutex
	cloudContextStatuses   map[string]string
	cloudContextPollerStop chan struct{}

	// workingIssueCache memoizes the resolved working issue per env so the sidebar
	// hover card doesn't re-run git + gh on every hover.
	workingIssueMu    sync.Mutex
	workingIssueCache map[string]workingIssueCacheEntry

	emitFn func(name string, args ...any)
}

// SetEmitter overrides how the App emits frontend events; the headless server
// uses it to redirect events to SSE subscribers instead of the Wails runtime.
func (a *App) SetEmitter(emit func(name string, args ...any)) {
	a.emitFn = emit
}

func (a *App) emit(name string, args ...any) {
	if a.emitFn != nil {
		a.emitFn(name, args...)
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
		deps:                 deps,
		sessions:             make(map[string]*managedTerminal),
		idleStops:            make(map[string]struct{}),
		intentionalStops:     make(map[string]struct{}),
		busyEnvs:             make(map[string]int),
		workspaceSyncs:       make(map[string]*workspaceSyncWorker),
		orchestrators:        make(map[string]*orchestratorSession),
		credentialRefreshers: make(map[string]*cloudCredentialsRefresher),
		workingIssueCache:    make(map[string]workingIssueCacheEntry),
	}
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
		app.identity = newDesktopIdentity(eruncommon.DefaultDesktopIdentityDir())
	}
	return app
}

// mcpBearer returns "" on a nil identity (unit tests) or a signing failure; an
// auth-enabled env then rejects the empty bearer with 401, which is correct.
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

func withDefaultCoreDeps(deps erunUIDeps) erunUIDeps {
	if deps.store == nil {
		deps.store = eruncommon.ConfigStore{}
	}
	if deps.findProjectRoot == nil {
		deps.findProjectRoot = eruncommon.FindProjectRoot
	}
	if deps.resolveCLIPath == nil {
		deps.resolveCLIPath = resolveCLIExecutable
	}
	if deps.resolveBuildInfo == nil {
		deps.resolveBuildInfo = func() eruncommon.BuildInfo {
			return resolveCurrentBuildInfo(deps.resolveCLIPath)
		}
	}
	if deps.resolveImageRegistry == nil {
		deps.resolveImageRegistry = eruncommon.ResolveRuntimeImageRegistryVersions
	}
	if deps.deleteNamespace == nil {
		deps.deleteNamespace = eruncommon.DeleteKubernetesNamespace
	}
	return deps
}

// withDefaultCloudDeps supplies the one cloud default erun-common cannot fill
// itself: CloudSecretStore has no usable zero value (it needs a real backing
// directory), so Cloudflare token operations fail unless it is wired here.
func withDefaultCloudDeps(deps erunUIDeps) erunUIDeps {
	if deps.cloudDeps.CloudSecretStore == nil {
		if store, err := eruncommon.DefaultCloudSecretStore(); err == nil {
			deps.cloudDeps.CloudSecretStore = store
		}
	}
	return deps
}

func withDefaultRuntimeDeps(deps erunUIDeps) erunUIDeps {
	deps = withDefaultRuntimeResolutionDeps(deps)
	deps = withDefaultRuntimeSessionDeps(deps)
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
	if deps.checkRuntimeDeployed == nil {
		deps.checkRuntimeDeployed = checkRuntimeDeployed
	}
	if deps.canConnectLocalPort == nil {
		deps.canConnectLocalPort = canConnectLocalTCP
	}
	if deps.setRemoteCloudAlias == nil {
		deps.setRemoteCloudAlias = setEnvironmentCloudAliasViaMCP
	}
	if deps.resolveOrchestratorLaunch == nil {
		deps.resolveOrchestratorLaunch = orchestratorLaunchCommand
	}
	return deps
}

func withDefaultRuntimeSessionDeps(deps erunUIDeps) erunUIDeps {
	if deps.ensureMCP == nil {
		deps.ensureMCP = func(ctx context.Context, result eruncommon.OpenResult) error {
			return ensureMCPViaOpenCommand(ctx, deps.resolveCLIPath(), result)
		}
	}
	if deps.reconnectMCP == nil {
		deps.reconnectMCP = func(ctx context.Context, result eruncommon.OpenResult, onLine func(string)) error {
			return runOpenForReconnect(ctx, deps.resolveCLIPath(), result, onLine)
		}
	}
	if deps.ensureSSHD == nil {
		deps.ensureSSHD = func(ctx context.Context, result eruncommon.OpenResult) error {
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
	return deps
}

func withDefaultUIDeps(deps erunUIDeps) erunUIDeps {
	deps = withDefaultWorkspaceDeps(deps)
	deps = withDefaultPodDeps(deps)
	deps = withDefaultWindowAndContributeDeps(deps)
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
	if deps.loadIdleStatus == nil {
		deps.loadIdleStatus = loadIdleStatusFromMCP
	}
	if deps.loadAPILog == nil {
		deps.loadAPILog = loadAPILog
	}
	if deps.workspaceSyncReady == nil {
		deps.workspaceSyncReady = workspaceSyncSSHReady
	}
	if deps.syncWorkspace == nil {
		deps.syncWorkspace = syncWorkspaceOnce
	}
	if deps.workspaceSyncInterval <= 0 {
		deps.workspaceSyncInterval = defaultWorkspaceSyncInterval
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
	if deps.recordActivity == nil {
		deps.recordActivity = eruncommon.RecordEnvironmentActivity
	}
	if deps.stopCloudContext == nil {
		deps.stopCloudContext = func(_ context.Context, name string) (eruncommon.CloudContextStatus, error) {
			return eruncommon.StopCloudContext(eruncommon.Context{}, deps.store, eruncommon.CloudContextParams{Name: name}, deps.cloudContextDeps)
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
	if deps.orchestratorRestorePath == "" {
		deps.orchestratorRestorePath = defaultOrchestratorRestorePath()
	}
	return deps
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// macOS GUI launches (Finder/Dock) inherit launchd's minimal env — no
	// Homebrew PATH, no KUBECONFIG, no AWS_* — so pull a short allowlist from the
	// user's login shell, and do it first so every later shell-out sees the same
	// state the user's terminal does.
	importLoginShellEnv()
	configureAppIdentity("ERun")
	a.startActivityPollers()
	a.startCloudContextStatusPoller()
	a.startConfigWatcher()
	// Populate and keep live every linked orchestrator mirror, not only envs
	// opened this session. Off the startup path so config/network I/O per env
	// does not delay first paint.
	go a.startWorkspaceSyncForConfiguredEnvs()
}

func (a *App) shutdown(context.Context) {
	a.stopConfigWatcher()
	a.stopActivityPollers()
	a.stopCloudContextStatusPoller()
	a.stopActionRunners()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopAllWorkspaceSyncsLocked()
	a.stopAllCloudCredentialsRefreshersLocked()
	a.closeAllSessionsLocked()
}

func (a *App) beforeClose(ctx context.Context) bool {
	_ = saveAppWindowState(a.deps.windowStatePath, appWindowState{
		Maximised: a.deps.windowMaximised(ctx),
	})
	return false
}
