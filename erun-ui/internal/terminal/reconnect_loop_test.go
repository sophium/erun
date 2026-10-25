package terminal

import (
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// A session whose pod keeps dying caps its respawns and emits one stop marker.
func TestStartSessionStopsReconnectingAfterRepeatedFailures(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	emits := newCapturedEmits()
	app := newTestService(Deps{
		Store:           store,
		FindProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		ResolveCLIPath:  func() string { return "/tmp/erun" },
		StartTerminal: func(StartParams) (PTY, error) {
			session := newStubTerminalSession()
			sessionsMu.Lock()
			sessions = append(sessions, session)
			sessionsMu.Unlock()
			return session, nil
		},
	})
	defer closeTestService(app)
	app.d.Emit = emits.fn()

	if _, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	const closes = reconnectLoopMaxExits + 1
	for i := 0; i < closes; i++ {
		expected := i + 1
		waitForSessionCount(t, &sessionsMu, &sessions, expected, 2*time.Second)
		sessionsMu.Lock()
		current := sessions[expected-1]
		sessionsMu.Unlock()
		_ = current.Close()
	}

	// The marker is emitted before the refused respawn, so once it lands the
	// session count is final.
	waitForTerminalMarker(t, emits, loopGuardMarkerNeedle, 10*time.Second)

	sessionsMu.Lock()
	got := len(sessions)
	sessionsMu.Unlock()
	if got != closes {
		t.Fatalf("expected loop guard to cap session count at %d (original + %d respawns), got %d",
			closes, reconnectLoopMaxExits, got)
	}
}

// A respawn must reach `erun open` as a reconnect so it does not clear a
// recorded stop; the operator's own open stays a plain open.
func TestRespawnDeclaresItselfAReconnect(t *testing.T) {
	projectRoot := t.TempDir()
	var launchesMu sync.Mutex
	var launches [][]string
	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	emits := newCapturedEmits()
	app := newTestService(Deps{
		Store: stubUIStore{
			tenants: map[string]model.TenantConfig{
				"erun": {Name: "erun", DefaultEnvironment: "remote"},
			},
			envs: map[string]config.EnvConfig{
				"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
			},
		},
		FindProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		ResolveCLIPath:  func() string { return "/tmp/erun" },
		StartTerminal: func(params StartParams) (PTY, error) {
			launchesMu.Lock()
			launches = append(launches, params.Args)
			launchesMu.Unlock()
			session := newStubTerminalSession()
			sessionsMu.Lock()
			sessions = append(sessions, session)
			sessionsMu.Unlock()
			return session, nil
		},
	})
	defer closeTestService(app)
	app.d.Emit = emits.fn()

	if _, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	waitForSessionCount(t, &sessionsMu, &sessions, 1, 2*time.Second)
	sessionsMu.Lock()
	first := sessions[0]
	sessionsMu.Unlock()
	_ = first.Close()
	waitForSessionCount(t, &sessionsMu, &sessions, 2, 2*time.Second)

	launchesMu.Lock()
	defer launchesMu.Unlock()
	if contains := strings.Contains(strings.Join(launches[0], " "), "--reconnect"); contains {
		t.Fatalf("the operator's tab open must stay a plain open: %v", launches[0])
	}
	if contains := strings.Contains(strings.Join(launches[1], " "), "--reconnect"); !contains {
		t.Fatalf("the respawn must declare itself a reconnect: %v", launches[1])
	}
}

func waitForSessionCount(t *testing.T, mu *sync.Mutex, sessions *[]*stubTerminalSession, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(*sessions)
		mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	got := len(*sessions)
	mu.Unlock()
	t.Fatalf("timed out waiting for %d sessions, got %d", want, got)
}

// Match the marker words, not the ANSI-styled string.
const (
	loopGuardMarkerNeedle = "stopped reconnecting after repeated failures"
)

// waitForTerminalMarker polls for a marker emitted from the session's goroutine.
func waitForTerminalMarker(t *testing.T, emits *capturedEmits, needle string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sawTerminalMarker(emits, needle) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for the %q marker on the terminal-output channel", needle)
}

func sawTerminalMarker(emits *capturedEmits, needle string) bool {
	for _, evt := range emits.events(uimodel.TerminalOutputEvent) {
		payload, ok := evt.(uimodel.TerminalOutputPayload)
		if !ok {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(payload.Data)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), needle) {
			return true
		}
	}
	return false
}

// A long-lived session that exited once must still reconnect.
func TestTrackExitForLoopGuardPrunesOldExits(t *testing.T) {
	app := newTestService(Deps{})
	managed := &Session{}

	older := time.Now().Add(-2 * reconnectLoopWindow)
	managed.recentExits = []time.Time{older, older, older, older}

	if got := app.trackExitForLoopGuard(managed); got {
		t.Fatalf("trackExitForLoopGuard tripped on stale exits; want false (pruned)")
	}
	if len(managed.recentExits) != 1 {
		t.Fatalf("expected pruning to leave 1 entry, got %d", len(managed.recentExits))
	}
}

