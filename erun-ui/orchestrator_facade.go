package main

import (
	"context"
	"io"
	"runtime"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-ui/internal/envops"
	orchestratorsvc "github.com/sophium/erun/erun-ui/internal/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/runtimeview"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The orchestrator and whip surface; the behavior lives in internal/orchestrator.

func (a *App) CreateOrchestrator(name string, envs []orchestratorsvc.EnvInput, dirs []string, alias string, launch orchestratorsvc.LaunchInput) (orchestratorsvc.Info, error) {
	return a.orchestrator.CreateOrchestrator(name, envs, dirs, alias, launch)
}

func (a *App) DeleteOrchestrator(id string) error {
	return a.orchestrator.DeleteOrchestrator(id)
}

func (a *App) DownloadOrchestratorOutput(id, name string) (string, error) {
	return a.orchestrator.DownloadOrchestratorOutput(id, name, a.saveFileDialog("Save orchestrator output"))
}

func (a *App) ListOrchestratorAliasChoices() ([]string, error) {
	return a.orchestrator.ListOrchestratorAliasChoices()
}

func (a *App) ListOrchestratorEnvCandidates() ([]orchestratorsvc.EnvCandidate, error) {
	return a.orchestrator.ListOrchestratorEnvCandidates()
}

func (a *App) ListOrchestratorLaunchChoices() (orchestratorsvc.LaunchChoices, error) {
	return a.orchestrator.ListOrchestratorLaunchChoices()
}

func (a *App) ListOrchestratorOutputs(id string) (model.RuntimeOutputsListResult, error) {
	return a.orchestrator.ListOrchestratorOutputs(id)
}

func (a *App) ListOrchestrators() []orchestratorsvc.Info {
	return a.orchestrator.ListOrchestrators()
}

func (a *App) OrchestratorGuidancePaths(id string) orchestratorsvc.GuidancePaths {
	return a.orchestrator.OrchestratorGuidancePaths(id)
}

func (a *App) RestartOrchestrator(id string, cols, rows int) (orchestratorsvc.Info, error) {
	return a.orchestrator.RestartOrchestrator(id, cols, rows)
}

func (a *App) RevealOrchestratorGuidance(id, layer, ide string) error {
	return a.orchestrator.RevealOrchestratorGuidance(id, layer, ide)
}

func (a *App) RunOrchestratorOutputOnHost(id, name string) error {
	return a.orchestrator.RunOrchestratorOutputOnHost(id, name)
}

func (a *App) StartOrchestrator(id string, cols, rows int) (orchestratorsvc.Info, error) {
	return a.orchestrator.StartOrchestrator(id, cols, rows)
}

func (a *App) StopOrchestrator(id string) error {
	return a.orchestrator.StopOrchestrator(id)
}

func (a *App) UpdateOrchestrator(id, name string, envs []orchestratorsvc.EnvInput, dirs []string, alias string, launch orchestratorsvc.LaunchInput) (orchestratorsvc.Info, error) {
	return a.orchestrator.UpdateOrchestrator(id, name, envs, dirs, alias, launch)
}

func (a *App) WhipNow(targets []orchestratorsvc.WhipTargetRef) (orchestratorsvc.WhipReport, error) {
	return a.orchestrator.WhipNow(targets)
}

func (a *App) WhipTargets() (orchestratorsvc.WhipTargetList, error) {
	return a.orchestrator.WhipTargets()
}

// newOrchestratorService reads the deps funcs and sibling services per call, so
// a test that swaps a dep after NewApp still reaches its fake.
func (a *App) newOrchestratorService() *orchestratorsvc.Service {
	return orchestratorsvc.New(orchestratorsvc.Deps{
		Store:        a.deps.store,
		Context:      a.context,
		Started:      func() bool { return a.ctx != nil },
		Emit:         a.emit,
		Terminals:    orchestratorTerminals{a: a},
		ClaudeAgents: func() orchestratorsvc.AgentCLI { return a.deps.claudeAgents },
		ResolveLaunch: func(launch orchestrator.OrchestratorLaunch, agentID, initialPrompt, mcpConfigPath string) (string, []string, error) {
			return a.deps.resolveOrchestratorLaunch(launch, agentID, initialPrompt, mcpConfigPath)
		},
		// The size is clamped here, not in the service, so a respawn starts at the clamped size too.
		StartTerminal: func(params orchestratorsvc.StartParams) (orchestratorsvc.PTY, error) {
			cols, rows := terminal.ClampSize(params.Cols, params.Rows)
			return a.deps.startTerminal(terminal.StartParams{
				Dir:        terminal.ResolveStartDir(params.Dir),
				Executable: params.Executable,
				Args:       params.Args,
				Env:        params.Env,
				Cols:       cols,
				Rows:       rows,
			})
		},
		OpenIDE: a.openOrchestratorGuidance,
		ResolveGatewayCredential: func(catalog *config.OpenRouterConfig) (string, error) {
			return a.deps.resolveGatewayCredential(catalog)
		},
		ResolveOpenRouterConfig: func() *config.OpenRouterConfig { return a.settings.ResolveOpenRouterConfig() },
		RunLabelCommand: func(ctx context.Context, dir, name string, args ...string) (string, error) {
			return a.deps.runOrchestratorLabelCommand(ctx, dir, name, args...)
		},
		// A missing probe reads as answering, so an unknown never reports an outage.
		CanReachMCPEndpoint: func(port int) bool {
			return a.deps.canReachMCPEndpoint == nil || a.deps.canReachMCPEndpoint(port)
		},
		MCPForwardStale: func(port int) bool {
			return a.terminal.ClassifyMCPUnreachable(port) == mcp.LocalMCPStaleForward
		},
		MCPEdgeAnswers: func(port int) bool { return a.envops.MCPEdgeAnswers(port) },
		MCPEndpoint:    mcpEndpointForOpenResult,
		MCPBearer:      a.mcpBearer,
		WhipEnvironment: func(ctx context.Context, tenant, environment, endpoint, bearer string) (model.WhipResult, error) {
			return a.deps.whipEnvironment(ctx, tenant, environment, endpoint, bearer)
		},
		LaunchHostArtifact: func(exePath, dir string) error {
			return a.deps.launchHostArtifact(exePath, dir)
		},
		ReportHostArtifactSigning: a.reportHostArtifactSigning,
		EnsureRuntimeOnce:         func(selection uimodel.Selection) { a.envops.EnsureRuntimeOnce(selection) },
		RepairEdge: func(selection uimodel.Selection, budget time.Duration) {
			a.envops.EnsureRuntimeWithin(selection, envops.EnsureForBrokenForward, budget)
		},
		ActivitySnapshot: a.orchestratorEnvActivity,
		UsageSnapshot:    a.orchestratorEnvUsage,
		EmitAIActivity: func(serial int, busy bool) {
			a.terminal.EmitAIActivity(serial, uimodel.Selection{}, busy)
		},
		SkillsSource:     func() string { return buildSkillsSource },
		AgentsSource:     func() string { return buildAgentsSource },
		OpenPath:         func() string { return a.deps.orchestratorOpenPath },
		NudgeHistoryPath: func() string { return a.deps.orchestratorNudgeHistoryPath },
	})
}

// openOrchestratorGuidance opens a guidance layer in the host IDE from the shared workspace.
func (a *App) openOrchestratorGuidance(ctx context.Context, dir, ide, path string) error {
	executable, args, err := terminal.LocalOpenIDECommand(runtime.GOOS, ide, path)
	if err != nil {
		return err
	}
	output, err := a.deps.runIDECommand(ctx, terminal.StartParams{
		Dir:        dir,
		Executable: executable,
		Args:       args,
		Env:        []string{appSessionEnvVar + "=1"},
	})
	if err == nil {
		return nil
	}
	return terminal.FormatOpenIDEError(ide, output, err)
}

// orchestratorEnvActivity is the runtime's activity readings as the orchestrator cards and pacer read them.
func (a *App) orchestratorEnvActivity() map[string]orchestratorsvc.EnvActivity {
	states := a.runtime.ActivitySnapshot()
	if states == nil {
		return nil
	}
	out := make(map[string]orchestratorsvc.EnvActivity, len(states))
	for key, state := range states {
		out[key] = orchestratorsvc.EnvActivity{
			Snapshot:                *runtimeview.ActivitySnapshotFrom(state),
			BusyHolderOrchestrators: state.BusyHolderOrchestrators,
		}
	}
	return out
}

// orchestratorEnvUsage is the runtime's usage readings as the orchestrator cards render them.
func (a *App) orchestratorEnvUsage() map[string]uimodel.UsageSnapshot {
	readings := a.runtime.UsageSnapshot()
	if readings == nil {
		return nil
	}
	out := make(map[string]uimodel.UsageSnapshot, len(readings))
	for key, reading := range readings {
		out[key] = *runtimeview.UsageSnapshotFrom(reading)
	}
	return out
}

// orchestratorTerminals is the terminal registry as the orchestrator service reaches it.
type orchestratorTerminals struct {
	a *App
}

func (t orchestratorTerminals) WithLock(fn func(orchestratorsvc.Registry)) {
	t.a.terminal.WithLock(func(l *terminal.Locked) {
		fn(orchestratorRegistry{a: t.a, l: l})
	})
}

type orchestratorRegistry struct {
	a *App
	l *terminal.Locked
}

func (r orchestratorRegistry) Live(id string) (orchestratorsvc.Terminal, bool) {
	managed := r.l.Session(terminal.OrchestratorSessionKey(id))
	if managed == nil || managed.Closed {
		return nil, false
	}
	return orchestratorTerminal{a: r.a, managed: managed}, true
}

func (r orchestratorRegistry) Register(id string, pty orchestratorsvc.PTY, respawn func() (orchestratorsvc.PTY, error)) orchestratorsvc.Terminal {
	managed := &terminal.Session{
		PTY:       pty,
		Key:       terminal.OrchestratorSessionKey(id),
		Serial:    r.l.NextSerial(),
		Kind:      terminal.KindOrchestrator,
		StartedAt: time.Now(),
	}
	if respawn != nil {
		managed.Respawn = func() (terminal.PTY, error) {
			session, err := respawn()
			return session, err
		}
	}
	r.l.Register(managed)
	return orchestratorTerminal{a: r.a, managed: managed}
}

func (r orchestratorRegistry) Remove(id string) (orchestratorsvc.Terminal, bool) {
	key := terminal.OrchestratorSessionKey(id)
	managed := r.l.Session(key)
	if managed == nil {
		return nil, false
	}
	r.l.Delete(key)
	return orchestratorTerminal{a: r.a, managed: managed}, true
}

func (r orchestratorRegistry) TypedRecently(id string) bool {
	managed := r.l.Session(terminal.OrchestratorSessionKey(id))
	return managed != nil && r.l.TypedRecently(managed)
}

type orchestratorTerminal struct {
	a       *App
	managed *terminal.Session
}

func (t orchestratorTerminal) Serial() int { return t.managed.Serial }

func (t orchestratorTerminal) Stream() { t.a.terminal.SpawnStream(t.managed) }

func (t orchestratorTerminal) Close() { _ = t.a.terminal.CloseManaged(t.managed) }

// WriteIfOpen reads the PTY under the terminal lock, so a concurrent close or
// respawn cannot race the write.
func (t orchestratorTerminal) WriteIfOpen(text string) bool {
	var session terminal.PTY
	t.a.terminal.WithLock(func(*terminal.Locked) {
		if !t.managed.Closed {
			session = t.managed.PTY
		}
	})
	if session == nil {
		return false
	}
	_, err := io.WriteString(session, text)
	return err == nil
}
