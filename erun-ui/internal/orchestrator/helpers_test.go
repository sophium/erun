package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	orchcmd "github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func init() {
	// Keep agent settings, hook reports and the shared workspace out of the
	// developer's real HOME.
	home, err := os.MkdirTemp("", "erun-ui-orchestrator-test-home-")
	if err != nil {
		panic(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "XDG_CONFIG_HOME"} {
		_ = os.Setenv(key, filepath.Join(home, key))
	}
}

// testDeps are the desktop seams a test wires, named as the desktop names
// them. newTestApp reads them per call, so a test may swap one after building.
type testDeps struct {
	store                        Store
	startTerminal                func(StartParams) (PTY, error)
	resolveOrchestratorLaunch    func(orchcmd.OrchestratorLaunch, string, string, string) (string, []string, error)
	canReachMCPEndpoint          func(int) bool
	mcpForwardStale              func(int) bool
	runOrchestratorLabelCommand  func(context.Context, string, string, ...string) (string, error)
	resolveGatewayCredential     func(*configpkg.OpenRouterConfig) (string, error)
	claudeAgents                 AgentCLI
	openIDE                      func(ctx context.Context, dir, ide, path string) error
	whipEnvironment              func(context.Context, string, string, string, string) (model.WhipResult, error)
	launchHostArtifact           func(exePath, dir string) error
	ensureRuntimeOnce            func(uimodel.Selection)
	repairEdge                   func(uimodel.Selection, time.Duration)
	orchestratorOpenPath         string
	orchestratorNudgeHistoryPath string
}

// testApp is the orchestrator service with the desktop around it faked: the
// terminal registry, the runtime readings and the emitter.
type testApp struct {
	*Service
	deps      *testDeps
	terminals *fakeTerminals
	runtime   *fakeRuntime
	// ctx is the Wails context; nil until a test marks startup as run.
	ctx    context.Context
	emitMu sync.RWMutex
	emitFn uimodel.Emitter
}

// newTestApp fills every seam deps leaves unset with an inert fake, so no test
// reaches a process, a cluster or a pod it did not wire.
func newTestApp(deps testDeps) *testApp {
	deps = withTestDefaults(deps)
	app := &testApp{deps: &deps, terminals: newFakeTerminals(), runtime: &fakeRuntime{}}
	d := Deps{
		Store:        liveStore{deps: app.deps},
		Context:      app.context,
		Started:      func() bool { return app.ctx != nil },
		Emit:         app.emit,
		Terminals:    app.terminals,
		ClaudeAgents: func() AgentCLI { return app.deps.claudeAgents },
		MCPEndpoint: func(result configpkg.OpenResult) string {
			return fmt.Sprintf("http://127.0.0.1:%d/mcp", configpkg.MCPPortForResult(result))
		},
		MCPBearer:        func(string, string) string { return "" },
		ActivitySnapshot: app.runtime.activitySnapshot,
		UsageSnapshot:    app.runtime.usageSnapshot,
		EmitAIActivity: func(serial int, busy bool) {
			app.emit(uimodel.AIActivityEvent, aiActivityPayload{SessionID: serial, Busy: busy})
		},
		SkillsSource:     func() string { return stampedSkillsSource },
		AgentsSource:     func() string { return stampedAgentsSource },
		OpenPath:         func() string { return app.deps.orchestratorOpenPath },
		NudgeHistoryPath: func() string { return app.deps.orchestratorNudgeHistoryPath },
	}
	app.wireProcessSeams(&d)
	app.wireEnvironmentSeams(&d)
	app.Service = New(d)
	return app
}

func withTestDefaults(deps testDeps) testDeps {
	if deps.store == nil {
		deps.store = stubUIStore{config: &configpkg.ERunConfig{}}
	}
	if deps.claudeAgents == nil {
		deps.claudeAgents = newFakeClaudeAgents()
	}
	// The desktop defaults these under the config dir, which init confines to a temp home.
	if deps.orchestratorOpenPath == "" {
		deps.orchestratorOpenPath = DefaultOpenPath()
	}
	if deps.orchestratorNudgeHistoryPath == "" {
		deps.orchestratorNudgeHistoryPath = DefaultNudgeHistoryPath()
	}
	return deps
}

