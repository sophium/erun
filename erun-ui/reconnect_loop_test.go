package main

import (
	"context"
	"encoding/base64"
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
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "ctx"},
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
