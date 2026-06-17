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

// TestStartSessionStopsReconnectingAfterRepeatedFailures locks the
// fast-exit loop guard from issue #361. Before this gate, a managed
// `erun open` PTY whose cluster kept tearing down the freshly-spawned
// pod (helm rollout timeouts, MCP port-forward races against a
// terminating EC2) would respawn indefinitely — each respawn re-ran
// `erun open`, deployed, failed in a few seconds, and exited, leaving
// the user with N stacked failed-deploy entries in the activity drawer
// and a wall of reconnect markers. tryReconnect now stops after the
// cap and emits a single retry marker via the terminal-output channel
// so the user sees an explicit stop.
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

	// reconnectLoopMaxExits is the production cap. Closing the
	// session that many times must each trigger a respawn (so we end
	// up with maxExits+1 stubs in the slice — original plus
	// maxExits respawns). The final close past the cap must NOT
	// trigger a respawn.
	const closes = reconnectLoopMaxExits + 1
	for i := 0; i < closes; i++ {
		expected := i + 1
		waitForSessionCount(t, &sessionsMu, &sessions, expected, 2*time.Second)
		sessionsMu.Lock()
		current := sessions[expected-1]
		sessionsMu.Unlock()
		_ = current.Close()
	}

	// Give tryReconnect a beat to run for the over-cap close. If the
	// gate broke, a fresh session would land in the slice; the assert
	// below catches it.
	time.Sleep(200 * time.Millisecond)
	sessionsMu.Lock()
	got := len(sessions)
	sessionsMu.Unlock()
	if got != closes {
		t.Fatalf("expected loop guard to cap session count at %d (original + %d respawns), got %d",
			closes, reconnectLoopMaxExits, got)
	}

	// The retry marker is emitted through terminal-output as a
	// base64-encoded ANSI line. Decode the captured payloads and
	// look for the production marker text.
	if !sawLoopMarker(emits) {
		t.Fatal("expected reconnect-loop marker on terminal-output channel after cap")
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

func sawLoopMarker(emits *capturedEmits) bool {
	const needle = "stopped reconnecting after repeated failures"
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

// TestTrackExitForLoopGuardPrunesOldExits documents the window-based
// pruning: a single exit after a long-running session must not trip
// the cap. Without pruning, a managed terminal that ran for hours and
// then exited cleanly would refuse to reconnect on its very first
// failure because counters were stale.
func TestTrackExitForLoopGuardPrunesOldExits(t *testing.T) {
	app := &App{}
	managed := &managedTerminal{}

	// Backfill exits older than the window. Pruning should drop all
	// of them on the next call, leaving a single fresh entry — well
	// under the cap.
	older := time.Now().Add(-2 * reconnectLoopWindow)
	managed.recentExits = []time.Time{older, older, older, older}

	if got := app.trackExitForLoopGuard(managed); got {
		t.Fatalf("trackExitForLoopGuard tripped on stale exits; want false (pruned)")
	}
	if len(managed.recentExits) != 1 {
		t.Fatalf("expected pruning to leave 1 entry, got %d", len(managed.recentExits))
	}
}

// TestStopCloudContextSuppressesReconnect locks the intentional-stop
// gate from issue #412. Before this gate, clicking the Power button
// in the titlebar fired `StopCloudContext` and the kubectl session
// died moments later as the EC2 instance entered `stopping`. The
// reconnect loop then re-ran `erun open`, whose CloudContextPreflight
// called StartCloudContext and immediately undid the user's stop.
// `shouldRespawnForCloudContext` already blocked reconnect when the
// on-disk cloud-context status was non-running, but the status poller
// writes that field on its own cadence so a race window stayed wide
// open. StopCloudContext now records an intent marker the moment the
// AWS call returns; shouldRespawnForCloudContext consults it before
// reading the on-disk status, so the marker closes the race without
// waiting for the poller.
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
		// Stub stop so the test does not need a real AWS client. Pretend the
		// instance entered `stopping` but the cache stays at running so the
		// shouldRespawnForCloudContext on-disk branch alone would NOT block
		// the respawn — only the intent marker can.
		stopCloudContext: func(_ context.Context, name string) (eruncommon.CloudContextStatus, error) {
			return eruncommon.CloudContextStatus{
				CloudContextConfig: eruncommon.CloudContextConfig{Name: name, KubernetesContext: "cluster-cloud"},
				Status:             "stopping",
			}, nil
		},
	})
	defer app.shutdown(context.Background())
	// Seed the cache so shouldRespawnForCloudContext's on-disk fallback
	// would allow respawn if the intent marker were not in place.
	app.setCloudContextStatusInCache("managed-cloud", eruncommon.CloudContextStatusRunning)

	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	if _, err := app.StartSession(selection, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	waitForSessionCount(t, &sessionsMu, &sessions, 1, 2*time.Second)

	// User clicks the Power button. StopCloudContext marks intent BEFORE
	// the AWS call, then sets the cache to stopping — but we keep the
	// returned status's effect on the cache irrelevant by leaving the
	// stub above with the same `Stopping` value. The poller has not run.
	if _, err := app.StopCloudContext("managed-cloud"); err != nil {
		t.Fatalf("StopCloudContext failed: %v", err)
	}

	// Now drop the live PTY the way the kubectl session dies when the
	// API server goes away. Without the marker, tryReconnect respawns
	// and a second stub appears in `sessions`.
	sessionsMu.Lock()
	first := sessions[0]
	sessionsMu.Unlock()
	_ = first.Close()

	// Give tryReconnect a beat. The deadline mirrors the cloud-context
	// gate test so a regression races for ~1s.
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

// TestClearIntentionalStopForCloudContext documents the recovery
// path: once the user explicitly resumes (via the titlebar Start
// button, the sidebar re-click that runs `erun open`, or a successful
// idle-stop clear), the intent marker must clear so a subsequent
// kubectl-drop reconnects normally. Without this clear, the env
// would be permanently stuck in "no reconnect" after every Stop
// click. StartCloudContext, ensureLinkedCloudContextRunning, and the
// idle-stop clear path all funnel through
// clearIntentionalStopForCloudContext.
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

	// The recovery callers (StartCloudContext, ensureLinkedCloudContextRunning,
	// idle-stop's clear path) all clear the marker via the same helper. Test
	// the helper directly so the test does not need to stand up a full AWS
	// stub harness with valid instance IDs.
	app.clearIntentionalStopForCloudContext("managed-cloud")
	if app.isIntentionalStop(selection) {
		t.Fatal("expected intentional-stop marker to clear after clearIntentionalStopForCloudContext")
	}
}