func TestTrackExitForLoopGuardTripsAfterCap(t *testing.T) {
	app := newTestService(Deps{})
	managed := &Session{}

	for i := 1; i <= reconnectLoopMaxExits; i++ {
		if app.trackExitForLoopGuard(managed) {
			t.Fatalf("loop guard tripped early at call %d (cap=%d)", i, reconnectLoopMaxExits)
		}
	}
	if !app.trackExitForLoopGuard(managed) {
		t.Fatalf("loop guard did not trip on call %d (cap=%d)",
			reconnectLoopMaxExits+1, reconnectLoopMaxExits)
	}
}

// Respawning after a deploy failure would re-deploy from every tab at once.
func TestTryReconnectRefusesAfterDeployFailure(t *testing.T) {
	emits := newCapturedEmits()
	app := newTestService(Deps{})
	app.d.Emit = emits.fn()

	respawned := false
	managed := &Session{
		Serial:      7,
		readyClosed: true,
		readyErr:    errors.New("==> Deploy failed after 4s"),
		Respawn: func() (PTY, error) {
			respawned = true
			return newStubTerminalSession(), nil
		},
	}

	if app.tryReconnect(managed, "exit code 1") {
		t.Fatal("tryReconnect must refuse to respawn after a deploy failure")
	}
	if respawned {
		t.Fatal("respawn must not run after a deploy failure")
	}
	if !sawDeployFailedMarker(emits) {
		t.Fatal("expected the deploy-failed marker on the terminal-output channel")
	}
}

// The deploy failed in a separate activity, so this open's readyErr is clean.
func TestTryReconnectRefusesWhenActivityDeployFailed(t *testing.T) {
	emits := newCapturedEmits()
	app := newTestService(Deps{
		LatestDeployFailed: func(tenant, environment string) bool {
			return tenant == "petios" && environment == "rihards-develop"
		},
	})
	app.d.Emit = emits.fn()

	respawned := false
	managed := &Session{
		Serial:      9,
		Selection:   uimodel.Selection{Tenant: "petios", Environment: "rihards-develop"},
		readyClosed: false, // no deploy-failed readyErr — this open timed out reaching the pod
		Respawn: func() (PTY, error) {
			respawned = true
			return newStubTerminalSession(), nil
		},
	}

	if app.tryReconnect(managed, "timed out waiting for MCP port-forward") {
		t.Fatal("tryReconnect must refuse when the env's latest deploy failed")
	}
	if respawned {
		t.Fatal("respawn must not run when the env's latest deploy failed")
	}
	if !sawDeployFailedMarker(emits) {
		t.Fatal("expected the deploy-failed marker on the terminal-output channel")
	}
}

// A healthy session that drops reconnects; its open skips the current deploy.
func TestTryReconnectAfterHealthyDropRespawns(t *testing.T) {
	emits := newCapturedEmits()
	app := newTestService(Deps{})
	app.d.Emit = emits.fn()

	respawned := false
	managed := &Session{
		Serial:      8,
		readyClosed: true,
		readyErr:    nil,
		Respawn: func() (PTY, error) {
			respawned = true
			return newStubTerminalSession(), nil
		},
	}

	if !app.tryReconnect(managed, "transient drop") {
		t.Fatal("a healthy session that dropped should reconnect")
	}
	if !respawned {
		t.Fatal("respawn should run for a healthy dropped session")
	}
}

func sawDeployFailedMarker(emits *capturedEmits) bool {
	const needle = "deploy failed — not retrying"
	for _, evt := range emits.events(uimodel.TerminalOutputEvent) {
		payload, ok := evt.(uimodel.TerminalOutputPayload)
		if !ok {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(payload.Data)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), needle) {
			return true
		}
	}
	return false
}

// A clean exit or a torn-down registration refuses respawn.
func TestOrchestratorReconnectRefusedGuards(t *testing.T) {
	registered := map[string]bool{}
	svc := newTestService(Deps{OrchestratorRegisteredLocked: func(id string) bool { return registered[id] }})
	id := "agent"
	key := OrchestratorSessionKey(id)
	managed := &Session{Key: key, Kind: KindOrchestrator}
	registered[id] = true
	svc.sessions[key] = managed

	if !svc.orchestratorReconnectRefused(managed, "") {
		t.Fatal("a clean exit (empty reason) must refuse respawn")
	}
	if svc.orchestratorReconnectRefused(managed, "exit status 1") {
		t.Fatal("a crash with an intact registration must not be refused")
	}

	delete(registered, id)
	delete(svc.sessions, key)
	if !svc.orchestratorReconnectRefused(managed, "exit status 1") {
		t.Fatal("a torn-down registration must refuse respawn, so Stop cannot be undone")
	}
}
