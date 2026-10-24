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
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/infra/registry"
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
	SaveTenantConfig(config.TenantConfig) error
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
	resolveImageRegistry   func(context.Context, string, string) (registry.RuntimeRegistryVersions, error)
	cloudDeps              cloud.CloudDependencies
	cloudContextDeps       cloud.CloudContextDependencies
	deleteNamespace        lifecycle.NamespaceDeleterFunc
	listKubeContexts       func() ([]string, error)
	loadResourceStatus     func(context.Context, uiRuntimeResourceInput) (uiRuntimeResourceStatus, error)
	loadClusterRegistry    func(context.Context, uiRuntimeResourceInput) (uiClusterRegistryStatus, error)
	loadHostedRegistry     func(context.Context) uiHostedRegistryStatus
	checkRuntimeDeployed   func(context.Context, string, string, string) (bool, error)
	stopEnvironmentRuntime func(core.Context, lifecycle.StopEnvironmentParams) (lifecycle.StopEnvironmentResult, error)
	readRuntimeRunState    func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error)
	ensureMCP              func(context.Context, config.OpenResult) error
	reconnectMCP           func(context.Context, config.OpenResult, func(string)) error
	ensureSSHD             func(context.Context, config.OpenResult) error
	canConnectLocalPort    func(int) bool
	// canReachMCPEndpoint answers whether an MCP port carries traffic, which a
	// dial cannot: a stale port-forward accepts and never answers, so a
	// dial-gated recovery never fires and the environment stays dead behind a
	// healthy-looking listener.
	canReachMCPEndpoint       func(int) bool
	setRemoteCloudAlias       func(context.Context, string, string, string, string, string) (config.EnvConfig, error)
	startTerminal             func(startTerminalSessionParams) (terminalSession, error)
	runIDECommand             func(context.Context, startTerminalSessionParams) (string, error)
	launchHostArtifact        func(exePath, dir string) error
	launchHostOpener          func(executable string, args []string) error
	resolveOrchestratorLaunch func(launch orchestrator.OrchestratorLaunch, agentID, initialPrompt, mcpConfigPath string) (string, []string, error)
	// claudeAgents drives the Claude background agents persisted orchestrators
	// run as, injectable so no test ever starts or stops a real one.
	claudeAgents claudeAgentCLI
	// resolveGatewayCredential reads the secret an orchestrator launched through
	// the gateway sends, injectable so a test never reads the real secret store.
	resolveGatewayCredential func(*config.OpenRouterConfig) (string, error)
	savePastedFile           func(pastedFileSaveParams) (string, error)
	listAgentOutputs         func(config.OpenResult, jobs.RuntimeOutputsParams) (jobs.RuntimeOutputsListResult, error)
	downloadAgentOutput      func(config.OpenResult, jobs.RuntimeOutputDownloadParams) (jobs.RuntimeOutputResult, error)
	loadDiff                 func(context.Context, string, string, uiDiffOptions) (lifecycle.DiffResult, error)
	execCommit               func(ctx context.Context, endpoint, bearer, branch, message string) (gitx.CommitWorkingTreeResult, error)
	execPush                 func(ctx context.Context, endpoint, bearer, branch, remote string) (gitx.PushWorkingTreeBranchResult, error)
	loadIdleStatus           func(context.Context, string, string) (activity.EnvironmentIdleStatus, error)
	loadEnvironmentJobs      func(context.Context, string, string) ([]shared.EnvironmentJob, error)
	readEnvironmentJobOutput func(context.Context, string, string, jobs.ReadEnvironmentJobOutputParams) (jobs.EnvironmentJobOutput, error)
	cancelEnvironmentJob     func(context.Context, string, string, jobs.CancelEnvironmentJobParams) (jobs.CancelEnvironmentJobResult, error)
	whipEnvironment          func(context.Context, string, string, string, string) (orchestrator.WhipResult, error)
	loadAPILog               func(context.Context, uiTenantDashboardInput) (string, error)
	workspaceSyncReady       func(context.Context, string) error
	syncWorkspace            func(context.Context, workspace.WorkspaceSyncParams) (workspace.WorkspaceSyncResult, error)
	workspaceSyncInterval    time.Duration
	recordActivity           func(activity.EnvironmentActivityParams) error
	runWorkingIssueCommand   workingIssueCommandRunner
	loadPodBranch            func(context.Context, string, string) (string, error)
	runPodRaw                func(context.Context, string, string, []string) (string, error)
	execRuntimePod           func(context.Context, uiSelection, string) (string, error)
	loadRuntimeUsage         func(context.Context, uiSelection) (uiRuntimeUsage, error)
	stopCloudContext         func(context.Context, string) (config.CloudContextStatus, error)
	windowStatePath          string
	windowMaximised          func(context.Context) bool
	// skipLoginShellEnv leaves the process environment as the caller gave it.
	// Set for --headless, whose caller always supplies its environment on
	// purpose -- the importer exists for launchd's minimal Finder/Dock env.
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
	// runOrchestratorLabelCommand runs the GitHub CLI calls that provision an
	// orchestrator's claim label. It is the shared host-command runner under a
	// name of its own, so a test that drives provisioning says which host
	// boundary it is stubbing instead of stubbing the sidebar's working-issue
	// probe by accident -- and a test that drives the sidebar does not have to
	// know a label is being minted somewhere behind it.
	runOrchestratorLabelCommand workingIssueCommandRunner
}

