package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

// TestOrchestratorPacingMarkerReportsMeasuredStaleness: a report aged ~20
// minutes must render the measured gap, not the orchestratorPacingStaleAfter
// constant ("10m0s"), which would read as the contract being honoured.
func TestOrchestratorPacingMarkerReportsMeasuredStaleness(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{})
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	session := newCallRecordingSession()
	key := orchestratorSessionKey("agent")
	app.sessions[key] = &managedTerminal{session: session, key: key, serial: 7, kind: sessionKindOrchestrator}
	app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 7, name: "agent", startedAt: time.Now().Add(-30 * time.Minute)}
	writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: false, AtUnix: time.Now().Add(-20 * time.Minute).Unix()})

	app.reconcileOrchestratorPacing()

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

// TestOrchestratorPacingMarkerDistinguishesADiedTurnFromAQuietOne: a session
// whose last report said "busy" reads differently from one that finished its
// turn and went quiet, and differently again from one that never reported.
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

			app := NewApp(erunUIDeps{})
			emits := newCapturedEmits()
			app.SetEmitter(emits.fn())

			session := newCallRecordingSession()
			key := orchestratorSessionKey("agent")
			app.sessions[key] = &managedTerminal{session: session, key: key, serial: 7, kind: sessionKindOrchestrator}
			started := time.Now().Add(-orchestratorPacingStaleAfter - time.Minute)
			app.orchestrators["agent"] = &orchestratorSession{id: "agent", serial: 7, name: "agent", startedAt: started}
			if tc.writeReport {
				writeOrchestratorActivity(t, "agent", orchestratorActivity{Busy: tc.busy, AtUnix: started.Unix()})
			}

			app.reconcileOrchestratorPacing()

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

// TestOrchestratorPacingLogsEachDecisionReasonOnTransition: every branch
// decideOrchestratorPacing can reach, not just a nudge, logs its reason, and a
// second tick with nothing changed repeats no line.
func TestOrchestratorPacingLogsEachDecisionReasonOnTransition(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0
	restoreLogOutputAfter(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)

	app := NewApp(erunUIDeps{})

	freshSession := newCallRecordingSession()
	freshKey := orchestratorSessionKey("fresh")
	app.sessions[freshKey] = &managedTerminal{session: freshSession, key: freshKey, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["fresh"] = &orchestratorSession{id: "fresh", serial: 1, name: "fresh", startedAt: time.Now()}

	app.orchestrators["gone"] = &orchestratorSession{id: "gone", serial: 2, name: "gone", startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute)}

	cappedSession := newCallRecordingSession()
	cappedKey := orchestratorSessionKey("capped")
	app.sessions[cappedKey] = &managedTerminal{session: cappedSession, key: cappedKey, serial: 3, kind: sessionKindOrchestrator}
	app.orchestrators["capped"] = &orchestratorSession{
		id: "capped", serial: 3, name: "capped",
		startedAt:    time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
		pacingCapped: true,
	}

	// id != name: the log names the id first and carries the display name
	// beside it.
	nudgedSession := newCallRecordingSession()
	nudgedKey := orchestratorSessionKey("petios")
	app.sessions[nudgedKey] = &managedTerminal{session: nudgedSession, key: nudgedKey, serial: 4, kind: sessionKindOrchestrator}
	app.orchestrators["petios"] = &orchestratorSession{id: "petios", serial: 4, name: "petios-qa", startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute)}

	app.reconcileOrchestratorPacing()

	logged := logs.String()
	// The id leads because it is the key the state files use; the display name
	// follows parenthetically only when it differs, so a redundant
	// "fresh (fresh)" cannot creep in.
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

	// A second tick with none of these reasons changed must not repeat any line.
	logs.Reset()
	app.reconcileOrchestratorPacing()
	if logs.Len() != 0 {
		t.Fatalf("expected no repeated pacing log lines on an unchanged reason, got:\n%s", logs.String())
	}
}

// TestOrchestratorPacingLogLineJoinsItsNudgeHistoryRecord pins that a pacing
// log line and a nudge history record about the same orchestrator are matchable
// without reading config.yaml, even when the orchestrator's id and name
// differ (orchestrator-nudge-history.json keys by id).
func TestOrchestratorPacingLogLineJoinsItsNudgeHistoryRecord(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	orchestratorPacingNudgeSettle = 0
	restoreLogOutputAfter(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)

	app := NewApp(erunUIDeps{})
	historyPath := app.deps.orchestratorNudgeHistoryPath
	if historyPath == "" {
		t.Fatal("expected the app to resolve a nudge history path")
	}

	session := newCallRecordingSession()
	key := orchestratorSessionKey("petios")
	app.sessions[key] = &managedTerminal{session: session, key: key, serial: 4, kind: sessionKindOrchestrator}
	app.orchestrators["petios"] = &orchestratorSession{
		id: "petios", serial: 4, name: "petios-qa",
		startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute),
	}

	app.reconcileOrchestratorPacing()

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
