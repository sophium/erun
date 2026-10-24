package main

import (
	"bytes"
	"context"
	"log"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The gap this covers: reconcileOrchestratorPacing iterated the desktop's own
// session map, so a configured orchestrator whose session this desktop never
// launched -- one started in a terminal, or by a previous desktop instance --
// had no row, was never reconciled, and never appeared in this log at all. Its
// nudge count simply froze, indistinguishable from a session that needed no
// nudge. The log's whole purpose is that a quiet pane and a suppressed one can
// be told apart from it, and an orchestrator that is never evaluated is less
// legible than one that is suppressed -- the inverse of what it exists for.
//
// The reason is stated in full in the assertion below on purpose: it is the
// transport's own reason, never "not-alive", because these sessions are usually
// alive and reporting.
func TestOrchestratorPacingLogsAConfiguredOrchestratorItCannotPace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	restoreLogOutputAfter(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	// One orchestrator this desktop holds a session for, so the contrast is in
	// the same log rather than asserted from a different run.
	createOrchestratorNamed(t, app, "held")
	heldSession := newCallRecordingSession()
	heldKey := orchestratorSessionKey("held")
	app.sessions[heldKey] = &managedTerminal{session: heldSession, key: heldKey, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["held"] = &orchestratorSession{id: "held", serial: 1, name: "held", startedAt: time.Now()}

	// And one it does not, which is the case that used to be silent.
	createOrchestratorNamed(t, app, "external")

	app.reconcileOrchestratorPacing()

	logged := logs.String()
	for _, want := range []string{
		"orchestrator held pacing decision=fresh",
		"orchestrator external pacing decision=unreachable-from-transport quiet=unknown",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("expected %q in the pacing log, got:\n%s", want, logged)
		}
	}
	// Distinguishable from the two states it used to be conflated with: a
	// session that is genuinely fresh, and one that has already been capped.
	// The unreached orchestrator never reported at all, so neither applies to
	// it, and saying either would be a claim nobody here observed.
	for _, unwanted := range []string{
		"orchestrator external pacing decision=fresh",
		"orchestrator external pacing decision=already-capped",
	} {
		if strings.Contains(logged, unwanted) {
			t.Fatalf("expected no %q, got:\n%s", unwanted, logged)
		}
	}

	// Same once-per-transition rule as every other row: a tick with nothing
	// changed repeats nothing. Without that, this population -- which never
	// changes reason -- would write a line every 15 seconds forever.
	logs.Reset()
	app.reconcileOrchestratorPacing()
	if strings.Contains(logs.String(), "orchestrator external pacing") {
		t.Fatalf("expected the unreachable decision not to repeat on an unchanged tick, got:\n%s", logs.String())
	}
}

// The other half of the line's honesty: when the orchestrator's own hooks ARE
// reporting, the quiet period is the age of that report rather than "unknown".
// The report is what makes it visible to the desktop in the first place, so a
// line that could not measure it would be the least useful case named.
func TestOrchestratorPacingUnmanagedRowReportsTheAgeOfItsOwnReport(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	restoreLogOutputAfter(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)

	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	createOrchestratorNamed(t, app, "external")
	writeOrchestratorActivity(t, "external", orchestratorActivity{
		Busy:   false,
		AtUnix: time.Now().Add(-20 * time.Minute).Unix(),
	})

	app.reconcileOrchestratorPacing()

	// The report's own age, to the second: pinning the exact instant would only
	// re-measure the clock this test runs on.
	want := regexp.MustCompile(`orchestrator external pacing decision=unreachable-from-transport quiet=20m\d+s`)
	if !want.MatchString(logs.String()) {
		t.Fatalf("expected %s in the pacing log, got:\n%s", want, logs.String())
	}
}

// Nothing is attempted against a session the desktop does not own: the decision
// is none/unreachable, and it is reached before any of the acting paths (a
// rearm, a cap, a write into a pty) are considered. Nudging it is not possible
// from here, and pretending otherwise would be worse than the gap.
func TestOrchestratorPacingNeverNudgesAConfiguredOrchestratorItDoesNotOwn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	createOrchestratorNamed(t, app, "external")
	writeOrchestratorActivity(t, "external", orchestratorActivity{
		Busy:   false,
		AtUnix: time.Now().Add(-2 * time.Hour).Unix(),
	})

	rows := app.orchestratorPacingUnmanagedRows(map[string]struct{}{})
	if len(rows) != 1 || rows[0].id != "external" || !rows[0].unmanaged {
		t.Fatalf("expected one unmanaged row for the configured orchestrator, got %+v", rows)
	}
	decision, reason := app.reconcileOrchestratorPacingOne(rows[0], time.Now(), false)
	if decision != orchestratorPacingNone || reason != orchestratorPacingReasonUnreachable {
		t.Fatalf("expected none/unreachable for a session this desktop does not own, got %v/%v", decision, reason)
	}
	// An explicit whip does not change that either: the operator asking does
	// not create a pty to write into.
	if decision, reason := app.reconcileOrchestratorPacingOne(rows[0], time.Now(), true); decision != orchestratorPacingNone || reason != orchestratorPacingReasonUnreachable {
		t.Fatalf("expected none/unreachable for an explicit whip too, got %v/%v", decision, reason)
	}
}

// The hover card's could-not half. A configured orchestrator whose own hooks
// are reporting, but which this desktop holds no session for, is being driven
// somewhere else -- and erun will never pace it from here. Its nudge count
// stays frozen at zero, which is exactly what a freshly checked, needing-nothing
// orchestrator looks like, so the card has to be able to say which one this is.
func TestListOrchestratorsMarksAConfiguredOrchestratorItCannotPace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	reported := createOrchestratorNamed(t, app, "external")
	quiet := createOrchestratorNamed(t, app, "stopped")
	writeOrchestratorActivity(t, reported, orchestratorActivity{Busy: true, AtUnix: time.Now().Add(-time.Minute).Unix()})
	// Old enough that the pacer would no longer be deciding for it either.
	writeOrchestratorActivity(t, quiet, orchestratorActivity{Busy: false, AtUnix: time.Now().Add(-time.Hour).Unix()})

	byID := map[string]orchestratorInfo{}
	for _, info := range app.ListOrchestrators() {
		byID[info.ID] = info
	}
	if !byID[reported].PacingUnreachable {
		t.Fatalf("expected %s to be marked as unpaced from this desktop, got %+v", reported, byID[reported])
	}
	if byID[quiet].PacingUnreachable {
		t.Fatalf("expected a stopped orchestrator nothing is reporting for to stay unmarked, got %+v", byID[quiet])
	}
	for _, info := range byID {
		if info.Status != "stopped" {
			t.Fatalf("expected every orchestrator here to be stopped, got %+v", info)
		}
	}
}

// The mark is about a session this desktop cannot pace, never about the
// orchestrator's own records: one it holds a session for is paced here however
// recently that session reported.
func TestListOrchestratorsNeverMarksAnOrchestratorItHoldsASessionFor(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	id := createAndStartOrchestrator(t, app)
	writeOrchestratorActivity(t, id, orchestratorActivity{Busy: true, AtUnix: time.Now().Unix()})

	info := requireOrchestratorInfo(t, app, id)
	if info.Status != "running" || info.PacingUnreachable {
		t.Fatalf("expected a running, paced orchestrator, got %+v", info)
	}
}