// TestStopCloudContextErrorClearsIntentionalStop locks the failure
// path: if the AWS Stop call returns an error, the cluster is still
// up, so a subsequent kubectl drop must reconnect normally. Leaving
// the marker behind would silently disable reconnect after a
// transient stop error.
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

// TestTrackExitForLoopGuardTripsAfterCap exercises the threshold edge
// without a real PTY. Calling trackExitForLoopGuard maxExits+1 times
// in immediate succession must return false for the first maxExits
// calls and true on the next, matching the cap on tryReconnect.
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

// TestTryReconnectRefusesAfterDeployFailure pins #447: when an env's open
// ended in a deploy failure (signalReady recorded an error from
// `==> Deploy failed`), tryReconnect must NOT respawn. Respawning re-runs
// `erun open`, re-deploying a broken env — and because every tab (ERun + AI)
// reconnects independently, that becomes a parallel re-deploy storm. The user
// recovers via the failed-deploy card actions or by reopening the env.
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
// guard misses: the deploy failed in a *separate* activity and this open exits
// with an MCP port-forward timeout / pod-not-ready (no `==> Deploy failed`
// readyErr) while trying to reach the never-ready pod. tryReconnect must still
// refuse, consulting the activity queue, so the env stops hammering itself.
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
// reached a healthy ready (readyErr == nil) and then dropped is a transient
// drop and must still reconnect. Its `erun open` finds the deploy already
// current and skips it, so no re-deploy storm.
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

// TestSessionTakenOverByAnotherWindowDoesNotReconnect locks the takeover
// handover from #478: when another ERun window re-attaches a persistent pod
// session, this window's `erun open` prints the stable taken-over notice and
// exits cleanly. The desktop must NOT respawn — a respawn would re-attach and
// steal the session straight back, and the two windows would fight over it —
// and must surface the take-back affordance marker instead. Clicking the env
// in the sidebar is the deliberate take-back (it starts a fresh session).
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
			// The PTY replays `erun open`'s output: the kubectl attach line,
			// then the taken-over notice another window's attach triggered.
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

	// Let streamSession scan the notice line, then end the PTY the way the
	// CLI does after printing it.
	waitForTakenOverFlag(t, app, 2*time.Second)
	sessionsMu.Lock()
	current := sessions[0]
	sessionsMu.Unlock()
	_ = current.Close()

	time.Sleep(200 * time.Millisecond)
	sessionsMu.Lock()
	got := len(sessions)
	sessionsMu.Unlock()
	if got != 1 {
		t.Fatalf("takeover must not respawn: expected 1 session, got %d", got)
	}
	if !sawTakenOverMarker(emits) {
		t.Fatal("expected taken-over marker on terminal-output channel")
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

func sawTakenOverMarker(emits *capturedEmits) bool {
	const needle = "re-attached in another ERun window"
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
