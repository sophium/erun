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

// TestWhipOrchestratorsNowNamesEveryOutcome is the section-level whip's
// visible-record contract: every requested orchestrator appears in the
// result, named with its own decision and reason -- not just an aggregate
// "ran" signal.
func TestWhipOrchestratorsNowNamesEveryOutcome(t *testing.T) {
	isolateOrchestratorWhipConfig(t)
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})

	aliveSession := newCallRecordingSession()
	aliveKey := orchestratorSessionKey("alive")
	app.sessions[aliveKey] = &managedTerminal{session: aliveSession, key: aliveKey, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["alive"] = &orchestratorSession{id: "alive", serial: 1, name: "alive", startedAt: time.Now()}

	// Not alive: no managed session registered for this orchestrator at all.
	app.orchestrators["gone"] = &orchestratorSession{id: "gone", serial: 2, name: "gone", startedAt: time.Now()}

	outcomes := app.whipOrchestratorsNow(map[string]struct{}{"alive": {}, "gone": {}})
	if len(outcomes) != 2 {
		t.Fatalf("expected an outcome for every requested orchestrator, got %d: %+v", len(outcomes), outcomes)
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

// TestWhipOrchestratorsNowRecordsExplicitWhipHistorySeparately is the other
// half of the issue: an explicit, operator-triggered whip goes through the
// same sendOrchestratorPacingNudge write the automatic pacer uses (so it
// still costs against the shared cap, unchanged), but must register in its
// own cumulative pacingWhipCount/pacingLastWhipAtUnix history rather than
// being indistinguishable from an automatic nudge -- or invisible, which was
// the reported defect.
func TestWhipOrchestratorsNowRecordsExplicitWhipHistorySeparately(t *testing.T) {
	isolateOrchestratorWhipConfig(t)
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})
	session := newCallRecordingSession()
	key := orchestratorSessionKey("agent")
	app.sessions[key] = &managedTerminal{session: session, key: key, serial: 1, kind: sessionKindOrchestrator}
	// Fresh activity: an automatic pass would never nudge this session, but an
	// explicit whip ignores staleness and pushes it anyway.
	app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 1, name: "agent", startedAt: time.Now()}

	outcomes := app.whipOrchestratorsNow(map[string]struct{}{"agent": {}})
	if len(outcomes) != 1 || outcomes[0].decision != orchestratorPacingNudge {
		t.Fatalf("expected the explicit whip to push despite fresh activity, got %+v", outcomes)
	}

	app.mu.Lock()
	s := app.orchestrators["agent"]
	app.mu.Unlock()
	if s.pacingWhipCount != 1 || s.pacingLastWhipAtUnix == 0 {
		t.Fatalf("expected the explicit whip to be recorded in its own history, got count=%d lastAt=%d", s.pacingWhipCount, s.pacingLastWhipAtUnix)
	}
	if s.pacingAutoNudgeCount != 0 {
		t.Fatalf("expected an explicit whip to leave the automatic-nudge history untouched, got %d", s.pacingAutoNudgeCount)
	}
	if s.pacingNudgeCount != 1 {
		t.Fatalf("expected the explicit whip to still cost against the shared cap gauge, got %d", s.pacingNudgeCount)
	}
}

// A configured orchestrator the desktop never opened has no session, and
// orchestratorPacingRows only enumerates sessions. Naming it anyway is the
// whole contract of an explicit whip: an omitted target reads as "not
// considered", where a skip names a reason the operator can act on.
func TestWhipOrchestratorsNowNamesAConfiguredOrchestratorWithNoSession(t *testing.T) {
	isolateOrchestratorWhipConfig(t)
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{store: stubUIStore{config: &eruncommon.ERunConfig{
		Orchestrators: []eruncommon.OrchestratorConfig{
			{ID: "opened", Name: "opened"},
			{ID: "never-opened", Name: "never-opened"},
		},
	}}})

	key := orchestratorSessionKey("opened")
	app.sessions[key] = &managedTerminal{session: newCallRecordingSession(), key: key, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["opened"] = &orchestratorSession{id: "opened", serial: 1, name: "opened", startedAt: time.Now()}

	byID := map[string]orchestratorWhipOutcome{}
	for _, outcome := range app.whipOrchestratorsNow(map[string]struct{}{"opened": {}, "never-opened": {}}) {
		byID[outcome.id] = outcome
	}

	got, named := byID["never-opened"]
	if !named {
		t.Fatalf("a configured orchestrator with no session was omitted from the report entirely, got %+v", byID)
	}
	if got.decision != orchestratorPacingNone || got.reason != orchestratorPacingReasonNotAlive {
		t.Fatalf("never-opened orchestrator: got %+v, want none/not-alive", got)
	}
	if _, named := byID["opened"]; !named {
		t.Fatalf("the orchestrator holding a live session went missing, got %+v", byID)
	}
}

