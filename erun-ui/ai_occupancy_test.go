package main

import (
	"context"
	"testing"

	"github.com/adrg/xdg"
	eruncommon "github.com/sophium/erun/erun-common"
)

// newAIOccupancyTestApp isolates the activity-lease cache per test and returns
// an App plus a counter of how many PTYs StartAISession actually launched, so
// each test can assert on spawn count without repeating the stub wiring.
func newAIOccupancyTestApp(t *testing.T) (*App, *int) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	started := 0
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(params startTerminalSessionParams) (terminalSession, error) {
			started++
			return newStubTerminalSession(), nil
		},
	})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app, &started
}

func takeTestLease(t *testing.T, name string) {
	t.Helper()
	if _, err := eruncommon.TakeEnvironmentActivityLease(eruncommon.TakeEnvironmentActivityLeaseParams{
		Tenant:      "erun",
		Environment: "remote",
		Name:        name,
	}); err != nil {
		t.Fatalf("seed lease %q: %v", name, err)
	}
}

// TestStartAISessionReportsOccupancyInsteadOfStartingASecondAgent pins erun#1221:
// opening the AI tab on an environment already held by another job's activity
// lease must not silently launch a competing agent. Unless the caller confirms,
// the start is reported back as occupied and no PTY is spawned.
func TestStartAISessionReportsOccupancyInsteadOfStartingASecondAgent(t *testing.T) {
	app, started := newAIOccupancyTestApp(t)
	takeTestLease(t, "job-fix-1201")
	selection := uiSelection{Tenant: "erun", Environment: "remote"}

	result, err := app.StartAISession(selection, 0, 80, 24, false)
	if err != nil {
		t.Fatalf("StartAISession failed: %v", err)
	}
	assertOccupiedUnconfirmedResult(t, result, *started)

	// Confirmed retries the same start and this time actually launches the AI
	// PTY — the whole point is a deliberate second agent, not a refused one.
	confirmed, err := app.StartAISession(selection, 0, 80, 24, true)
	if err != nil {
		t.Fatalf("confirmed StartAISession failed: %v", err)
	}
	assertConfirmedStartResult(t, confirmed, *started)
}

func assertOccupiedUnconfirmedResult(t *testing.T, result startSessionResult, started int) {
	t.Helper()
	if started != 0 {
		t.Fatalf("expected no PTY to be started while occupied and unconfirmed, got %d", started)
	}
	if result.SessionID != 0 {
		t.Fatalf("expected no session id on an occupied, unconfirmed start, got %d", result.SessionID)
	}
	if len(result.Occupancy) != 1 || result.Occupancy[0].Name != "job-fix-1201" {
		t.Fatalf("expected the held lease to be reported as the occupant, got %+v", result.Occupancy)
	}
}

func assertConfirmedStartResult(t *testing.T, result startSessionResult, started int) {
	t.Helper()
	if started != 1 {
		t.Fatalf("expected the confirmed retry to start exactly one PTY, got %d", started)
	}
	if result.SessionID == 0 {
		t.Fatalf("expected a session id once confirmed, got %+v", result)
	}
	if len(result.Occupancy) != 0 {
		t.Fatalf("expected no occupancy on the confirmed result, got %+v", result.Occupancy)
	}
}

// TestStartAISessionStartsNormallyWithNoHeldLease pins the common path: an
// environment with no agent already working in it shows no occupancy notice
// and the AI tab starts immediately, matching today's behavior.
func TestStartAISessionStartsNormallyWithNoHeldLease(t *testing.T) {
	app, started := newAIOccupancyTestApp(t)

	result, err := app.StartAISession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24, false)
	if err != nil {
		t.Fatalf("StartAISession failed: %v", err)
	}
	if *started != 1 {
		t.Fatalf("expected the unoccupied start to launch a PTY immediately, got %d", *started)
	}
	if len(result.Occupancy) != 0 {
		t.Fatalf("expected no occupancy notice, got %+v", result.Occupancy)
	}
}

// TestStartAISessionReuseSkipsOccupancyCheck pins that reattaching to this
// desktop's own already-tracked AI session never consults leases: reuse is
// not a second agent, so it must never gate on a coexisting job's lease.
func TestStartAISessionReuseSkipsOccupancyCheck(t *testing.T) {
	app, started := newAIOccupancyTestApp(t)
	selection := uiSelection{Tenant: "erun", Environment: "remote"}

	first, err := app.StartAISession(selection, 0, 80, 24, false)
	if err != nil {
		t.Fatalf("first StartAISession failed: %v", err)
	}

	// A job takes a lease only after the AI tab is already live — the case
	// this desktop's own reattach must not treat as a foreign occupant.
	takeTestLease(t, "job-fix-1201")

	again, err := app.StartAISession(selection, 0, 80, 24, false)
	if err != nil {
		t.Fatalf("second StartAISession failed: %v", err)
	}
	if *started != 1 {
		t.Fatalf("expected exactly one PTY across reuse, got %d", *started)
	}
	if again.SessionID != first.SessionID {
		t.Fatalf("expected the reattach to return the same session id, got %d vs %d", again.SessionID, first.SessionID)
	}
	if len(again.Occupancy) != 0 {
		t.Fatalf("expected reuse to skip the occupancy check entirely, got %+v", again.Occupancy)
	}
}
