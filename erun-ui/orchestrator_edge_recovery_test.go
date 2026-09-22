package main

import (
	"bytes"
	"context"
	"log"
	"path/filepath"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestEdgeRecoveryIsLoggedOnceTheEdgeAnswersAgain is the pairing this defect asks
// for, end to end through the two real code paths: the entry is written by
// wireOrchestratorMCP at spawn, and the exit by the activity sweep that later
// observes the same edge answering. Both directions are asserted, because the
// defect was an entry with no exit and the cheap way to "fix" that is an exit
// that fires whether or not anything recovered.
func TestEdgeRecoveryIsLoggedOnceTheEdgeAnswersAgain(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
	// A forward that holds its local port while its edge answers nothing, which
	// is the shape the sweep probes without repairing away the wiring first.
	probe := &forwardRepairProbe{}
	app, _ := forwardRepairTestApp(t, probe, nil)
	defer app.shutdown(context.Background())

	var logs bytes.Buffer
	log.SetOutput(&logs)
	restoreLogOutputAfter(t)

	app.wireOrchestratorMCP("petios", "Petios", []eruncommon.OrchestratorEnvConfig{
		{Tenant: "erun", Environment: "remote"},
	})
	if got := logs.String(); !strings.Contains(got, "orchestrator petios: wired erun/remote but its edge is not answering") {
		t.Fatalf("expected the unreachable edge to be recorded on entry, got:\n%s", got)
	}

	// The edge is still broken, and nothing has claimed a recovery for it.
	//
	// sealForwardForSweep re-seeds the forward after the wiring resolved its own
	// config paths, so the sweep below observes that forward rather than falling
	// back to the pod probe and returning before it ever asks the edge anything.
	sealForwardForSweep(t)
	if got := logs.String(); strings.Contains(got, "answering again") {
		t.Fatalf("an edge yet to answer reported a recovery:\n%s", got)
	}

	// The edge answers again. The next sweep is the observation that logs it.
	probe.setAnswers(true)
	state := sweepUntilRebindSettles(t, app)
	if !state.reachable || !state.observed {
		t.Fatalf("expected the sweep to observe the answering edge, got %+v", state)
	}
	if got := logs.String(); !strings.Contains(got, "orchestrator petios: wired erun/remote and its edge is answering again") {
		t.Fatalf("expected the recovery line naming the orchestrator and the wiring, got:\n%s", got)
	}

	// One entry, one exit. The episode is retired by its exit, so an edge that
	// stays healthy does not re-announce a recovery already reported.
	sweepUntilRebindSettles(t, app)
	if got := strings.Count(logs.String(), "answering again"); got != 1 {
		t.Fatalf("recovery lines = %d, want exactly 1:\n%s", got, logs.String())
	}
}

// sealForwardForSweep re-seeds the MCP forward state for the environment the
// recovery test watches, once the wiring path has finished resolving config
// roots of its own. Without it the sweep finds no recorded forward, falls back
// to probing the runtime pod, and returns without ever reaching the edge
// question — which would let both directions of this test pass vacuously.
func sealForwardForSweep(t *testing.T) {
	t.Helper()
	seedMCPForward(t, "erun", "remote", forwardRepairTestPort)
}

// TestEdgeRecoveryPairsEveryEntryWhenTwoOrchestratorsShareTheEdge locks the
// pairing when more than one orchestrator links the same environment: each one
// logs its own outage entry, so a recovery must produce an exit naming each of
// them. A single exit naming only the last orchestrator to be wired would leave
// the other entry looking unresolved, which is the defect this pairing exists
// to remove.
func TestEdgeRecoveryPairsEveryEntryWhenTwoOrchestratorsShareTheEdge(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
	probe := &forwardRepairProbe{}
	app, _ := forwardRepairTestApp(t, probe, nil)
	defer app.shutdown(context.Background())

	var logs bytes.Buffer
	log.SetOutput(&logs)
	restoreLogOutputAfter(t)

	envs := []eruncommon.OrchestratorEnvConfig{{Tenant: "erun", Environment: "remote"}}
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
	if state := sweepUntilRebindSettles(t, app); !state.reachable || !state.observed {
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

// TestEdgeOutageEntryWithNoRecoveryObservationStaysUnpaired is the other half of
// the property: an entry whose edge never answers through a bound port gets no
// exit line, so the log cannot report a resolution that did not happen. A
// forward that is gone entirely is not a recovered edge either — no traffic can
// flow through a port nothing holds — so it stays unpaired too.
func TestEdgeOutageEntryWithNoRecoveryObservationStaysUnpaired(t *testing.T) {
	for _, tc := range []struct {
		name string
		// forwardDropped is the ordinary shape of a lost forward: nothing holds
		// the local port, so there is no edge to observe answering.
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

			app.wireOrchestratorMCP("petios", "Petios", []eruncommon.OrchestratorEnvConfig{
				{Tenant: "erun", Environment: "remote"},
			})
			sealForwardForSweep(t)
			var state environmentActivityState
			for range forwardRepairAttempts + 2 {
				state = sweepUntilRebindSettles(t, app)
			}

			got := logs.String()
			if !strings.Contains(got, "but its edge is not answering") {
				t.Fatalf("expected the entry to be recorded, got:\n%s", got)
			}
			// The sweep must have reached the edge question and diagnosed it, or
			// the absence of a recovery line below would prove nothing: a sweep
			// that returns early also logs nothing.
			if !state.outage {
				t.Fatalf("expected the sweep to diagnose the unreachable edge, got %+v", state)
			}
			if strings.Contains(got, "answering again") {
				t.Fatalf("an unresolved outage reported a recovery:\n%s", got)
			}
		})
	}
}

// TestCutEnvLabelRejectsLabelsTheSweepCouldNeverMatch locks the guard on the
// record path: a label that does not name a tenant/environment pair can never
// be observed by the sweep, so recording it would leave an entry that no exit
// can ever retire.
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
