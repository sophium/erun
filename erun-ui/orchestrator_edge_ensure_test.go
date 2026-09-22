package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// orchestratorEdgeTestID is the orchestrator the launch-path tests wire. It is
// named, rather than left to a generated id, because the client config the
// repair must precede is a per-orchestrator file and the probe has to look for
// that exact path.
const orchestratorEdgeTestID = "petios"

// spawnEdgeProbe is one linked environment's MCP edge as these tests drive it:
// the reachability probe answers from `answering`, and the reconnect stub flips
// that to true as a real port-forward coming up would. The probe the config
// write makes — the one after the repair — therefore sees an answering edge
// exactly when the repair worked.
//
// Every reconnect records whether the orchestrator's client config already
// existed at the moment it ran. That is the ordering the whole change exists to
// hold, and it is asserted rather than inferred from the end state, which is
// identical whether the edge was opened before the write or after it.
type spawnEdgeProbe struct {
	mu        sync.Mutex
	answering bool
	err       error
	attempts  int
	// configExistedBeforeAttempt is appended once per reconnect.
	configExistedBeforeAttempt []bool
}

func (p *spawnEdgeProbe) reachable(int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.answering
}

func (p *spawnEdgeProbe) reconnect(_ context.Context, _ eruncommon.OpenResult, _ func(string)) error {
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

// snapshot copies the probe's observations out from under its lock, so an
// assertion never races a reconnect.
func (p *spawnEdgeProbe) snapshot() (attempts int, configExisted []bool, answering bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempts, append([]bool(nil), p.configExistedBeforeAttempt...), p.answering
}

// orchestratorEdgeTestApp builds an app a launch can actually run against: one
// remote-agent env that resolves an MCP port, a stubbed harness launch, and the
// ctx and reconnect seam the ensure needs. Without ctx the ensure no-ops by
// design (see beginEnvRuntimeEnsure), which is what keeps every other
// orchestrator test off this path.
func orchestratorEdgeTestApp(t *testing.T, edge *spawnEdgeProbe) (*App, *capturedEmits) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// os.UserConfigDir reads XDG_CONFIG_HOME first on Linux, so pin it too or
	// the config path this test stats is whatever the developer's shell says.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ERUN_SKILLS_DIR", t.TempDir())
	t.Setenv("ERUN_AGENTS_DIR", t.TempDir())
	// Pin the executable seam: without it the wire only writes a config where an
	// erun binary happens to sit on PATH or beside the test binary.
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
	// ResolveOpen — the first thing a rebind does — needs a resolvable repo path
	// even for a remote-agent env, which newOrchestratorStubStore does not set.
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
		resolveOrchestratorLaunch: func(string, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		canReachMCPEndpoint: edge.reachable,
		reconnectMCP:        edge.reconnect,
	})
	app.ctx = context.Background()
	app.SetEmitter(emits.fn())
	app.investigations.reportDir = t.TempDir()
	return app, emits
}

// spawnEdgeTestOrchestrator launches the one orchestrator these tests wire,
// through the real spawn path rather than the wire alone.
func spawnEdgeTestOrchestrator(t *testing.T, app *App) {
	t.Helper()
	spawn := orchestratorSpawn{
		id:   orchestratorEdgeTestID,
		name: "Petios",
		envs: []eruncommon.OrchestratorEnvConfig{{Tenant: "frs", Environment: "dev"}},
		cols: 80,
		rows: 24,
	}
	if _, err := app.spawnOrchestratorSession(spawn); err != nil {
		t.Fatalf("spawnOrchestratorSession: %v", err)
	}
}

func spawnEdgeTestTarget() []eruncommon.OrchestratorEnvConfig {
	return []eruncommon.OrchestratorEnvConfig{{Tenant: "frs", Environment: "dev"}}
}

// TestSpawnOrchestratorOpensTheEdgeBeforeWritingMCPConfig is the regression for
// the defect this change exists to fix: an orchestrator spawn wired an MCP
// client at a dead edge, so the client's one launch-time connect failed and
// every tool for that environment stayed unavailable for the whole session —
// until the operator ran `erun open` by hand, which nothing had told them to do.
//
// The environment here is healthy and merely unforwarded, which is the case an
// operator hits on every restart. The assertion is the ordering, not the end
// state: at the moment the rebind ran, the client config naming that port did
// not exist yet.
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
	// The edge answered by the time the config was written, so the launch has
	// nothing to report — a notice here is the false alarm that sends an
	// operator to fix a working environment.
	for _, event := range emits.events(appNotificationEvent) {
		if payload, ok := event.(appNotificationPayload); ok && strings.Contains(payload.Message, "not answering") {
			t.Fatalf("a repaired edge must not be reported unreachable: %q", payload.Message)
		}
	}
}

