package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	orchestratorsvc "github.com/sophium/erun/erun-ui/internal/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The orchestrator facade's tests: the paths that cross from the orchestrator
// service into the terminal registry, the environment operations, the
// investigations and the restart hand-off. The service's own behavior is
// tested in internal/orchestrator.

// newOrchestratorStubStore has a remote-agent, a local-agent and a non-orchestratable runtime env.
func newOrchestratorStubStore(laptopRepoPath string) stubUIStore {
	return stubUIStore{
		config: &configpkg.ERunConfig{},
		tenants: map[string]model.TenantConfig{
			"frs": {Name: "frs", DefaultEnvironment: "dev"},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/dev":     {Name: "dev", Type: configpkg.EnvironmentTypeRemoteAgent, KubernetesContext: "ctx"},
			"frs/laptop":  {Name: "laptop", Type: configpkg.EnvironmentTypeLocalAgent, LocalRepoPath: laptopRepoPath},
			"frs/runtime": {Name: "runtime", Type: configpkg.EnvironmentTypeRuntime},
		},
	}
}

// orchestratorTestApp confines $HOME to a temp dir and stubs every process an
// orchestrator start would run.
func orchestratorTestApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	// Empty skills/agents sources so no "no skills source" report is posted.
	t.Setenv("ERUN_SKILLS_DIR", t.TempDir())
	t.Setenv("ERUN_AGENTS_DIR", t.TempDir())
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(terminal.StartParams) (terminal.PTY, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(orchestrator.OrchestratorLaunch, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		canReachMCPEndpoint:         orchestratorTestAlwaysReachable,
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
		resolveGatewayCredential: func(*configpkg.OpenRouterConfig) (string, error) {
			return "", errors.New("no gateway credential staged by this test")
		},
		// Keep failure reports out of the shared host temp dir.
		investigationReportDir: t.TempDir(),
	})
	return app
}

func orchestratorTestAlwaysReachable(int) bool { return true }

// stubOrchestratorClaimLabel keeps tests from minting real labels via gh.
func stubOrchestratorClaimLabel(context.Context, string, string, ...string) (string, error) {
	return "", nil
}

const investigateHelmTimeoutReport = `erun deploy failed
Target: frs/dev
Version: 1.0.179
Release: frs-devops
Namespace: frs-dev
Started: 2026-08-16T06:01:12Z
Elapsed: 4s

Error: ==> Deploy failed after 4s

Output:
helm upgrade --install frs-devops ./chart
Error: UPGRADE FAILED: timed out waiting for the condition`

const (
	testGatewayURL   = "https://gateway.example.test"
	testGatewayToken = "sk-gateway-test-secret"
)

func stageGatewayCatalog(app *App) {
	stubStore(app).config.OpenRouter = &configpkg.OpenRouterConfig{
		BaseURL:      testGatewayURL,
		DefaultModel: "acme/coder",
		Models: []configpkg.OpenRouterModel{
			{ID: "acme/coder", Context: 200000},
			{ID: "acme/thinker", RequiresReasoningEcho: true},
		},
	}
}

func editStoredOrchestrator(app *App, id string, edit func(*configpkg.OrchestratorConfig)) {
	config := stubStore(app).config
	for i := range config.Orchestrators {
		if config.Orchestrators[i].ID == id {
			edit(&config.Orchestrators[i])
		}
	}
}

func withOrchestratorConfigHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	// macOS resolves UserConfigDir from HOME.
	t.Setenv("HOME", home)
	return home
}

// orchestratorConfigFile is where the orchestrator service keeps id's per-orchestrator file.
func orchestratorConfigFile(t *testing.T, elem ...string) string {
	t.Helper()
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("resolve the config dir: %v", err)
	}
	return filepath.Join(append([]string{configDir, "ERun"}, elem...)...)
}

// orchestratorMCPConfigFile is the --mcp-config file the service writes for id.
func orchestratorMCPConfigFile(t *testing.T, id string) string {
	t.Helper()
	return orchestratorConfigFile(t, "orchestrator-mcp-"+id+".json")
}

// recordOpenOrchestrators writes the open set oldest first, as the service records starts.
func recordOpenOrchestrators(t *testing.T, path string, ids ...string) {
	t.Helper()
	type entry struct {
		OrchestratorID string `json:"orchestratorId"`
	}
	entries := make([]entry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, entry{OrchestratorID: id})
	}
	data, err := json.Marshal(map[string]any{"orchestrators": entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("record open orchestrators: %v", err)
	}
}

func TestInvestigateFailureSpawnsTransientTenantScopedOrchestrator(t *testing.T) {
	var seededPrompt string
	var seededLaunch orchestrator.OrchestratorLaunch
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	app.deps.resolveOrchestratorLaunch = func(launch orchestrator.OrchestratorLaunch, _, prompt, _ string) (string, []string, error) {
		seededPrompt, seededLaunch = prompt, launch
		return "claude-stub", nil, nil
	}

	info, err := app.InvestigateFailure(investigateHelmTimeoutReport, "frs", "dev", 80, 24)
	if err != nil {
		t.Fatalf("InvestigateFailure failed: %v", err)
	}
	if !info.Transient || len(info.Tenants) != 1 || info.Tenants[0] != "frs" {
		t.Fatalf("expected a transient investigator scoped to frs, got %+v", info)
	}
	if seededLaunch.Model != "opus" || seededLaunch.Gateway != nil {
		t.Fatalf("expected the default launch, got %+v", seededLaunch)
	}
	requireStagedReportPrompt(t, seededPrompt, "UPGRADE FAILED")
	if listed := app.ListOrchestrators(); len(listed) != 1 || !listed[0].Transient {
		t.Fatalf("expected one transient investigator listed, got %+v", listed)
	}
}

