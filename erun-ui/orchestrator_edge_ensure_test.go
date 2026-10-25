package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Named because the probe looks for this orchestrator's per-id config path.
const orchestratorEdgeTestID = "petios"

// spawnEdgeProbe records, per reconnect, whether the client config already
// existed, so tests assert the ordering rather than the end state.
type spawnEdgeProbe struct {
	mu                         sync.Mutex
	answering                  bool
	err                        error
	attempts                   int
	configExistedBeforeAttempt []bool
}

func (p *spawnEdgeProbe) reachable(int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.answering
}

func (p *spawnEdgeProbe) reconnect(_ context.Context, _ config.OpenResult, _ func(string)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, statErr := os.Stat(orchestratorMCPConfigPath(orchestratorEdgeTestID))
	p.configExistedBeforeAttempt = append(p.configExistedBeforeAttempt, statErr == nil)
	p.attempts++
	if p.err != nil {
		return p.err
	}
	p.answering = true
	return nil
}

func (p *spawnEdgeProbe) snapshot() (attempts int, configExisted []bool, answering bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempts, append([]bool(nil), p.configExistedBeforeAttempt...), p.answering
}

// orchestratorEdgeTestApp sets ctx; without it the ensure no-ops, which keeps
// other orchestrator tests off this path.
func orchestratorEdgeTestApp(t *testing.T, edge *spawnEdgeProbe) (*App, *capturedEmits) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// os.UserConfigDir reads XDG_CONFIG_HOME first on Linux.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ERUN_SKILLS_DIR", t.TempDir())
	t.Setenv("ERUN_AGENTS_DIR", t.TempDir())
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
	// ResolveOpen needs a resolvable repo path even for a remote-agent env.
	store := newOrchestratorStubStore(t.TempDir())
	devEnv := store.envs["frs/dev"]
	devEnv.LocalRepoPath = t.TempDir()
	store.envs["frs/dev"] = devEnv
	emits := newCapturedEmits()
	app := NewApp(erunUIDeps{
		store: store,
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(orchestrator.OrchestratorLaunch, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		canReachMCPEndpoint:         edge.reachable,
		reconnectMCP:                edge.reconnect,
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
	})
	app.ctx = context.Background()
	app.SetEmitter(emits.fn())
	app.investigations.reportDir = t.TempDir()
	return app, emits
}

func spawnEdgeTestOrchestrator(t *testing.T, app *App) {
	t.Helper()
	spawn := orchestratorSpawn{
		id:   orchestratorEdgeTestID,
		name: "Petios",
		envs: []config.OrchestratorEnvConfig{{Tenant: "frs", Environment: "dev"}},
		cols: 80,
		rows: 24,
	}
	if _, err := app.spawnOrchestratorSession(spawn); err != nil {
		t.Fatalf("spawnOrchestratorSession: %v", err)
	}
}

func spawnEdgeTestTarget() []config.OrchestratorEnvConfig {
	return []config.OrchestratorEnvConfig{{Tenant: "frs", Environment: "dev"}}
}

func TestSpawnOrchestratorOpensTheEdgeBeforeWritingMCPConfig(t *testing.T) {
	edge := &spawnEdgeProbe{}
	app, emits := orchestratorEdgeTestApp(t, edge)
	defer app.shutdown(context.Background())

	spawnEdgeTestOrchestrator(t, app)

	attempts, configExisted, answering := edge.snapshot()
	if attempts != 1 {
		t.Fatalf("edge rebinds = %d, want 1 — a linked env that is not answering must be opened", attempts)
	}
	if configExisted[0] {
		t.Fatal("the MCP client config was already on disk when the edge was opened: the client would launch against a dead port")
	}
	if !answering {
		t.Fatal("the rebind did not leave the edge answering")
	}

	data, err := os.ReadFile(orchestratorMCPConfigPath(orchestratorEdgeTestID))
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if !strings.Contains(string(data), "frs-dev") {
		t.Fatalf("the linked env must still be wired:\n%s", data)
	}
	for _, event := range emits.events(uimodel.AppNotificationEvent) {
		if payload, ok := event.(uimodel.AppNotificationPayload); ok && strings.Contains(payload.Message, "not answering") {
			t.Fatalf("a repaired edge must not be reported unreachable: %q", payload.Message)
		}
	}
}

