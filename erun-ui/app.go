package main

import (
	"context"
	"sync"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	terminalOutputEvent = "terminal-output"
	terminalExitEvent   = "terminal-exit"
	appStatusEvent      = "app-status"
	appSessionEnvVar    = "ERUN_UI_SESSION"
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
	store                erunUIStore
	findProjectRoot      eruncommon.ProjectFinderFunc
	resolveCLIPath       func() string
	resolveBuildInfo     func() eruncommon.BuildInfo
	resolveImageRegistry func(context.Context, string, string) (eruncommon.RuntimeRegistryVersions, error)
	cloudDeps            eruncommon.CloudDependencies
	cloudContextDeps     eruncommon.CloudContextDependencies
	deleteNamespace      eruncommon.NamespaceDeleterFunc
	listKubeContexts     func() ([]string, error)
	loadResourceStatus   func(context.Context, uiRuntimeResourceInput) (uiRuntimeResourceStatus, error)
	ensureMCP            func(context.Context, eruncommon.OpenResult) error
	canConnectLocalPort  func(int) bool
	setRemoteCloudAlias  func(context.Context, string, string, string, string) (eruncommon.EnvConfig, error)
	startTerminal        func(startTerminalSessionParams) (terminalSession, error)
	runIDECommand        func(context.Context, startTerminalSessionParams) (string, error)
	savePastedImage      func(pastedImageSaveParams) (string, error)
	loadDiff             func(context.Context, string, uiDiffOptions) (eruncommon.DiffResult, error)
	loadIdleStatus       func(context.Context, string) (eruncommon.EnvironmentIdleStatus, error)
	loadAPILog           func(context.Context, uiTenantDashboardInput) (string, error)
	recordActivity       func(eruncommon.EnvironmentActivityParams) error
	stopCloudContext     func(context.Context, string) (eruncommon.CloudContextStatus, error)
	windowStatePath      string
	windowMaximised      func(context.Context) bool
}

type App struct {
	ctx  context.Context
	deps erunUIDeps

	mu         sync.Mutex
	current    *managedTerminal
	nextSerial int
	sessions   map[string]*managedTerminal
	idleStops  map[string]struct{}
	busyEnvs   map[string]int
}

func NewApp(deps erunUIDeps) *App {
	deps = withDefaultCoreDeps(deps)
	deps = withDefaultRuntimeDeps(deps)
	deps = withDefaultUIDeps(deps)
	return &App{
		deps:      deps,
		sessions:  make(map[string]*managedTerminal),
		idleStops: make(map[string]struct{}),
		busyEnvs:  make(map[string]int),
	}
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
	return deps
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	configureAppIdentity("ERun")
}

func (a *App) shutdown(context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeAllSessionsLocked()
}

func (a *App) beforeClose(ctx context.Context) bool {
	_ = saveAppWindowState(a.deps.windowStatePath, appWindowState{
		Maximised: a.deps.windowMaximised(ctx),
	})
	return false
}
