package orchestrator

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestOrchestratorPacingMarkerReportsMeasuredStaleness(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := newTestApp(testDeps{})
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	session := newCallRecordingSession()
	app.terminals.seed("agent", 7, session)
	app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 7, name: "agent", startedAt: time.Now().Add(-30 * time.Minute)}
	writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: false, AtUnix: time.Now().Add(-20 * time.Minute).Unix()})

	app.ReconcileOrchestratorPacing()

	texts := orchestratorPacingMarkerTexts(emits)
	if len(texts) != 1 {
		t.Fatalf("expected exactly one marker, got %v", texts)
	}
	if strings.Contains(texts[0], orchestratorPacingStaleAfter.String()) {
		t.Fatalf("marker still names the constant %s instead of the measured gap: %q", orchestratorPacingStaleAfter, texts[0])
	}
	if !strings.Contains(texts[0], "20m") {
		t.Fatalf("expected the marker to name the measured ~20m gap, got %q", texts[0])
	}
}

func TestOrchestratorPacingMarkerDistinguishesADiedTurnFromAQuietOne(t *testing.T) {
	cases := []struct {
		name        string
		writeReport bool
		busy        bool
		wantSubstr  string
	}{
		{"last report said busy: reads as a died turn", true, true, "may have died"},
		{"last report said idle: reads as an ordinary quiet pane", true, false, "no activity report for"},
		{"no report was ever written: reads as never reported", false, false, "since it started"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("HOME", t.TempDir())
			orchestratorPacingNudgeSettle = 0

			app := newTestApp(testDeps{})
			emits := newCapturedEmits()
			app.SetEmitter(emits.fn())

			session := newCallRecordingSession()
			app.terminals.seed("agent", 7, session)
			started := time.Now().Add(-orchestratorPacingStaleAfter - time.Minute)
			app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 7, name: "agent", startedAt: started}
			if tc.writeReport {
				writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: tc.busy, AtUnix: started.Unix()})
			}

			app.ReconcileOrchestratorPacing()

			texts := orchestratorPacingMarkerTexts(emits)
			if len(texts) != 1 {
				t.Fatalf("expected exactly one marker, got %v", texts)
			}
			if !strings.Contains(texts[0], tc.wantSubstr) {
				t.Fatalf("marker %q does not contain %q", texts[0], tc.wantSubstr)
			}
		})
	}
}

func TestOrchestratorPacingLogsEachDecisionReasonOnTransition(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0
	restoreLogOutputAfter(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)

	app := newTestApp(testDeps{})

	freshSession := newCallRecordingSession()
	app.terminals.seed("fresh", 1, freshSession)
	app.orchestrators["fresh"] = &orchestratorSession{id: "fresh", serial: 1, name: "fresh", startedAt: time.Now()}

	app.orchestrators["gone"] = &orchestratorSession{id: "gone", serial: 2, name: "gone", startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute)}

	cappedSession := newCallRecordingSession()
	app.terminals.seed("capped", 3, cappedSession)
	app.orchestrators["capped"] = &orchestratorSession{
		id: "capped", serial: 3, name: "capped",
		startedAt:    time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
		pacingCapped: true,
	}

	nudgedSession := newCallRecordingSession()
	app.terminals.seed("petios", 4, nudgedSession)
	app.orchestrators["petios"] = &orchestratorSession{id: "petios", serial: 4, name: "petios-qa", startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute)}

	app.ReconcileOrchestratorPacing()

	logged := logs.String()
	for _, want := range []string{
		"orchestrator fresh pacing decision=fresh",
		"orchestrator gone pacing decision=not-alive",
		"orchestrator capped pacing decision=already-capped",
		"orchestrator petios (petios-qa) pacing decision=nudge",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("expected %q in the pacing log, got:\n%s", want, logged)
		}
	}

	logs.Reset()
	app.ReconcileOrchestratorPacing()
	if logs.Len() != 0 {
		t.Fatalf("expected no repeated pacing log lines on an unchanged reason, got:\n%s", logs.String())
	}
}

// The nudge history file keys by id, so the log line must carry the id.
func TestOrchestratorPacingLogLineJoinsItsNudgeHistoryRecord(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0
	restoreLogOutputAfter(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)

	app := newTestApp(testDeps{})
	historyPath := app.deps.orchestratorNudgeHistoryPath
	if historyPath == "" {
		t.Fatal("expected the app to resolve a nudge history path")
	}

	session := newCallRecordingSession()
	app.terminals.seed("petios", 4, session)
	app.orchestrators["petios"] = &orchestratorSession{
		id: "petios", serial: 4, name: "petios-qa",
		startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
	}

	app.ReconcileOrchestratorPacing()

	const wantLine = "orchestrator petios (petios-qa) pacing decision=nudge"
	if !strings.Contains(logs.String(), wantLine) {
		t.Fatalf("expected %q in the pacing log, got:\n%s", wantLine, logs.String())
	}
	if _, found, unreadable := orchestratorNudgeHistoryFor(historyPath, "petios"); !found || unreadable {
		t.Fatalf("expected a nudge history record under id %q (found=%v unreadable=%v), so the log line joins its state record",
			"petios", found, unreadable)
	}
	if _, found, _ := orchestratorNudgeHistoryFor(historyPath, "petios-qa"); found {
		t.Fatalf("nudge history must be keyed by the id, not by the display name %q", "petios-qa")
	}
}
