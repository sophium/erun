package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestStartSessionStopsReconnectingAfterRepeatedFailures guards against
// infinite respawn: a managed session whose pod kept dying on spawn used
// to respawn forever, stacking failed-deploy entries in the activity
// drawer. The loop now caps respawns and emits a single stop marker.
func TestStartSessionStopsReconnectingAfterRepeatedFailures(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
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

	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
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

	// Wait on the marker, not on a clock: the guard runs on the session's own
	// goroutine, so a fixed sleep made this test fail under load -- and worse,
	// the count assertion below would pass vacuously for the same reason the
	// marker had not arrived (nothing had run yet). The marker is the guard's
	// observable decision, and it is emitted before the respawn it refuses, so
	// once it lands the session count is final.
	waitForTerminalMarker(t, emits, loopGuardMarkerNeedle, 10*time.Second)

	sessionsMu.Lock()
	got := len(sessions)
	sessionsMu.Unlock()
	if got != closes {
		t.Fatalf("expected loop guard to cap session count at %d (original + %d respawns), got %d",
			closes, reconnectLoopMaxExits, got)
	}
}

// TestRespawnDeclaresItselfAReconnect pins the flag that keeps a stop stopped.
// The desktop respawns a tab whenever its session drops, and a stop is exactly
// what drops every session — so a respawn that reached `erun open` as a plain
// open would clear the recorded stop and scale the runtime back up, undoing the
// operator's command within a second. The operator's own tab open must stay a
// plain open, or nothing would ever start a stopped environment again.
func TestRespawnDeclaresItselfAReconnect(t *testing.T) {
	projectRoot := t.TempDir()
	var launchesMu sync.Mutex
	var launches [][]string
	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	emits := newCapturedEmits()
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			tenants: map[string]eruncommon.TenantConfig{
				"erun": {Name: "erun", DefaultEnvironment: "remote"},
			},
			envs: map[string]eruncommon.EnvConfig{
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

	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
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

// The user-visible text of the two markers these tests wait on. Matching on
// the words rather than the full ANSI string keeps the assertion about what the
// operator reads, not about the styling around it.
const (
	loopGuardMarkerNeedle = "stopped reconnecting after repeated failures"
	takenOverMarkerNeedle = "re-attached in another ERun window"
)

// waitForTerminalMarker polls the captured emits for a terminal-output marker.
// It replaces a fixed sleep-then-assert: the markers are emitted from the
// session's own goroutine, so any fixed interval is a race against the machine
// rather than against the code under test.
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
	for _, evt := range emits.events(terminalOutputEvent) {
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

// TestTrackExitForLoopGuardPrunesOldExits guards windowed pruning: a
// session that ran for hours and then exited once must still reconnect on
// its next failure; without pruning, stale exit counters would trip the
// cap and refuse it.
func TestTrackExitForLoopGuardPrunesOldExits(t *testing.T) {
	app := &App{}
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

// TestStopCloudContextSuppressesReconnect guards the intentional-stop
// race: clicking Power stops the cloud context, but the reconnect loop
// would re-run `erun open` and restart it. The on-disk status gate alone
// races the status poller, so an intent marker recorded the moment Stop
// returns is what closes that window.
func TestStopCloudContextSuppressesReconnect(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              eruncommon.EnvironmentTypeRuntime,
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
		// The instance goes to `stopping` but the cache stays running, so the
		// on-disk status branch alone would allow the respawn — only the intent
		// marker can block it, which is what this test isolates.
		stopCloudContext: func(_ context.Context, name string) (eruncommon.CloudContextStatus, error) {
			return eruncommon.CloudContextStatus{
				CloudContextConfig: eruncommon.CloudContextConfig{Name: name, KubernetesContext: "cluster-cloud"},
				Status:             "stopping",
			}, nil
		},
	})
	defer app.shutdown(context.Background())
	app.setCloudContextStatusInCache("managed-cloud", eruncommon.CloudContextStatusRunning)

	selection := uiSelection{Tenant: "erun", Environment: "remote"}
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

// TestClearIntentionalStopForCloudContext guards the recovery path:
// once the user resumes, the intent marker must clear so a later drop
// reconnects normally; otherwise the env stays permanently stuck in
// "no reconnect" after every Stop.
func TestClearIntentionalStopForCloudContext(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              eruncommon.EnvironmentTypeRuntime,
			},
		},
	}

	app := NewApp(erunUIDeps{
		store: store,
		stopCloudContext: func(_ context.Context, name string) (eruncommon.CloudContextStatus, error) {
			return eruncommon.CloudContextStatus{
				CloudContextConfig: eruncommon.CloudContextConfig{Name: name, KubernetesContext: "cluster-cloud"},
				Status:             "stopping",
			}, nil
		},
	})
	defer app.shutdown(context.Background())
	app.setCloudContextStatusInCache("managed-cloud", eruncommon.CloudContextStatusRunning)

	selection := uiSelection{Tenant: "erun", Environment: "remote"}

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

// TestStopCloudContextErrorClearsIntentionalStop guards the failure
// path: if the AWS Stop call errors the cluster is still up, so a
// later drop must reconnect; leaving the intent marker behind would
// silently disable reconnect after a transient stop error.
func TestStopCloudContextErrorClearsIntentionalStop(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              eruncommon.EnvironmentTypeRuntime,
			},
		},
	}

	wantErr := errors.New("aws stop failed")
	app := NewApp(erunUIDeps{
		store: store,
		stopCloudContext: func(_ context.Context, _ string) (eruncommon.CloudContextStatus, error) {
			return eruncommon.CloudContextStatus{}, wantErr
		},
	})
	defer app.shutdown(context.Background())
	app.setCloudContextStatusInCache("managed-cloud", eruncommon.CloudContextStatusRunning)

	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	if _, err := app.StopCloudContext("managed-cloud"); err == nil {
		t.Fatal("expected StopCloudContext to surface stub error")
	}
	if app.isIntentionalStop(selection) {
		t.Fatal("expected intentional-stop marker to clear after StopCloudContext error")
	}
}