// TestSpawnOrchestratorCompletesWithAnUnreachableEdge locks the other half of
// the contract: the repair is not a gate. An environment that genuinely cannot
// be reached — its pod is down, the cluster is unreachable — must not hold the
// orchestrator back from launching, and must still be reported honestly rather
// than silently wired at a port nobody opened.
func TestSpawnOrchestratorCompletesWithAnUnreachableEdge(t *testing.T) {
	edge := &spawnEdgeProbe{err: errors.New("timed out waiting for MCP port-forward")}
	app, emits := orchestratorEdgeTestApp(t, edge)
	defer app.shutdown(context.Background())

	spawnEdgeTestOrchestrator(t, app)

	if attempts, _, answering := edge.snapshot(); attempts != 1 || answering {
		t.Fatalf("attempts = %d, answering = %v; want one attempt against an edge still down", attempts, answering)
	}

	// The session still launches with its env wired: an unreachable edge is
	// recoverable per call, so dropping the env here would strand it for the
	// whole session even after the edge came back.
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

// TestWireOrchestratorMCPRetriesAnEdgeRepairThatFailed locks the failed-ensure
// contract the shared ensure already carries (erun-ui/AGENTS.md): a reconnect
// that did not reach the runtime must not stamp the dedup window, because that
// window is what would suppress the next attempt for a whole TTL while the
// operator stares at a dead environment. A failed repair here means the next
// launch tries again rather than inheriting the previous failure's silence.
func TestWireOrchestratorMCPRetriesAnEdgeRepairThatFailed(t *testing.T) {
	edge := &spawnEdgeProbe{err: errors.New("timed out waiting for MCP port-forward")}
	app, _ := orchestratorEdgeTestApp(t, edge)
	defer app.shutdown(context.Background())

	app.wireOrchestratorMCP(orchestratorEdgeTestID, "Petios", spawnEdgeTestTarget())

	key := selectionKey(normalizeSelection(uiSelection{Tenant: "frs", Environment: "dev"}))
	app.envEnsureMu.Lock()
	_, stamped := app.envEnsureDone[key]
	app.envEnsureMu.Unlock()
	if stamped {
		t.Fatal("a failed rebind stamped the dedup window: the next launch would be suppressed for the whole TTL")
	}

	app.wireOrchestratorMCP(orchestratorEdgeTestID, "Petios", spawnEdgeTestTarget())
	if attempts, _, _ := edge.snapshot(); attempts != 2 {
		t.Fatalf("edge rebinds = %d, want 2 — a failed repair must be retried on the next launch", attempts)
	}
}

// TestWireOrchestratorMCPLeavesAnAnsweringEdgeAlone is the mirror of the retry
// above, and the reason the repair is scoped to an edge that probed dead rather
// than armed unconditionally on every launch: a linked environment that is
// already reachable must not be reconnected. Reconnecting it would put a real
// `erun open` on the launch path of every healthy orchestrator and race the
// forward its own tabs are using.
func TestWireOrchestratorMCPLeavesAnAnsweringEdgeAlone(t *testing.T) {
	edge := &spawnEdgeProbe{answering: true}
	app, emits := orchestratorEdgeTestApp(t, edge)
	defer app.shutdown(context.Background())

	app.wireOrchestratorMCP(orchestratorEdgeTestID, "Petios", spawnEdgeTestTarget())

	if attempts, _, _ := edge.snapshot(); attempts != 0 {
		t.Fatalf("edge rebinds = %d, want 0 — an answering edge must not be reopened", attempts)
	}
	for _, event := range emits.events(appNotificationEvent) {
		if payload, ok := event.(appNotificationPayload); ok && strings.Contains(payload.Message, "not answering") {
			t.Fatalf("an answering edge was reported unreachable: %q", payload.Message)
		}
	}
}

// unreachableNoticeFor finds the one "its edge is not answering" notice naming
// the given environment, failing the test when the launch posted none — the
// silence that made the original defect look like a working session.
func unreachableNoticeFor(t *testing.T, emits *capturedEmits, label string) appNotificationPayload {
	t.Helper()
	var found []appNotificationPayload
	for _, event := range emits.events(appNotificationEvent) {
		payload, ok := event.(appNotificationPayload)
		if !ok {
			t.Fatalf("unexpected payload type: %T", event)
		}
		if strings.Contains(payload.Message, label) && strings.Contains(payload.Message, "not answering") {
			found = append(found, payload)
		}
	}
	if len(found) != 1 {
		t.Fatalf("notices naming %s and its silent edge = %d, want 1: %+v", label, len(found), emits.events(appNotificationEvent))
	}
	return found[0]
}
