package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open/appsession"
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
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			sessionsMu.Lock()
			sessions = append(sessions, session)
			sessionsMu.Unlock()
			return session, nil
		},
	})
	defer app.shutdown(context.Background())
	app.SetEmitter(emits.fn())

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
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			tenants: map[string]model.TenantConfig{
				"erun": {Name: "erun", DefaultEnvironment: "remote"},
			},
			envs: map[string]config.EnvConfig{
				"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
			},
		},
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(params startTerminalSessionParams) (terminalSession, error) {
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
	defer app.shutdown(context.Background())
	app.SetEmitter(emits.fn())

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
	takenOverMarkerNeedle = "re-attached in another ERun window"
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
		payload, ok := evt.(terminalOutputPayload)
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
	app := withTestEnvops(&App{})
	managed := &managedTerminal{}

	older := time.Now().Add(-2 * reconnectLoopWindow)
	managed.recentExits = []time.Time{older, older, older, older}

	if got := app.trackExitForLoopGuard(managed); got {
		t.Fatalf("trackExitForLoopGuard tripped on stale exits; want false (pruned)")
	}
	if len(managed.recentExits) != 1 {
		t.Fatalf("expected pruning to leave 1 entry, got %d", len(managed.recentExits))
	}
}

// The on-disk status gate races the poller, so Stop records an intent marker.
func TestStopCloudContextSuppressesReconnect(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &config.ERunConfig{
			CloudContexts: []model.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              config.EnvironmentTypeRuntime,
			},
		},
	}

	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			sessionsMu.Lock()
			sessions = append(sessions, session)
			sessionsMu.Unlock()
			return session, nil
		},
		// The cache stays running, so only the intent marker can block the respawn.
		stopCloudContext: func(_ context.Context, name string) (model.CloudContextStatus, error) {
			return model.CloudContextStatus{
				CloudContextConfig: model.CloudContextConfig{Name: name, KubernetesContext: "cluster-cloud"},
				Status:             "stopping",
			}, nil
		},
	})
	defer app.shutdown(context.Background())
	app.cloudContexts.SetStatus("managed-cloud", cloud.CloudContextStatusRunning)

	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	if _, err := app.StartSession(selection, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	waitForSessionCount(t, &sessionsMu, &sessions, 1, 2*time.Second)

	if _, err := app.StopCloudContext("managed-cloud"); err != nil {
		t.Fatalf("StopCloudContext failed: %v", err)
	}

	sessionsMu.Lock()
	first := sessions[0]
	sessionsMu.Unlock()
	_ = first.Close()

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		sessionsMu.Lock()
		count := len(sessions)
		sessionsMu.Unlock()
		if count > 1 {
			t.Fatalf("StopCloudContext intent marker did not block reconnect; got %d sessions", count)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestClearIntentionalStopForCloudContext(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &config.ERunConfig{
			CloudContexts: []model.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              config.EnvironmentTypeRuntime,
			},
		},
	}

	app := NewApp(erunUIDeps{
		store: store,
		stopCloudContext: func(_ context.Context, name string) (model.CloudContextStatus, error) {
			return model.CloudContextStatus{
				CloudContextConfig: model.CloudContextConfig{Name: name, KubernetesContext: "cluster-cloud"},
				Status:             "stopping",
			}, nil
		},
	})
	defer app.shutdown(context.Background())
	app.cloudContexts.SetStatus("managed-cloud", cloud.CloudContextStatusRunning)

	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	if _, err := app.StopCloudContext("managed-cloud"); err != nil {
		t.Fatalf("StopCloudContext failed: %v", err)
	}
	if !app.isIntentionalStop(selection) {
		t.Fatal("expected intentional-stop marker after StopCloudContext")
	}

	app.clearIntentionalStopForCloudContext("managed-cloud")
	if app.isIntentionalStop(selection) {
		t.Fatal("expected intentional-stop marker to clear after clearIntentionalStopForCloudContext")
	}
}