// TestTrackExitForLoopGuardTripsAfterCap: back-to-back exits (no pruning
// window elapses) must trip the guard exactly at the cap.
func TestTrackExitForLoopGuardTripsAfterCap(t *testing.T) {
	app := &App{}
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

// TestTryReconnectRefusesAfterDeployFailure pins the deploy-failure gate: when
// an env's open ended in a deploy failure, tryReconnect must not respawn.
// Respawning re-runs `erun open`, re-deploying a broken env — and because every
// tab reconnects independently, that becomes a parallel re-deploy storm. The
// user recovers via the failed-deploy card actions.
func TestTryReconnectRefusesAfterDeployFailure(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{}
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

// TestTryReconnectRefusesWhenActivityDeployFailed covers the case the readyErr
// guard misses: the deploy failed in a separate activity and this open times
// out reaching the never-ready pod, so its own readyErr is clean. tryReconnect
// must still refuse — consulting the activity queue — so the env stops
// hammering itself.
func TestTryReconnectRefusesWhenActivityDeployFailed(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{}
	app.SetEmitter(emits.fn())
	app.activityQueue = newActivityQueueStore(nil, nil)
	failed, _ := app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "petios", Environment: "rihards-develop"})
	if _, ok := app.activityQueue.finish(failed.ID, activityQueueStatusFailed, "helm release failed"); !ok {
		t.Fatal("seed: finish failed deploy returned ok=false")
	}

	respawned := false
	managed := &managedTerminal{
		serial:      9,
		selection:   uiSelection{Tenant: "petios", Environment: "rihards-develop"},
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

// TestTryReconnectAfterHealthyDropRespawns is the counterpart: a session that
// reached a healthy ready and then dropped is a transient drop and must still
// reconnect — its `erun open` finds the deploy current and skips it, so no
// re-deploy storm.
func TestTryReconnectAfterHealthyDropRespawns(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{}
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
	for _, evt := range emits.events(terminalOutputEvent) {
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

// TestSessionTakenOverByAnotherWindowDoesNotReconnect guards the takeover
// handover: when another ERun window re-attaches a persistent pod session,
// this window's `erun open` prints the taken-over notice and exits. The
// desktop must not respawn — that would steal the session straight back and
// the two windows would fight over it — and must surface the take-back
// affordance instead. The deliberate take-back is clicking the env in the
// sidebar.
func TestSessionTakenOverByAnotherWindowDoesNotReconnect(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
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
				[]byte(eruncommon.ShellSessionTakenOverNotice+"\n")...)
			sessionsMu.Lock()
			sessions = append(sessions, session)
			sessionsMu.Unlock()
			return session, nil
		},
	})
	defer app.shutdown(context.Background())
	app.SetEmitter(emits.fn())

	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
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
