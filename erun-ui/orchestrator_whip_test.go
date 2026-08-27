package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrg/xdg"
	eruncommon "github.com/sophium/erun/erun-common"
)

// isolateOrchestratorWhipConfig points the shared xdg config dir at a temp
// directory for one test and forces the adrg/xdg package (which caches its
// resolved base directories at process init, not per read) to re-resolve them
// against the new env var -- the same fix erun-common's activity cache tests
// already need for the same underlying library.
func isolateOrchestratorWhipConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(func() {
		xdg.Reload()
		setOrchestratorWhipConfig(eruncommon.ResolveWhipConfig(nil))
	})
}

func writeRootConfig(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(xdg.ConfigHome, "erun")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write root config: %v", err)
	}
}

// TestRefreshOrchestratorWhipConfigUnconfiguredKeepsTodaysText is half of the
// "configurable message, default-preserving" contract: an install with no
// whip section in ~/.erun/config.yaml (or no config file at all) must resolve
// to exactly today's constant text -- behaviour does not change for anyone
// who configures nothing.
func TestRefreshOrchestratorWhipConfigUnconfiguredKeepsTodaysText(t *testing.T) {
	isolateOrchestratorWhipConfig(t)

	refreshOrchestratorWhipConfig()

	got := getOrchestratorWhipConfig()
	if got.Message != orchestratorPacingNudgeText {
		t.Fatalf("unconfigured install got message %q, want today's text %q", got.Message, orchestratorPacingNudgeText)
	}
	if got.MaxNudges != orchestratorPacingMaxNudges {
		t.Fatalf("unconfigured install got MaxNudges=%d, want %d", got.MaxNudges, orchestratorPacingMaxNudges)
	}
}

// TestRefreshOrchestratorWhipConfigOverrideWins is the other half: a
// configured message in ~/.erun/config.yaml overrides the default, and takes
// effect on the next reconciler tick (refreshOrchestratorWhipConfig) without a
// rebuild or restart.
func TestRefreshOrchestratorWhipConfigOverrideWins(t *testing.T) {
	isolateOrchestratorWhipConfig(t)
	writeRootConfig(t, "whip:\n  message: \"Stop dawdling and ship it.\"\n  maxnudges: 3\n")

	refreshOrchestratorWhipConfig()

	got := getOrchestratorWhipConfig()
	if got.Message != "Stop dawdling and ship it." {
		t.Fatalf("got message %q, want the configured override", got.Message)
	}
	if got.MaxNudges != 3 {
		t.Fatalf("got MaxNudges=%d, want the configured override 3", got.MaxNudges)
	}
}

// TestWhipOrchestratorNowIgnoresFreshnessButRespectsCap is the red-then-green
// contract for the manual per-entry whip: a fresh
// session (moved a second ago) is still pushed when explicitly whipped, but an
// already-capped one stays capped rather than getting a bypassed nudge. Before
// whipOrchestratorNow existed there was no way to assert this at all -- the
// automatic reconciler's decideOrchestratorPacing always passed explicit=false,
// so a fresh session was unconditionally skipped (red: this exact scenario had
// no code path to reach). Quoting both:
//
//	red:   ./... has no whipOrchestratorNow symbol; build fails
//	green: fresh session pushed=true, capped session pushed=false (below)
func TestWhipOrchestratorNowIgnoresFreshnessButRespectsCap(t *testing.T) {
	isolateOrchestratorWhipConfig(t)
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})

	freshSession := newCallRecordingSession()
	freshKey := orchestratorSessionKey("fresh")
	app.sessions[freshKey] = &managedTerminal{session: freshSession, key: freshKey, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["fresh"] = &orchestratorSession{id: "fresh", serial: 1, name: "fresh", startedAt: time.Now()}

	decision, reason, err := app.whipOrchestratorNow("fresh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != orchestratorPacingNudge || reason != orchestratorPacingReasonNudge {
		t.Fatalf("explicit whip on a fresh session: got decision=%v reason=%v, want nudge/nudge", decision, reason)
	}
	if len(freshSession.Calls()) != 2 {
		t.Fatalf("expected the explicit whip to write the nudge text and its CR, got %v", freshSession.Calls())
	}

	cappedSession := newCallRecordingSession()
	cappedKey := orchestratorSessionKey("capped")
	app.sessions[cappedKey] = &managedTerminal{session: cappedSession, key: cappedKey, serial: 2, kind: sessionKindOrchestrator}
	app.orchestrators["capped"] = &orchestratorSession{id: "capped", serial: 2, name: "capped", startedAt: time.Now(), pacingCapped: true}

	decision, reason, err = app.whipOrchestratorNow("capped")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != orchestratorPacingNone || reason != orchestratorPacingReasonAlreadyCapped {
		t.Fatalf("explicit whip on a capped session: got decision=%v reason=%v, want none/already-capped", decision, reason)
	}
	if len(cappedSession.Calls()) != 0 {
		t.Fatalf("expected the cap to still block the write, got %v", cappedSession.Calls())
	}
}

// TestWhipOrchestratorNowUnknownIDErrors gives a definite answer instead of a
// silent no-op when the id names no live orchestrator.
func TestWhipOrchestratorNowUnknownIDErrors(t *testing.T) {
	isolateOrchestratorWhipConfig(t)
	app := NewApp(erunUIDeps{})
	if _, _, err := app.whipOrchestratorNow("does-not-exist"); err == nil {
		t.Fatal("expected an error for an unknown orchestrator id")
	}
}

// TestWhipAllOrchestratorsNowNamesEveryOutcome is the section-level whip's
// visible-record contract: every orchestrator appears in the result, named
// with its own decision and reason -- not just an aggregate "ran" signal.
func TestWhipAllOrchestratorsNowNamesEveryOutcome(t *testing.T) {
	isolateOrchestratorWhipConfig(t)
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})

	aliveSession := newCallRecordingSession()
	aliveKey := orchestratorSessionKey("alive")
	app.sessions[aliveKey] = &managedTerminal{session: aliveSession, key: aliveKey, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["alive"] = &orchestratorSession{id: "alive", serial: 1, name: "alive", startedAt: time.Now()}

	// Not alive: no managed session registered for this orchestrator at all.
	app.orchestrators["gone"] = &orchestratorSession{id: "gone", serial: 2, name: "gone", startedAt: time.Now()}

	outcomes := app.whipAllOrchestratorsNow()
	if len(outcomes) != 2 {
		t.Fatalf("expected an outcome for every orchestrator, got %d: %+v", len(outcomes), outcomes)
	}
	byID := map[string]orchestratorWhipOutcome{}
	for _, outcome := range outcomes {
		byID[outcome.id] = outcome
	}
	if got := byID["alive"]; got.decision != orchestratorPacingNudge || got.reason != orchestratorPacingReasonNudge {
		t.Fatalf("alive orchestrator: got %+v, want nudge/nudge", got)
	}
	if got := byID["gone"]; got.decision != orchestratorPacingNone || got.reason != orchestratorPacingReasonNotAlive {
		t.Fatalf("gone orchestrator: got %+v, want none/not-alive", got)
	}
}
