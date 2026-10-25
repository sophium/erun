package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/config"
	orchcmd "github.com/sophium/erun/erun-common/command/orchestrator"
)

// adrg/xdg caches base dirs at init, so Reload after changing XDG_CONFIG_HOME.
func isolateOrchestratorWhipConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(func() {
		xdg.Reload()
		setOrchestratorWhipConfig(orchcmd.ResolveWhipConfig(nil))
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

// A configured whip takes effect on the next refresh, without a restart.
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

func TestWhipOrchestratorsNowNamesEveryOutcome(t *testing.T) {
	isolateOrchestratorWhipConfig(t)
	orchestratorPacingNudgeSettle = 0

	app := newTestApp(testDeps{})

	aliveSession := newCallRecordingSession()
	app.terminals.seed("alive", 1, aliveSession)
	app.orchestrators["alive"] = &orchestratorSession{id: "alive", serial: 1, name: "alive", startedAt: time.Now()}

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

// An explicit whip shares the pacer's cap but keeps its own history.
func TestWhipOrchestratorsNowRecordsExplicitWhipHistorySeparately(t *testing.T) {
	isolateOrchestratorWhipConfig(t)
	orchestratorPacingNudgeSettle = 0

	app := newTestApp(testDeps{})
	session := newCallRecordingSession()
	app.terminals.seed("agent", 1, session)
	// An automatic pass would skip this fresh session; an explicit whip does not.
	app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 1, name: "agent", startedAt: time.Now()}

	outcomes := app.whipOrchestratorsNow(map[string]struct{}{"agent": {}})
	if len(outcomes) != 1 || outcomes[0].decision != orchestratorPacingNudge {
		t.Fatalf("expected the explicit whip to push despite fresh activity, got %+v", outcomes)
	}

	_, unlock := lockTerminalForTest(app)
	s := app.orchestrators["agent"]
	unlock()
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

// An explicit whip names a configured orchestrator with no session instead of omitting it.
func TestWhipOrchestratorsNowNamesAConfiguredOrchestratorWithNoSession(t *testing.T) {
	isolateOrchestratorWhipConfig(t)
	orchestratorPacingNudgeSettle = 0

	app := newTestApp(testDeps{store: stubUIStore{config: &config.ERunConfig{
		Orchestrators: []config.OrchestratorConfig{
			{ID: "opened", Name: "opened"},
			{ID: "never-opened", Name: "never-opened"},
		},
	}}})

	app.terminals.seed("opened", 1, newCallRecordingSession())
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

// Asserts on writes, so a filter that only trimmed the report still fails.
func TestWhipOrchestratorsNowOnlyPushesRequestedOrchestrators(t *testing.T) {
	isolateOrchestratorWhipConfig(t)
	orchestratorPacingNudgeSettle = 0

	app := newTestApp(testDeps{})

	wantedSession := newCallRecordingSession()
	app.terminals.seed("wanted", 1, wantedSession)
	app.orchestrators["wanted"] = &orchestratorSession{id: "wanted", serial: 1, name: "wanted", startedAt: time.Now()}

	unwantedSession := newCallRecordingSession()
	app.terminals.seed("unwanted", 2, unwantedSession)
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

func TestListWhipOrchestratorTargetsUnionsSessionsAndConfigs(t *testing.T) {
	app := newTestApp(testDeps{store: stubUIStore{config: &config.ERunConfig{
		Orchestrators: []config.OrchestratorConfig{
			{ID: "opened", Name: "opened"},
			{ID: "never-opened", Name: "never-opened"},
		},
	}}})
	app.terminals.seed("opened", 1, newCallRecordingSession())
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
