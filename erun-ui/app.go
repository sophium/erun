package main

import (
	"context"
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
	mcpReconnectLineEvent       = "mcp-reconnect-line"
	environmentInitializedEvent = "environment-initialized"
	environmentInitFailedEvent  = "environment-init-failed"
	environmentsChangedEvent    = "environments-changed"
	aiActivityEvent             = "ai-activity"
	appSessionEnvVar            = "ERUN_UI_SESSION"
)

type erunUIStore interface {
	eruncommon.ListStore
	SaveERunConfig(eruncommon.ERunConfig) error
	SaveTenantConfig(eruncommon.TenantConfig) error
	SaveEnvConfig(string, eruncommon.EnvConfig) error
}

type projectConfigLoader interface {
	LoadProjectConfig(string) (eruncommon.ProjectConfig, string, error)
}

type erunUIDeps struct {
	store                  erunUIStore
	findProjectRoot        eruncommon.ProjectFinderFunc
	resolveCLIPath         func() string
	resolveBuildInfo       func() eruncommon.BuildInfo
	resolveImageRegistry   func(context.Context, string, string) (eruncommon.RuntimeRegistryVersions, error)
	cloudDeps              eruncommon.CloudDependencies
	cloudContextDeps       eruncommon.CloudContextDependencies
	deleteNamespace        eruncommon.NamespaceDeleterFunc
	listKubeContexts       func() ([]string, error)
	loadResourceStatus     func(context.Context, uiRuntimeResourceInput) (uiRuntimeResourceStatus, error)
	ensureMCP              func(context.Context, eruncommon.OpenResult) error
	reconnectMCP           func(context.Context, eruncommon.OpenResult, func(string)) error
	ensureSSHD             func(context.Context, eruncommon.OpenResult) error
	canConnectLocalPort    func(int) bool
	setRemoteCloudAlias    func(context.Context, string, string, string, string) (eruncommon.EnvConfig, error)
	startTerminal          func(startTerminalSessionParams) (terminalSession, error)
	runIDECommand          func(context.Context, startTerminalSessionParams) (string, error)
	savePastedImage        func(pastedImageSaveParams) (string, error)
	loadDiff               func(context.Context, string, uiDiffOptions) (eruncommon.DiffResult, error)
	loadIdleStatus         func(context.Context, string) (eruncommon.EnvironmentIdleStatus, error)
	loadAPILog             func(context.Context, uiTenantDashboardInput) (string, error)
	workspaceSyncReady     func(context.Context, string) error
	syncWorkspace          func(context.Context, workspaceSyncParams) (workspaceSyncResult, error)
	workspaceSyncInterval  time.Duration
	recordActivity         func(eruncommon.EnvironmentActivityParams) error
	runWorkingIssueCommand workingIssueCommandRunner
	stopCloudContext       func(context.Context, string) (eruncommon.CloudContextStatus, error)
	windowStatePath        string
	windowMaximised        func(context.Context) bool
	cloneERun              func(context.Context, string) error
	contributeStatePath    string
}

type App struct {
	ctx  context.Context
	deps erunUIDeps

	mu                        sync.Mutex
	nextSerial                int
	sessions                  map[string]*managedTerminal
	idleStops                 map[string]struct{}
	intentionalStops          map[string]struct{}
	busyEnvs                  map[string]int
	workspaceSyncs            map[string]*workspaceSyncWorker
	credentialRefreshers      map[string]*cloudCredentialsRefresher
	activityQueue             *activityQueueStore
	activityStatusPoller      func(activityQueueEntry)
	activityPollersStop       chan struct{}
	activityWatchedContextsMu sync.Mutex
	activityWatchedContexts   map[string]struct{}
	actionQueueMu             sync.Mutex
	actionQueues              map[string]*envActionQueue
	actionCancels             map[string]context.CancelFunc
	configWatcher             *configWatcher
	contribute                *contributeStore
	contributeApps            *contributeAppForwards

	// cloudContextStatuses caches the live AWS-observed power state for
	// each cloud context, keyed by context name. Populated by the
	// background poller and by handlers that already call Refresh
	// (settings dialog, Init/Start/Stop). The persisted config no longer
	// carries Status (it is operational state, not configuration), so
	// any code path that needs "is this context running right now?" must
	// consult this map.
	cloudContextStatusesMu sync.RWMutex
	cloudContextStatuses   map[string]string
	cloudContextPollerStop chan struct{}

	// workingIssueCache memoizes the resolved working issue (branch +
	// linked issue title) per env so the sidebar hover card doesn't re-run
	// git + gh on every hover. Entries expire after workingIssueCacheTTL.
	workingIssueMu    sync.Mutex
	workingIssueCache map[string]workingIssueCacheEntry

	// emitFn dispatches Wails-style events to the frontend. In normal Wails
	// mode this calls runtime.EventsEmit; in headless mode it fans out to
	// the SSE subscribers in headlessserver. When unset it defaults to the
	// Wails runtime path during startup.
	emitFn func(name string, args ...any)
}

