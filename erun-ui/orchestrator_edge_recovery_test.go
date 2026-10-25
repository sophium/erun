package main

import (
	"bytes"
	"context"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"

	"github.com/sophium/erun/erun-ui/internal/runtimeview"
)

func TestEdgeRecoveryIsLoggedOnceTheEdgeAnswersAgain(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
	// A forward that holds its port while its edge answers nothing.
	probe := &forwardRepairProbe{}
	app, _ := forwardRepairTestApp(t, probe, nil)
	defer app.shutdown(context.Background())

	var logs bytes.Buffer
	log.SetOutput(&logs)
	restoreLogOutputAfter(t)

	app.wireOrchestratorMCP("petios", "Petios", []config.OrchestratorEnvConfig{
		{Tenant: "erun", Environment: "remote"},
	})
	if got := logs.String(); !strings.Contains(got, "orchestrator petios: wired erun/remote but its edge is not answering") {
		t.Fatalf("expected the unreachable edge to be recorded on entry, got:\n%s", got)
	}

	// Re-seed the forward so the sweep reaches the edge instead of the pod probe.
	sealForwardForSweep(t)
	if got := logs.String(); strings.Contains(got, "answering again") {
		t.Fatalf("an edge yet to answer reported a recovery:\n%s", got)
	}

	probe.setAnswers(true)
	state := sweepUntilRebindSettles(t, app)
	if !state.Reachable || !state.Observed {
		t.Fatalf("expected the sweep to observe the answering edge, got %+v", state)
	}
	if got := logs.String(); !strings.Contains(got, "orchestrator petios: wired erun/remote and its edge is answering again") {
		t.Fatalf("expected the recovery line naming the orchestrator and the wiring, got:\n%s", got)
	}

	sweepUntilRebindSettles(t, app)
	if got := strings.Count(logs.String(), "answering again"); got != 1 {
		t.Fatalf("recovery lines = %d, want exactly 1:\n%s", got, logs.String())
	}
}

// sealForwardForSweep re-seeds the forward; without it the sweep returns before
// the edge question and the test passes vacuously.
func sealForwardForSweep(t *testing.T) {
	t.Helper()
	seedMCPForward(t, "erun", "remote", forwardRepairTestPort)
}

func TestEdgeRecoveryPairsEveryEntryWhenTwoOrchestratorsShareTheEdge(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
	probe := &forwardRepairProbe{}
	app, _ := forwardRepairTestApp(t, probe, nil)
	defer app.shutdown(context.Background())

	var logs bytes.Buffer
	log.SetOutput(&logs)
	restoreLogOutputAfter(t)

	envs := []config.OrchestratorEnvConfig{{Tenant: "erun", Environment: "remote"}}
	app.wireOrchestratorMCP("petios", "Petios", envs)
	app.wireOrchestratorMCP("atlas", "Atlas", envs)
	for _, id := range []string{"petios", "atlas"} {
		want := "orchestrator " + id + ": wired erun/remote but its edge is not answering"
		if got := logs.String(); !strings.Contains(got, want) {
			t.Fatalf("expected the entry for %s, got:\n%s", id, got)
		}
	}

	sealForwardForSweep(t)
	probe.setAnswers(true)
	if state := sweepUntilRebindSettles(t, app); !state.Reachable || !state.Observed {
		t.Fatalf("expected the sweep to observe the answering edge, got %+v", state)
	}

	for _, id := range []string{"petios", "atlas"} {
		want := "orchestrator " + id + ": wired erun/remote and its edge is answering again"
		if got := logs.String(); !strings.Contains(got, want) {
			t.Fatalf("expected an exit pairing the entry for %s, got:\n%s", id, got)
		}
	}
	if got := strings.Count(logs.String(), "answering again"); got != 2 {
		t.Fatalf("recovery lines = %d, want one per recorded entry (2):\n%s", got, logs.String())
	}
}

func TestEdgeOutageEntryWithNoRecoveryObservationStaysUnpaired(t *testing.T) {
	for _, tc := range []struct {
		name           string
		forwardDropped bool
	}{
		{name: "edge still not answering on a held port"},
		{name: "forward dropped entirely", forwardDropped: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
			probe := &forwardRepairProbe{forwardDropped: tc.forwardDropped}
			app, _ := forwardRepairTestApp(t, probe, nil)
			defer app.shutdown(context.Background())

			var logs bytes.Buffer
			log.SetOutput(&logs)
			restoreLogOutputAfter(t)

			app.wireOrchestratorMCP("petios", "Petios", []config.OrchestratorEnvConfig{
				{Tenant: "erun", Environment: "remote"},
			})
			sealForwardForSweep(t)
			var state runtimeview.ActivityState
			for range forwardRepairAttempts + 2 {
				state = sweepUntilRebindSettles(t, app)
			}

			got := logs.String()
			if !strings.Contains(got, "but its edge is not answering") {
				t.Fatalf("expected the entry to be recorded, got:\n%s", got)
			}
			// A sweep that returned early would also log no recovery.
			if !state.Outage {
				t.Fatalf("expected the sweep to diagnose the unreachable edge, got %+v", state)
			}
			if strings.Contains(got, "answering again") {
				t.Fatalf("an unresolved outage reported a recovery:\n%s", got)
			}
		})
	}
}

func TestCutEnvLabelRejectsLabelsTheSweepCouldNeverMatch(t *testing.T) {
	for _, tc := range []struct {
		label       string
		tenant      string
		environment string
		ok          bool
	}{
		{label: "frs/dev", tenant: "frs", environment: "dev", ok: true},
		{label: "no-separator", ok: false},
		{label: "/dev", ok: false},
		{label: "frs/", ok: false},
		{label: "", ok: false},
	} {
		tenant, environment, ok := cutEnvLabel(tc.label)
		if ok != tc.ok || tenant != tc.tenant || environment != tc.environment {
			t.Fatalf("cutEnvLabel(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.label, tenant, environment, ok, tc.tenant, tc.environment, tc.ok)
		}
	}
}