type App struct {
	ctx  context.Context
	deps erunUIDeps

	// identity is the desktop's persistent signing identity: it mints the per-env
	// MCP bearer and supplies the public key deploy injects so each env's edge can
	// verify those tokens. nil in unit tests.
	identity *desktopIdentity

	mu         sync.Mutex
	nextSerial int
	sessions   map[string]*managedTerminal
	// sessionWG tracks every streamSession reader goroutine (spawned via
	// spawnStreamSession) so shutdown can wait for all of them to actually
	// exit after closing their sessions, rather than assuming a closed fd
	// means the goroutine reading it is already gone.
	sessionWG        sync.WaitGroup
	idleStops        map[string]struct{}
	intentionalStops map[string]struct{}
	// runtimeStops latches a per-env `erun stop` the desktop issued. Kept
	// separate from intentionalStops (which is per cloud context) so the two
	// stops cannot alias: they have different recoveries, and a runtime stop
	// flagged as a cloud-context stop would name the wrong one.
	runtimeStops map[string]struct{}
	// sessionHeartbeats holds the most recent pod observation per environment:
	// which persistent sessions still have a live program behind them. It is
	// what keeps a quiet-but-running AI tab from reading as finished, and what
	// makes the rendered session count and the running state one observation
	// rather than two guesses. See session_heartbeat.go.
	sessionHeartbeats map[string]sessionHeartbeat
	// aiSessionReadings holds the most recent structured AI-session status per
	// environment, as the environment's own AI tool reported it. It is what
	// keeps the sidebar badge from reading a session blocked on the human as
	// working. See ai_session_status.go.
	aiSessionReadings map[string]aiSessionReading
	// envActivity is the last observation published per environment, so the
	// sweep announces transitions rather than restating a quiet environment
	// every tick. See environment_activity.go.
	envActivity map[string]environmentActivityState
	// envUsage is the last cached usage reading published per environment, kept
	// so a hover card renders a cached figure with its age rather than
	// triggering a probe of its own. See environment_usage.go.
	envUsage map[string]environmentUsageReading
	// forwardRepairs tracks, per environment, the bounded repair episode for a
	// port-forward that holds its local port while its edge answers nothing.
	// See environment_forward_repair.go.
	forwardRepairs map[string]forwardRepairEpisode
	// edgeOutages tracks, per environment, an "edge is not answering" entry
	// logged when an orchestrator wired it, until the sweep sees that edge
	// answer and logs the matching exit. See orchestrator_edge_recovery.go.
	edgeOutages    map[string]orchestratorEdgeOutage
	busyEnvs       map[string]int
	workspaceSyncs map[string]*workspaceSyncWorker
	orchestrators  map[string]*orchestratorSession
	// unmanagedPacingReason is pacingLastReason's counterpart for a configured
	// orchestrator this desktop holds no session for and therefore has no
	// orchestratorSession to keep it on: the last pacing reason logged for it,
	// so the reconciler's decision line covers the whole configured population
	// without repeating itself every 15s tick. Guarded by a.mu, like the map it
	// shadows. See orchestratorPacingUnmanagedRows.
	unmanagedPacingReason map[string]orchestratorPacingReason
	// investigations bounds how many failure reports become agents, for how
	// long, and on what input. It holds its own lock; never call into it while
	// holding a.mu, since it observes session liveness through this App.
	investigations *investigationRegistry
	// skillsSourceReported latches the one warning a run posts when the shipped
	// skills cannot be resolved. The condition is a property of this build, so
	// restating it on every orchestrator launch would be noise.
	skillsSourceReported bool
	// agentsSourceReported is skillsSourceReported's counterpart for the
	// reusable agents (erun-builder/erun-reviewer).
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
	// initEmitted dedups the environment-initialized signal per env. `erun init`
	// emits "==> Initialized" once, but on Windows a ConPTY repaint (triggered by
	// writing the follow-up deploy command into the same Local shell) re-sends the
	// buffered line as fresh output, so the trace scanner would re-fire the event —
	// each re-fire composes another deploy, whose write repaints again: an endless
	// create→deploy loop. Fire at most once per env; reset on init-failure/delete.
	initEmittedMu sync.Mutex
	initEmitted   map[string]struct{}
	configWatcher *configWatcher
	// definitionWriteOrigin is the config watcher's origin filter: the env
	// config writes this desktop made while transferring a definition, so the
	// watcher stops firing on its own write. See hosted_definition_origin.go.
	definitionWriteOrigin definitionWriteOrigin
	contribute            *contributeStore
	contributeApps        *contributeAppForwards

	// cloudContextStatuses caches the live AWS-observed power state per cloud
	// context. The persisted config no longer carries Status (it is operational
	// state, not configuration), so any code path that needs "is this context
	// running right now?" must consult this map.
	cloudContextStatusesMu sync.RWMutex
	cloudContextStatuses   map[string]cloudContextCacheEntry
	cloudContextPollerStop chan struct{}

	// envNodes is the last node reading published per environment, so the sweep
	// announces transitions rather than restating an unchanged node every tick.
	// See environment_node.go.
	envNodesMu sync.Mutex
	envNodes   map[string]uiEnvironmentNodeSnapshot

	// closeConfirmed latches the operator's explicit "close anyway" choice from
	// the running-work confirmation, so the second beforeClose pass that
	// wailsruntime.Quit triggers proceeds instead of prompting again.
	closeConfirmed bool

	// workingIssueCache memoizes the resolved working issue per env so the sidebar
	// hover card doesn't re-run git + gh on every hover.
	workingIssueMu    sync.Mutex
	workingIssueCache map[string]workingIssueCacheEntry

	// emitMu guards emitFn. Two independent reasons, both real: NewApp starts
	// the activity queue's notify loop before a caller gets a chance to call
	// SetEmitter, so emit() can race SetEmitter from that goroutine; and
	// SetEmitter can be called after other background goroutines (session
	// streamers among them) are already emitting through it.
	emitMu sync.RWMutex
	emitFn func(name string, args ...any)

	// restartControlMarkerMu guards the control record against this process's
	// own shutdown, so an adoption still waiting for a previous owner to exit
	// cannot republish an endpoint after shutdown has removed it.
	restartControlMarkerMu       sync.Mutex
	restartControlMarkerReleased bool

	// restartControl is the loopback server a CLI-triggered restart talks
	// to (see restart_control.go). nil when the bind failed or startup has not
	// run yet (unit tests that construct an App directly).
	restartControl *restartControlServer
}

// SetEmitter overrides how the App emits frontend events; the headless server
// uses it to redirect events to SSE subscribers instead of the Wails runtime.
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

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// macOS GUI launches (Finder/Dock) inherit launchd's minimal env — no
	// Homebrew PATH, no KUBECONFIG, no AWS_* — so pull a short allowlist from the
	// user's login shell, and do it first so every later shell-out sees the same
	// state the user's terminal does.
	if !a.deps.skipLoginShellEnv {
		importLoginShellEnv()
	}
	configureAppIdentity("ERun")
	observeAppActivation()
	a.startActivityPollers()
	a.startCloudContextStatusPoller()
	a.startConfigWatcher()
	a.startRestartControl()
	// Migrate whatever changed while this desktop was closed. Off the startup
	// path: it is per-environment config and network I/O, and nothing on screen
	// waits for it. See hosted_definition_upload.go.
	go a.catchUpHostedDefinitions()
	// Populate and keep live every linked orchestrator mirror, not only envs
	// opened this session. Off the startup path so config/network I/O per env
	// does not delay first paint.
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
	// Every closed session's reader goroutine takes a.mu itself (via
	// currentSessionFor) on its way out, so this must wait outside the lock
	// above or it would deadlock against them.
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