// wireProcessSeams reaches every process an orchestrator runs only through the test's deps.
func (a *testApp) wireProcessSeams(d *Deps) {
	d.ResolveLaunch = func(launch orchcmd.OrchestratorLaunch, agentID, initialPrompt, mcpConfigPath string) (string, []string, error) {
		if a.deps.resolveOrchestratorLaunch == nil {
			return "", nil, errors.New("the orchestrator launch is not wired in this test")
		}
		return a.deps.resolveOrchestratorLaunch(launch, agentID, initialPrompt, mcpConfigPath)
	}
	d.StartTerminal = func(params StartParams) (PTY, error) {
		if a.deps.startTerminal == nil {
			return nil, errors.New("terminals are not wired in this test")
		}
		return a.deps.startTerminal(params)
	}
	d.OpenIDE = func(ctx context.Context, dir, ide, path string) error {
		if a.deps.openIDE == nil {
			return errors.New("IDE launch is not wired in this test")
		}
		return a.deps.openIDE(ctx, dir, ide, path)
	}
	d.ResolveGatewayCredential = func(catalog *configpkg.OpenRouterConfig) (string, error) {
		if a.deps.resolveGatewayCredential == nil {
			return "", errors.New("no gateway credential staged by this test")
		}
		return a.deps.resolveGatewayCredential(catalog)
	}
	d.ResolveOpenRouterConfig = func() *configpkg.OpenRouterConfig {
		catalog, _ := configpkg.ResolveOpenRouterConfig(a.deps.store)
		return catalog
	}
	d.RunLabelCommand = func(ctx context.Context, dir, name string, args ...string) (string, error) {
		if a.deps.runOrchestratorLabelCommand == nil {
			return "", errors.New("gh is not wired in this test")
		}
		return a.deps.runOrchestratorLabelCommand(ctx, dir, name, args...)
	}
	d.LaunchHostArtifact = func(exePath, dir string) error {
		if a.deps.launchHostArtifact == nil {
			return errors.New("host launch is not wired in this test")
		}
		return a.deps.launchHostArtifact(exePath, dir)
	}
	d.ReportHostArtifactSigning = func(model.HostArtifactSigning) {}
}

// wireEnvironmentSeams answers for the linked environments' edges: a missing
// probe reads as answering, as the desktop wires it.
func (a *testApp) wireEnvironmentSeams(d *Deps) {
	d.CanReachMCPEndpoint = func(port int) bool {
		return a.deps.canReachMCPEndpoint == nil || a.deps.canReachMCPEndpoint(port)
	}
	d.MCPForwardStale = func(port int) bool {
		return a.deps.mcpForwardStale != nil && a.deps.mcpForwardStale(port)
	}
	d.MCPEdgeAnswers = d.CanReachMCPEndpoint
	d.WhipEnvironment = func(ctx context.Context, tenant, environment, endpoint, bearer string) (model.WhipResult, error) {
		if a.deps.whipEnvironment == nil {
			return model.WhipResult{}, errors.New("the environment whip is not wired in this test")
		}
		return a.deps.whipEnvironment(ctx, tenant, environment, endpoint, bearer)
	}
	d.EnsureRuntimeOnce = func(selection uimodel.Selection) {
		if a.deps.ensureRuntimeOnce != nil {
			a.deps.ensureRuntimeOnce(selection)
		}
	}
	d.RepairEdge = func(selection uimodel.Selection, budget time.Duration) {
		if a.deps.repairEdge != nil {
			a.deps.repairEdge(selection, budget)
		}
	}
}

// stampedSkillsSource and stampedAgentsSource are the build stamps every test
// service reads, as a build-stamped binary carries them.
var (
	stampedSkillsSource string
	stampedAgentsSource string
)

// aiActivityPayload stands in for the terminal's busy event, which the
// desktop emits for an orchestrator pane through EmitAIActivity.
type aiActivityPayload struct {
	SessionID int  `json:"sessionId"`
	Busy      bool `json:"busy"`
}

func (a *testApp) context() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func (a *testApp) SetEmitter(emit func(name string, args ...any)) {
	a.emitMu.Lock()
	a.emitFn = emit
	a.emitMu.Unlock()
}

func (a *testApp) emit(name string, args ...any) {
	a.emitMu.RLock()
	emitFn := a.emitFn
	a.emitMu.RUnlock()
	if emitFn != nil {
		emitFn(name, args...)
	}
}

// shutdown closes every session, as the desktop's shutdown does.
func (a *testApp) shutdown(context.Context) {
	a.terminals.closeAll()
}

func createAndStartOrchestrator(t *testing.T, app *testApp) string {
	t.Helper()
	created, err := app.CreateOrchestrator("agent", []EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	return created.ID
}