func TestSpawnOrchestratorCompletesWithAnUnreachableEdge(t *testing.T) {
	edge := &spawnEdgeProbe{err: errors.New("timed out waiting for MCP port-forward")}
	app, emits := orchestratorEdgeTestApp(t, edge)
	defer app.shutdown(context.Background())

	spawnEdgeTestOrchestrator(t, app)

	if attempts, _, answering := edge.snapshot(); attempts != 1 || answering {
		t.Fatalf("attempts = %d, answering = %v; want one attempt against an edge still down", attempts, answering)
	}

	// An unreachable edge is recoverable per call, so the env stays wired.
	data, err := os.ReadFile(orchestratorMCPConfigPath(orchestratorEdgeTestID))
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if !strings.Contains(string(data), "frs-dev") {
		t.Fatalf("an unreachable env is still wired, not skipped:\n%s", data)
	}

	notice := unreachableNoticeFor(t, emits, "frs/dev")
	if notice.Kind != "warning" {
		t.Fatalf("kind = %q, want warning so the notice persists", notice.Kind)
	}
	if notice.Tenant != "frs" || notice.Environment != "dev" {
		t.Fatalf("notice = %+v, want it tagged with the env it names", notice)
	}
	if notice.Action != notificationActionDeploy {
		t.Fatalf("action = %q, want %q so the named recovery is reachable", notice.Action, notificationActionDeploy)
	}
}

func TestWireOrchestratorMCPRetriesAnEdgeRepairThatFailed(t *testing.T) {
	edge := &spawnEdgeProbe{err: errors.New("timed out waiting for MCP port-forward")}
	app, _ := orchestratorEdgeTestApp(t, edge)
	defer app.shutdown(context.Background())

	app.wireOrchestratorMCP(orchestratorEdgeTestID, "Petios", spawnEdgeTestTarget())

	if _, stamped := app.envops.EnsureReachedAt(uimodel.Selection{Tenant: "frs", Environment: "dev"}); stamped {
		t.Fatal("a failed rebind stamped the dedup window: the next launch would be suppressed for the whole TTL")
	}

	app.wireOrchestratorMCP(orchestratorEdgeTestID, "Petios", spawnEdgeTestTarget())
	if attempts, _, _ := edge.snapshot(); attempts != 2 {
		t.Fatalf("edge rebinds = %d, want 2 — a failed repair must be retried on the next launch", attempts)
	}
}

func TestWireOrchestratorMCPLeavesAnAnsweringEdgeAlone(t *testing.T) {
	edge := &spawnEdgeProbe{answering: true}
	app, emits := orchestratorEdgeTestApp(t, edge)
	defer app.shutdown(context.Background())

	app.wireOrchestratorMCP(orchestratorEdgeTestID, "Petios", spawnEdgeTestTarget())

	if attempts, _, _ := edge.snapshot(); attempts != 0 {
		t.Fatalf("edge rebinds = %d, want 0 — an answering edge must not be reopened", attempts)
	}
	for _, event := range emits.events(uimodel.AppNotificationEvent) {
		if payload, ok := event.(uimodel.AppNotificationPayload); ok && strings.Contains(payload.Message, "not answering") {
			t.Fatalf("an answering edge was reported unreachable: %q", payload.Message)
		}
	}
}

func unreachableNoticeFor(t *testing.T, emits *capturedEmits, label string) uimodel.AppNotificationPayload {
	t.Helper()
	var found []uimodel.AppNotificationPayload
	for _, event := range emits.events(uimodel.AppNotificationEvent) {
		payload, ok := event.(uimodel.AppNotificationPayload)
		if !ok {
			t.Fatalf("unexpected payload type: %T", event)
		}
		if strings.Contains(payload.Message, label) && strings.Contains(payload.Message, "not answering") {
			found = append(found, payload)
		}
	}
	if len(found) != 1 {
		t.Fatalf("notices naming %s and its silent edge = %d, want 1: %+v", label, len(found), emits.events(uimodel.AppNotificationEvent))
	}
	return found[0]
}
