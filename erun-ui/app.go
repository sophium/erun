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
	setRemoteCloudAlias    func(context.Context, string, string, string, string, string) (eruncommon.EnvConfig, error)
	startTerminal          func(startTerminalSessionParams) (terminalSession, error)
	runIDECommand          func(context.Context, startTerminalSessionParams) (string, error)
	savePastedFile         func(pastedFileSaveParams) (string, error)
	listAgentOutputs       func(eruncommon.OpenResult, eruncommon.RuntimeOutputsParams) (eruncommon.RuntimeOutputsListResult, error)
	downloadAgentOutput    func(eruncommon.OpenResult, eruncommon.RuntimeOutputDownloadParams) (eruncommon.RuntimeOutputResult, error)
	loadDiff               func(context.Context, string, string, uiDiffOptions) (eruncommon.DiffResult, error)
	loadIdleStatus         func(context.Context, string, string) (eruncommon.EnvironmentIdleStatus, error)
	loadAPILog             func(context.Context, uiTenantDashboardInput) (string, error)
	workspaceSyncReady     func(context.Context, string) error
	syncWorkspace          func(context.Context, workspaceSyncParams) (workspaceSyncResult, error)
	workspaceSyncInterval  time.Duration
	recordActivity         func(eruncommon.EnvironmentActivityParams) error
	runWorkingIssueCommand workingIssueCommandRunner
	loadPodBranch          func(context.Context, string, string) (string, error)
	runPodRaw              func(context.Context, string, string, []string) (string, error)
	stopCloudContext       func(context.Context, string) (eruncommon.CloudContextStatus, error)
	windowStatePath        string
	windowMaximised        func(context.Context) bool
	cloneERun              func(context.Context, string, string) error
	contributeStatePath    string
}

type App struct {
	ctx  context.Context
	deps erunUIDeps

	// identity is the desktop's persistent signing identity (issue #655). It
	// mints the short-lived per-env bearer the desktop sends to each env's MCP
	// edge and supplies the public key deploy injects so the edge verifies
	// those tokens. nil in unit tests, where mcpBearer returns "" so non-auth
	// envs and stubbed MCP deps keep working.
	identity *desktopIdentity

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
	envEnsureMu               sync.Mutex
	envEnsureInflight         map[string]struct{}
	envEnsureDone             map[string]time.Time
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
		app.identity = newDesktopIdentity(defaultDesktopIdentityDir())
	}
	return app
}

// mcpBearer mints the short-lived per-env bearer the desktop sends to the env's
// MCP edge (issue #655). A nil identity (unit tests) or a signing failure yields
// "", so non-auth envs and stubbed MCP deps keep working; an auth-enabled env
// rejects an empty bearer with 401, which is the correct outcome.
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

// withDefaultCloudDeps wires the cloud-provider dependency defaults the
// desktop needs for cloud-alias operations. erun-common fills the AWS hooks
// and VerifyCloudflareToken itself via normalizeCloudDependencies, but the
// off-config CloudSecretStore that Cloudflare init/status/export require has
// no usable zero value — it must be backed by a real directory. Wire the
// file-backed default rooted beside erun-config.yaml so Cloudflare token
// persistence works (today the desktop passes empty deps, so Cloudflare ops
// would fail with "cloud secret store is not configured"). A resolution
// failure leaves the store nil; erun-common then surfaces a clear error on
// the Cloudflare path rather than touching AWS-only flows.
func withDefaultCloudDeps(deps erunUIDeps) erunUIDeps {
	if deps.cloudDeps.CloudSecretStore == nil {
		if store, err := eruncommon.DefaultCloudSecretStore(); err == nil {
			deps.cloudDeps.CloudSecretStore = store
		}
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
	deps = withDefaultWorkspaceDeps(deps)
	deps = withDefaultPodDeps(deps)
	deps = withDefaultWindowAndContributeDeps(deps)
	return deps
}

// withDefaultWorkspaceDeps wires the read-model and workspace-sync defaults:
// pasted-image save, diff/idle/API-log loaders, and the workspace sync
// readiness check, runner, and interval.
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

// withDefaultPodDeps wires the pod-facing defaults: working-issue command
// runner, pod branch and raw-command loaders, activity recording, and the
// cloud-context stop adapter.
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

// withDefaultWindowAndContributeDeps wires the window-state and contribute
// defaults: window state path, maximised probe, ERun clone runner, and
// contribute state path.
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
