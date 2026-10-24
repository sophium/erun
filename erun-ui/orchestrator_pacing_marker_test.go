package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

// TestOrchestratorPacingMarkerReportsMeasuredStaleness is the regression test
// for erun#1376's first defect: the marker used to format the
// orchestratorPacingStaleAfter constant (always "10m0s") regardless of how
// long the session had actually been quiet, so a 20-minute outage rendered as
// the ten-minute contract being honoured. This drives the reconciler with a
// report aged ~20 minutes — double the contract — and asserts the marker
// names the measured gap rather than the constant. Quoted both ways: on the
// pre-fix code (marker built from orchestratorPacingStaleAfter) this fails
// because the rendered text is always "10m0s"; post-fix it names the real gap.
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

// TestOrchestratorPacingMarkerDistinguishesADiedTurnFromAQuietOne is the
// regression test for erun#1376's fourth requirement: a session whose last
// report said "busy" and was never followed by an idle one reads differently
// from one that finished its turn and then went quiet, and differently again
// from one that never wrote a report at all. The observed incident had a
// report stuck on busy=true from a turn that died on a dropped connection —
// this is what should have told the operator that from the pane alone.
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

// TestOrchestratorPacingLogsEachDecisionReasonOnTransition is the fix for
// erun#1376's second defect: every branch decideOrchestratorPacing can reach —
// not just the ones that end in a nudge — leaves a durable, findable record of
// the reason. Before this, a stalled pane and a healthy quiet one were
// indistinguishable because nothing recorded which one applied. It also pins
// that a second tick with nothing changed does not repeat any line, since a
// per-tick log would drown the signal for orchestrators that spend most of
// their life "fresh".
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

	// id != name, the shape that was reported: the log names the id first and
	// carries the display name beside it.
	nudgedSession := newCallRecordingSession()
	nudgedKey := orchestratorSessionKey("petios")
	app.sessions[nudgedKey] = &managedTerminal{session: nudgedSession, key: nudgedKey, serial: 4, kind: sessionKindOrchestrator}
	app.orchestrators["petios"] = &orchestratorSession{id: "petios", serial: 4, name: "petios-qa", startedAt: time.Now().Add(-orchestratorPacingStaleAfter - time.Minute)}

	app.reconcileOrchestratorPacing()

	logged := logs.String()
	// The id leads because it is the key the state files use; the display name
	// follows parenthetically only when it differs. The fixtures that keep
	// id == name pin that the common case still reads exactly as it did before,
	// so a redundant "fresh (fresh)" cannot creep in.
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
// without reading config.yaml. The pacing log named the config `name:` while
// orchestrator-nudge-history.json keys by `id:`, so an
// orchestrator whose pair differs — petios / petios-qa on the reporting host —
// appeared under two strings on two surfaces with nothing saying so, and the
// state file read as silently dropping nudges.
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