// TestWhipOrchestratorsNowOnlyPushesRequestedOrchestrators is the regression
// test for the desktop's own blast-radius bug (erun#1700): an orchestrator not
// in the requested set must not be nudged at all -- not merely omitted from
// the report after being pushed anyway. It asserts against the recording
// session's own write, not just the returned outcome list, so a filter that
// only trimmed the report (while still pushing everyone) would still fail
// this test.
func TestWhipOrchestratorsNowOnlyPushesRequestedOrchestrators(t *testing.T) {
	isolateOrchestratorWhipConfig(t)
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})

	wantedSession := newCallRecordingSession()
	wantedKey := orchestratorSessionKey("wanted")
	app.sessions[wantedKey] = &managedTerminal{session: wantedSession, key: wantedKey, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["wanted"] = &orchestratorSession{id: "wanted", serial: 1, name: "wanted", startedAt: time.Now()}

	unwantedSession := newCallRecordingSession()
	unwantedKey := orchestratorSessionKey("unwanted")
	app.sessions[unwantedKey] = &managedTerminal{session: unwantedSession, key: unwantedKey, serial: 2, kind: sessionKindOrchestrator}
	app.orchestrators["unwanted"] = &orchestratorSession{id: "unwanted", serial: 2, name: "unwanted", startedAt: time.Now()}

	outcomes := app.whipOrchestratorsNow(map[string]struct{}{"wanted": {}})
	if len(outcomes) != 1 || outcomes[0].id != "wanted" {
		t.Fatalf("expected only the requested orchestrator in the report, got %+v", outcomes)
	}
	if outcomes[0].decision != orchestratorPacingNudge {
		t.Fatalf("expected the requested orchestrator to actually be nudged, got %+v", outcomes[0])
	}
	if len(wantedSession.Calls()) == 0 {
		t.Fatal("expected the requested orchestrator's session to have been written to")
	}
	if calls := unwantedSession.Calls(); len(calls) != 0 {
		t.Fatalf("an orchestrator outside the requested set was written to -- got %d write(s): %v", len(calls), calls)
	}
}

// TestListWhipOrchestratorTargetsUnionsSessionsAndConfigs is WhipTargets'
// population contract for orchestrators: a live session and a configured-but-
// never-opened orchestrator both appear, deduplicated by id, so "select all
// orchestrators" can offer every orchestrator the operator could otherwise
// pick individually.
func TestListWhipOrchestratorTargetsUnionsSessionsAndConfigs(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{config: &eruncommon.ERunConfig{
		Orchestrators: []eruncommon.OrchestratorConfig{
			{ID: "opened", Name: "opened"},
			{ID: "never-opened", Name: "never-opened"},
		},
	}}})
	key := orchestratorSessionKey("opened")
	app.sessions[key] = &managedTerminal{session: newCallRecordingSession(), key: key, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["opened"] = &orchestratorSession{id: "opened", serial: 1, name: "opened", startedAt: time.Now()}

	targets := app.listWhipOrchestratorTargets()
	ids := map[string]bool{}
	for _, target := range targets {
		ids[target.id] = true
	}
	if !ids["opened"] || !ids["never-opened"] {
		t.Fatalf("expected both the live session and the configured-only orchestrator, got %+v", targets)
	}
	if len(targets) != 2 {
		t.Fatalf("expected exactly one row per orchestrator id, got %+v", targets)
	}
}