// A failed Stop leaves the cluster up, so reconnect must stay enabled.
func TestStopCloudContextErrorClearsIntentionalStop(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &config.ERunConfig{
			CloudContexts: []model.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              config.EnvironmentTypeRuntime,
			},
		},
	}

	wantErr := errors.New("aws stop failed")
	app := NewApp(erunUIDeps{
		store: store,
		stopCloudContext: func(_ context.Context, _ string) (model.CloudContextStatus, error) {
			return model.CloudContextStatus{}, wantErr
		},
	})
	defer app.shutdown(context.Background())
	app.cloudContexts.SetStatus("managed-cloud", cloud.CloudContextStatusRunning)

	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	if _, err := app.StopCloudContext("managed-cloud"); err == nil {
		t.Fatal("expected StopCloudContext to surface stub error")
	}
	if app.isIntentionalStop(selection) {
		t.Fatal("expected intentional-stop marker to clear after StopCloudContext error")
	}
}

func TestTrackExitForLoopGuardTripsAfterCap(t *testing.T) {
	app := withTestEnvops(&App{})
	managed := &managedTerminal{}

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
	app := withTestEnvops(&App{})
	app.SetEmitter(emits.fn())

	respawned := false
	managed := &managedTerminal{
		serial:      7,
		readyClosed: true,
		readyErr:    errors.New("==> Deploy failed after 4s"),
		respawn: func() (terminalSession, error) {
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
	app := withTestEnvops(&App{})
	app.SetEmitter(emits.fn())
	app.activityQueue = newActivityQueueStore(nil, nil)
	failed, _ := app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "petios", Environment: "rihards-develop"})
	if _, ok := app.activityQueue.finish(failed.ID, activityQueueStatusFailed, "helm release failed"); !ok {
		t.Fatal("seed: finish failed deploy returned ok=false")
	}

	respawned := false
	managed := &managedTerminal{
		serial:      9,
		selection:   uimodel.Selection{Tenant: "petios", Environment: "rihards-develop"},
		readyClosed: false, // no deploy-failed readyErr — this open timed out reaching the pod
		respawn: func() (terminalSession, error) {
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
	app := withTestEnvops(&App{})
	app.SetEmitter(emits.fn())

	respawned := false
	managed := &managedTerminal{
		serial:      8,
		readyClosed: true,
		readyErr:    nil,
		respawn: func() (terminalSession, error) {
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
		payload, ok := evt.(terminalOutputPayload)
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

// Respawning would steal the session back; the window shows take-back instead.
func TestSessionTakenOverByAnotherWindowDoesNotReconnect(t *testing.T) {
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
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			session.initialOutput = append(session.initialOutput,
				[]byte(appsession.ShellSessionTakenOverNotice+"\n")...)
			sessionsMu.Lock()
			sessions = append(sessions, session)
			sessionsMu.Unlock()
			return session, nil
		},
	})
	defer app.shutdown(context.Background())
	app.SetEmitter(emits.fn())

	if _, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	waitForSessionCount(t, &sessionsMu, &sessions, 1, 2*time.Second)

	waitForTakenOverFlag(t, app, 2*time.Second)
	sessionsMu.Lock()
	current := sessions[0]
	sessionsMu.Unlock()
	_ = current.Close()

	waitForTerminalMarker(t, emits, takenOverMarkerNeedle, 10*time.Second)

	sessionsMu.Lock()
	got := len(sessions)
	sessionsMu.Unlock()
	if got != 1 {
		t.Fatalf("takeover must not respawn: expected 1 session, got %d", got)
	}
}

func waitForTakenOverFlag(t *testing.T, app *App, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		flagged := false
		for _, managed := range app.sessions {
			if managed != nil && managed.takenOver {
				flagged = true
			}
		}
		app.mu.Unlock()
		if flagged {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the taken-over notice to be scanned from session output")
}