// requireStagedReportPrompt reads the report the prompt names and requires it to carry detail.
func requireStagedReportPrompt(t *testing.T, prompt, detail string) {
	t.Helper()
	if !strings.Contains(prompt, "report-") || !strings.Contains(prompt, "erun-file-issue") {
		t.Fatalf("unexpected seed prompt: %q", prompt)
	}
	start := strings.Index(prompt, "saved at ") + len("saved at ")
	end := strings.Index(prompt, ". Read it")
	if start < len("saved at ") || end <= start {
		t.Fatalf("could not locate the report path in the prompt: %q", prompt)
	}
	data, err := os.ReadFile(prompt[start:end])
	if err != nil {
		t.Fatalf("staged report file: %v", err)
	}
	if !strings.Contains(string(data), detail) {
		t.Fatalf("staged report missing the failure detail: %q", data)
	}
}

func TestInvestigateFailureRejectsEmptyReport(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	if _, err := app.InvestigateFailure("   ", "frs", "dev", 80, 24); err == nil {
		t.Fatal("expected an empty failure report to be rejected")
	}
}

// Real operator input in an orchestrator pane rearms its pacing cap, so the
// terminal's input path must reach the orchestrator service.
func TestSendSessionInputRearmsOrchestratorPacing(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	created, err := app.CreateOrchestrator("agent", []orchestratorsvc.EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorsvc.LaunchInput{})
	mustNoErr(t, err, "CreateOrchestrator")
	started, err := app.StartOrchestrator(created.ID, 80, 24)
	mustNoErr(t, err, "StartOrchestrator")

	whip := []orchestratorsvc.WhipTargetRef{{Kind: "orchestrator", ID: created.ID}}
	for i := 0; i <= orchestrator.DefaultWhipMaxNudges; i++ {
		_, err := app.WhipNow(whip)
		mustNoErr(t, err, "WhipNow")
	}
	if info := listedOrchestrator(t, app, created.ID); !info.NudgeCapped {
		t.Fatalf("expected repeated whips to cap pacing, got %+v", info)
	}

	mustNoErr(t, app.SendSessionInput(started.SessionID, "hello"), "SendSessionInput")

	if info := listedOrchestrator(t, app, created.ID); info.NudgeCount != 0 || info.NudgeCapped {
		t.Fatalf("expected real operator input to rearm pacing, got %+v", info)
	}
}

func listedOrchestrator(t *testing.T, app *App, id string) orchestratorsvc.Info {
	t.Helper()
	for _, info := range app.ListOrchestrators() {
		if info.ID == id {
			return info
		}
	}
	t.Fatalf("ListOrchestrators did not list %q", id)
	return orchestratorsvc.Info{}
}

// A crash reattaches to the same agent without a prompt.
func TestOrchestratorRespawnsAfterCrashOntoTheSameAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	var mu sync.Mutex
	var sessions []*stubTerminalSession
	var attached, prompts []string
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(terminal.StartParams) (terminal.PTY, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
		resolveOrchestratorLaunch: func(_ orchestrator.OrchestratorLaunch, agentID, initialPrompt, _ string) (string, []string, error) {
			mu.Lock()
			attached = append(attached, agentID)
			prompts = append(prompts, initialPrompt)
			mu.Unlock()
			return "claude-stub", nil, nil
		},
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
	})
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	created, err := app.CreateOrchestrator("agent", []orchestratorsvc.EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorsvc.LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	mu.Lock()
	first := sessions[0]
	first.waitErr = errors.New("exit status 1")
	mu.Unlock()
	_ = first.Close()

	waitForSessionCount(t, &mu, &sessions, 2, 2*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(attached) != 2 || attached[0] == "" || attached[0] != attached[1] {
		t.Fatalf("respawn must reattach to the SAME agent, got %v", attached)
	}
	if prompts[1] != "" {
		t.Fatalf("a reattach must not send the agent a prompt, got %q", prompts[1])
	}
	if creates := len(fakeAgentsOf(t, app).creates); creates != 1 {
		t.Fatalf("expected the agent created once and reattached, got %d creates", creates)
	}
}

func TestOrchestratorCleanExitDoesNotRespawn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	var mu sync.Mutex
	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(terminal.StartParams) (terminal.PTY, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
		resolveOrchestratorLaunch: func(orchestrator.OrchestratorLaunch, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
	})
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	created, err := app.CreateOrchestrator("agent", []orchestratorsvc.EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorsvc.LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	mu.Lock()
	first := sessions[0]
	mu.Unlock()
	_ = first.Close() // waitErr is nil: a clean exit

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(emits.events(uimodel.TerminalExitEvent)) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(emits.events(uimodel.TerminalExitEvent)) == 0 {
		t.Fatal("expected the clean exit to finalize the session")
	}
	mu.Lock()
	got := len(sessions)
	mu.Unlock()
	if got != 1 {
		t.Fatalf("a clean exit must not respawn, got %d sessions", got)
	}
}

func TestStopOrchestratorRefusesItsOwnRespawn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	var mu sync.Mutex
	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(terminal.StartParams) (terminal.PTY, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
		resolveOrchestratorLaunch: func(orchestrator.OrchestratorLaunch, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
	})
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorsvc.EnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorsvc.LaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	if err := app.StopOrchestrator(created.ID); err != nil {
		t.Fatalf("StopOrchestrator failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	got := len(sessions)
	mu.Unlock()
	if got != 1 {
		t.Fatalf("Stop must not be undone by a respawn, got %d sessions", got)
	}
	if app.orchestrator.Running(created.ID) {
		t.Fatal("expected no running session after Stop")
	}
}
