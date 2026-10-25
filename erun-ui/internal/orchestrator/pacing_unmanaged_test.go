package orchestrator

import (
	"bytes"
	"context"
	"log"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A configured orchestrator with no session here must still be logged, with the
// transport's reason, never "not-alive": such sessions are usually alive.
func TestOrchestratorPacingLogsAConfiguredOrchestratorItCannotPace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	restoreLogOutputAfter(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	createOrchestratorNamed(t, app, "held")
	heldSession := newCallRecordingSession()
	app.terminals.seed("held", 1, heldSession)
	app.orchestrators["held"] = &orchestratorSession{id: "held", serial: 1, name: "held", startedAt: time.Now()}

	createOrchestratorNamed(t, app, "external")

	app.ReconcileOrchestratorPacing()

	logged := logs.String()
	for _, want := range []string{
		"orchestrator held pacing decision=fresh",
		"orchestrator external pacing decision=unreachable-from-transport quiet=unknown",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("expected %q in the pacing log, got:\n%s", want, logged)
		}
	}
	// It never reported, so neither fresh nor capped applies.
	for _, unwanted := range []string{
		"orchestrator external pacing decision=fresh",
		"orchestrator external pacing decision=already-capped",
	} {
		if strings.Contains(logged, unwanted) {
			t.Fatalf("expected no %q, got:\n%s", unwanted, logged)
		}
	}

	// Once per transition, or this population logs every tick forever.
	logs.Reset()
	app.ReconcileOrchestratorPacing()
	if strings.Contains(logs.String(), "orchestrator external pacing") {
		t.Fatalf("expected the unreachable decision not to repeat on an unchanged tick, got:\n%s", logs.String())
	}
}

// A reporting unmanaged orchestrator's quiet period is its report's age, not "unknown".
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

	app.ReconcileOrchestratorPacing()

	want := regexp.MustCompile(`orchestrator external pacing decision=unreachable-from-transport quiet=20m\d+s`)
	if !want.MatchString(logs.String()) {
		t.Fatalf("expected %s in the pacing log, got:\n%s", want, logs.String())
	}
}

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
	// An explicit whip does not create a pty to write into.
	if decision, reason := app.reconcileOrchestratorPacingOne(rows[0], time.Now(), true); decision != orchestratorPacingNone || reason != orchestratorPacingReasonUnreachable {
		t.Fatalf("expected none/unreachable for an explicit whip too, got %v/%v", decision, reason)
	}
}

// An unmanaged orchestrator's nudge count stays zero, so the card must say it cannot be paced.
func TestListOrchestratorsMarksAConfiguredOrchestratorItCannotPace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	reported := createOrchestratorNamed(t, app, "external")
	quiet := createOrchestratorNamed(t, app, "stopped")
	writeOrchestratorActivity(t, reported, orchestratorActivity{Busy: true, AtUnix: time.Now().Add(-time.Minute).Unix()})
	// Old enough that the pacer would no longer decide for it either.
	writeOrchestratorActivity(t, quiet, orchestratorActivity{Busy: false, AtUnix: time.Now().Add(-time.Hour).Unix()})

	byID := map[string]Info{}
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