// SetEmitter overrides how the App emits frontend events. The headless server
// uses this to redirect EventsEmit calls to SSE subscribers instead of the
// Wails runtime.
func (a *App) SetEmitter(emit func(name string, args ...any)) {
	a.emitFn = emit
}

// emit dispatches the named event with optional payload args to whatever
// transport is currently wired up. Safe to call before startup; events emitted
// without a context or emitter configured are dropped silently, matching the
// pre-refactor behavior of runtime.EventsEmit with a nil context.
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
	deps = withDefaultRuntimeDeps(deps)
	deps = withDefaultUIDeps(deps)
	app := &App{
		deps:                 deps,
		sessions:             make(map[string]*managedTerminal),
		idleStops:            make(map[string]struct{}),
		intentionalStops:     make(map[string]struct{}),
		busyEnvs:             make(map[string]int),
		workspaceSyncs:       make(map[string]*workspaceSyncWorker),
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
	return app
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

func withDefaultRuntimeDeps(deps erunUIDeps) erunUIDeps {
	if deps.listKubeContexts == nil {
		deps.listKubeContexts = listKubernetesContexts
	}
	if deps.loadResourceStatus == nil {
		deps.loadResourceStatus = loadRuntimeResourceStatus
	}
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
	if deps.canConnectLocalPort == nil {
		deps.canConnectLocalPort = canConnectLocalTCP
	}
	if deps.setRemoteCloudAlias == nil {
		deps.setRemoteCloudAlias = setEnvironmentCloudAliasViaMCP
	}
	if deps.startTerminal == nil {
		deps.startTerminal = startTerminalSession
	}
	if deps.runIDECommand == nil {
		deps.runIDECommand = runIDECommand
	}
	return deps
}

func withDefaultUIDeps(deps erunUIDeps) erunUIDeps {
	if deps.savePastedImage == nil {
		deps.savePastedImage = savePastedImageToRuntime
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
	if deps.runWorkingIssueCommand == nil {
		deps.runWorkingIssueCommand = execWorkingIssueCommand
	}
	if deps.recordActivity == nil {
		deps.recordActivity = eruncommon.RecordEnvironmentActivity
	}
	if deps.stopCloudContext == nil {
		deps.stopCloudContext = func(_ context.Context, name string) (eruncommon.CloudContextStatus, error) {
			return eruncommon.StopCloudContext(eruncommon.Context{}, deps.store, eruncommon.CloudContextParams{Name: name}, deps.cloudContextDeps)
		}
	}
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
	return deps
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// macOS GUI launches (Finder/Dock) start with launchd's minimal env
	// — no Homebrew PATH, no KUBECONFIG, no AWS_*. Inherit a short
	// allowlist from the user's login shell so subprocess calls like
	// kubectl config get-contexts read the same state the user sees in
	// their terminal. No-op on other platforms. Runs before any other
	// startup task that shells out so the first call is already correct.
	importLoginShellEnv()
	configureAppIdentity("ERun")
	a.startActivityPollers()
	a.startCloudContextStatusPoller()
	a.startConfigWatcher()
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
