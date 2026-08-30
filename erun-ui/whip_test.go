package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// whipTestStore mirrors jobsTestStore's shape: one resolvable tenant/
// environment so eruncommon.ResolveOpen succeeds.
func whipTestStore(t *testing.T) stubUIStore {
	t.Helper()
	return stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "ux"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/ux": {
				Name:              "ux",
				Type:              eruncommon.EnvironmentTypeLocalAgent,
				LocalRepoPath:     t.TempDir(),
				KubernetesContext: "orbstack",
			},
		},
	}
}

// TestWhipOneEnvironmentNowNotOpenReportsNotAlive is the red-then-green
// contract for the environment half of the desktop's whip control: an
// environment nobody has opened in this session has no MCP edge to push
// through, so it must be reported not-alive without ever attempting the call
// -- the same semantics erun-cli's own `erun whip` reports for an unopened
// environment.
func TestWhipOneEnvironmentNowNotOpenReportsNotAlive(t *testing.T) {
	called := false
	app := NewApp(erunUIDeps{
		store: whipTestStore(t),
		canReachMCPEndpoint: func(int) bool {
			return false
		},
		canConnectLocalPort: func(int) bool {
			return false
		},
		whipEnvironment: func(context.Context, string, string, string, string) (eruncommon.WhipResult, error) {
			called = true
			return eruncommon.WhipResult{}, nil
		},
	})

	result := app.whipOneEnvironmentNow(context.Background(), "erun", "ux")
	if called {
		t.Fatal("expected the MCP call to be skipped for an environment with no reachable edge")
	}
	if result.Decision != eruncommon.WhipDecisionNone || result.Reason != eruncommon.WhipReasonNotAlive {
		t.Fatalf("got decision=%v reason=%v, want none/not-alive", result.Decision, result.Reason)
	}
	if result.Candidate.ID != "erun/ux" {
		t.Fatalf("expected the candidate id to name the environment, got %q", result.Candidate.ID)
	}
}

// TestWhipOneEnvironmentNowStaleForwardReportsUnreachableNotNotAlive is the
// sibling case jobsReachability already draws for job reads: a stale
// port-forward (something holds the port, the edge never answers) means a
// pod may well be running a session behind it, so reporting not-alive there
// would tell the operator there is nothing to push when the true answer is
// "cannot tell right now".
func TestWhipOneEnvironmentNowStaleForwardReportsUnreachableNotNotAlive(t *testing.T) {
	app := NewApp(erunUIDeps{
		store: whipTestStore(t),
		canReachMCPEndpoint: func(int) bool {
			return false
		},
		canConnectLocalPort: func(int) bool {
			return true
		},
	})

	result := app.whipOneEnvironmentNow(context.Background(), "erun", "ux")
	if result.Reason != eruncommon.WhipReasonUnreachable {
		t.Fatalf("got reason=%v, want unreachable", result.Reason)
	}
	if result.Error == "" {
		t.Fatal("expected the stale-forward description to be carried in Error")
	}
}

// TestWhipOneEnvironmentNowPushesThroughAReachableEdge is the counterpart
// green case: a reachable edge is called, and the pod's own decoded
// WhipResult (not a host-derived one) is what the desktop reports -- the
// desktop must never re-derive the environment decision locally.
func TestWhipOneEnvironmentNowPushesThroughAReachableEdge(t *testing.T) {
	var gotEndpoint string
	app := NewApp(erunUIDeps{
		store: whipTestStore(t),
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		whipEnvironment: func(_ context.Context, _, _, endpoint, _ string) (eruncommon.WhipResult, error) {
			gotEndpoint = endpoint
			return eruncommon.WhipResult{
				Candidate: eruncommon.WhipCandidate{Kind: eruncommon.WhipTargetEnvironment, ID: "erun/ux", Name: "erun/ux", Reachable: true, Alive: true},
				Decision:  eruncommon.WhipDecisionNudge,
				Reason:    eruncommon.WhipReasonNudge,
				Pushed:    true,
			}, nil
		},
	})

	result := app.whipOneEnvironmentNow(context.Background(), "erun", "ux")
	if gotEndpoint == "" {
		t.Fatal("expected the pod-backed whip call to be used")
	}
	if result.Decision != eruncommon.WhipDecisionNudge || !result.Pushed {
		t.Fatalf("expected the pod's own decision to pass through untouched, got %+v", result)
	}
}

// TestWhipOneEnvironmentNowStampsIdentityOnSuccessEvenIfThePodDidNot is the
// regression test for the "identity was never the pod's to supply" half of
// Every other return path in whipOneEnvironmentNow already stamps
// Candidate.ID/Name from the host's own id, but the success path used to
// trust whatever the pod echoed back verbatim. A pod that decodes to an empty
// (or wrong) identity must still be reported under the id the desktop itself
// resolved, never a blank row.
func TestWhipOneEnvironmentNowStampsIdentityOnSuccessEvenIfThePodDidNot(t *testing.T) {
	app := NewApp(erunUIDeps{
		store: whipTestStore(t),
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		whipEnvironment: func(context.Context, string, string, string, string) (eruncommon.WhipResult, error) {
			return eruncommon.WhipResult{
				Decision: eruncommon.WhipDecisionNudge,
				Reason:   eruncommon.WhipReasonNudge,
				Pushed:   true,
			}, nil
		},
	})

	result := app.whipOneEnvironmentNow(context.Background(), "erun", "ux")
	if result.Candidate.ID != "erun/ux" || result.Candidate.Name != "erun/ux" {
		t.Fatalf("expected the host-resolved id to be stamped regardless of the pod's payload, got %+v", result.Candidate)
	}
	if result.Decision != eruncommon.WhipDecisionNudge || !result.Pushed {
		t.Fatalf("expected the pod's own decision to still pass through, got %+v", result)
	}
}

// TestWhipOneEnvironmentNowSurfacesACallFailureAsFailedNotNotAlive covers the
// MCP call itself failing (e.g. an old runtime image without the "whip" tool,
// or -- since the fix below now forwards a real target -- a tenant/
// environment mismatch the pod's own resolveLocalTarget refused): the
// environment must be reported as a genuine failure, never folded into
// WhipReasonNotAlive. The whip tool itself already reports a dead session as
// a *successful* result carrying that reason, so an error at this call site
// can only mean the call itself did not work -- erun#1709's "a reason that
// contradicts the error" defect (a not-alive skip citing an unrelated
// call-level error, e.g. a missing target) is exactly what this guards.
func TestWhipOneEnvironmentNowSurfacesACallFailureAsFailedNotNotAlive(t *testing.T) {
	app := NewApp(erunUIDeps{
		store: whipTestStore(t),
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		whipEnvironment: func(context.Context, string, string, string, string) (eruncommon.WhipResult, error) {
			return eruncommon.WhipResult{}, errors.New("tool not found")
		},
	})

	result := app.whipOneEnvironmentNow(context.Background(), "erun", "ux")
	if result.Reason != eruncommon.WhipReasonCallFailed || result.Error == "" {
		t.Fatalf("expected a call-failed result naming the failure, got %+v", result)
	}
	if result.Reason == eruncommon.WhipReasonNotAlive {
		t.Fatal("a call-level failure must never be reported as not-alive")
	}
}

// TestWhipOneEnvironmentNowForwardsTheResolvedTarget is the plumbing
// regression test for erun#1709: the desktop used to call the "whip" MCP tool
// with only {"preview": false}, relying entirely on the pod defaulting to its
// own bound context. whipOneEnvironmentNow already resolved tenant/environment
// to reach this edge in the first place, so it must restate them in the call
// -- the stronger assertion that turns a stale edge pointed at the wrong
// environment into a named mismatch instead of a silent act on the wrong one.
func TestWhipOneEnvironmentNowForwardsTheResolvedTarget(t *testing.T) {
	var gotTenant, gotEnvironment string
	app := NewApp(erunUIDeps{
		store: whipTestStore(t),
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		whipEnvironment: func(_ context.Context, tenant, environment, _, _ string) (eruncommon.WhipResult, error) {
			gotTenant, gotEnvironment = tenant, environment
			return eruncommon.WhipResult{Decision: eruncommon.WhipDecisionNudge, Pushed: true}, nil
		},
	})

	app.whipOneEnvironmentNow(context.Background(), "erun", "ux")
	if gotTenant != "erun" || gotEnvironment != "ux" {
		t.Fatalf("expected the resolved target to be forwarded, got tenant=%q environment=%q", gotTenant, gotEnvironment)
	}
}

// TestWhipNowFoldsEnvironmentsAndOrchestratorsIntoOneReport is the desktop's
// own visible-record contract (root AGENTS.md's "Smooth, Seamless, No Dead
// Ends"): every configured environment and every live orchestrator appears in
// the returned report, not just an aggregate "ran" signal.
func TestWhipNowFoldsEnvironmentsAndOrchestratorsIntoOneReport(t *testing.T) {
	isolateOrchestratorWhipConfig(t)
	orchestratorPacingNudgeSettle = 0

	app := NewApp(erunUIDeps{
		store: whipTestStore(t),
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		whipEnvironment: func(context.Context, string, string, string, string) (eruncommon.WhipResult, error) {
			return eruncommon.WhipResult{
				Candidate: eruncommon.WhipCandidate{Kind: eruncommon.WhipTargetEnvironment, ID: "erun/ux", Name: "erun/ux", Reachable: true, Alive: true},
				Decision:  eruncommon.WhipDecisionNudge,
				Reason:    eruncommon.WhipReasonNudge,
				Pushed:    true,
			}, nil
		},
	})

	aliveSession := newCallRecordingSession()
	aliveKey := orchestratorSessionKey("alive")
	app.sessions[aliveKey] = &managedTerminal{session: aliveSession, key: aliveKey, serial: 1, kind: sessionKindOrchestrator}
	app.orchestrators["alive"] = &orchestratorSession{id: "alive", serial: 1, name: "alive", startedAt: time.Now()}

	report, err := app.WhipNow([]uiWhipTargetRef{
		{Kind: uiWhipTargetKindEnvironment, ID: "erun/ux"},
		{Kind: uiWhipTargetKindOrchestrator, ID: "alive"},
	})
	if err != nil {
		t.Fatalf("WhipNow failed: %v", err)
	}
	if len(report.Results) != 2 {
		t.Fatalf("expected one environment result and one orchestrator result, got %d: %+v", len(report.Results), report.Results)
	}
	byKind := map[string]uiWhipResult{}
	for _, result := range report.Results {
		byKind[result.Kind] = result
	}
	env := byKind["environment"]
	if env.Outcome != "pushed" || env.ID != "erun/ux" {
		t.Fatalf("environment result: got %+v, want pushed/erun/ux", env)
	}
	orchestrator := byKind["orchestrator"]
	if orchestrator.Outcome != "pushed" || orchestrator.ID != "alive" {
		t.Fatalf("orchestrator result: got %+v, want pushed/alive", orchestrator)
	}
}

// TestWhipEnvironmentsNowSkipsHostEnvs covers the "enumerates targets
// that could never have been whipped" finding: a host-type env has no pod and
// no cluster contact at all (EnvConfig.HasPod), so it can never carry an AI
// session to push. Reporting it every pass is noise the report should not
// carry, unlike a real env this transport merely cannot reach right now.
func TestWhipEnvironmentsNowSkipsHostEnvs(t *testing.T) {
	store := whipTestStore(t)
	store.envs["erun/desktop-build"] = eruncommon.EnvConfig{
		Name:          "desktop-build",
		Type:          eruncommon.EnvironmentTypeHost,
		LocalRepoPath: t.TempDir(),
	}

	app := NewApp(erunUIDeps{
		store: store,
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		whipEnvironment: func(context.Context, string, string, string, string) (eruncommon.WhipResult, error) {
			return eruncommon.WhipResult{
				Candidate: eruncommon.WhipCandidate{Kind: eruncommon.WhipTargetEnvironment, ID: "erun/ux", Name: "erun/ux", Reachable: true, Alive: true},
				Decision:  eruncommon.WhipDecisionNudge,
				Pushed:    true,
			}, nil
		},
	})

	results, err := app.whipEnvironmentsNow(context.Background(), map[string]struct{}{"erun/ux": {}, "erun/desktop-build": {}})
	if err != nil {
		t.Fatalf("whipEnvironmentsNow failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected the host env to be skipped, got %d results: %+v", len(results), results)
	}
	if results[0].Candidate.ID != "erun/ux" {
		t.Fatalf("expected only erun/ux, got %+v", results[0])
	}
}

// TestWhipEnvironmentsNowOnlyPushesRequestedEnvironments is the regression
// test for the desktop's own blast-radius bug (erun#1700): an environment not
// in the requested set must not be pushed at all, not merely omitted from the
// report after being attempted anyway.
func TestWhipEnvironmentsNowOnlyPushesRequestedEnvironments(t *testing.T) {
	store := whipTestStore(t)
	store.envs["erun/dev"] = eruncommon.EnvConfig{
		Name:              "dev",
		Type:              eruncommon.EnvironmentTypeLocalAgent,
		LocalRepoPath:     t.TempDir(),
		KubernetesContext: "orbstack",
	}

	var pushedEndpoints []string
	app := NewApp(erunUIDeps{
		store: store,
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		whipEnvironment: func(_ context.Context, _, _, endpoint, _ string) (eruncommon.WhipResult, error) {
			pushedEndpoints = append(pushedEndpoints, endpoint)
			return eruncommon.WhipResult{Decision: eruncommon.WhipDecisionNudge, Pushed: true}, nil
		},
	})

	results, err := app.whipEnvironmentsNow(context.Background(), map[string]struct{}{"erun/ux": {}})
	if err != nil {
		t.Fatalf("whipEnvironmentsNow failed: %v", err)
	}
	if len(results) != 1 || results[0].Candidate.ID != "erun/ux" {
		t.Fatalf("expected only the requested environment in the report, got %+v", results)
	}
	if len(pushedEndpoints) != 1 {
		t.Fatalf("expected exactly one push, got %d: %v", len(pushedEndpoints), pushedEndpoints)
	}
}

// TestListWhipEnvironmentTargetsSkipsHostEnvs mirrors
// TestWhipEnvironmentsNowSkipsHostEnvs for the selection surface's own
// population: a host-type env must not be offered as a selectable target
// either, since it can never carry a session to push.
func TestListWhipEnvironmentTargetsSkipsHostEnvs(t *testing.T) {
	store := whipTestStore(t)
	store.envs["erun/desktop-build"] = eruncommon.EnvConfig{
		Name:          "desktop-build",
		Type:          eruncommon.EnvironmentTypeHost,
		LocalRepoPath: t.TempDir(),
	}
	app := NewApp(erunUIDeps{store: store})

	targets, err := app.listWhipEnvironmentTargets()
	if err != nil {
		t.Fatalf("listWhipEnvironmentTargets failed: %v", err)
	}
	if len(targets) != 1 || targets[0].environment != "ux" {
		t.Fatalf("expected only ux, got %+v", targets)
	}
}

// TestWhipNowRefusesAnEmptyTargetList is the acceptance-criteria contract
// (erun#1700): an empty selection is not "all" -- WhipNow must refuse rather
// than fall back to whipping everything configured.
func TestWhipNowRefusesAnEmptyTargetList(t *testing.T) {
	app := NewApp(erunUIDeps{store: whipTestStore(t)})
	if _, err := app.WhipNow(nil); err == nil {
		t.Fatal("expected WhipNow to refuse an empty target list")
	}
	if _, err := app.WhipNow([]uiWhipTargetRef{}); err == nil {
		t.Fatal("expected WhipNow to refuse an empty target list")
	}
}

// TestWhipNowRefusesAnUnknownTargetKind guards the wire contract between the
// frontend and this method: a target kind neither transport recognizes must
// fail loudly rather than being silently dropped from the push.
func TestWhipNowRefusesAnUnknownTargetKind(t *testing.T) {
	app := NewApp(erunUIDeps{store: whipTestStore(t)})
	if _, err := app.WhipNow([]uiWhipTargetRef{{Kind: "bogus", ID: "x"}}); err == nil {
		t.Fatal("expected WhipNow to refuse an unrecognized target kind")
	}
}

// TestWhipTargetsListsEnvironmentsAndOrchestrators is the selection surface's
// whole-population contract: WhipTargets must offer exactly the environments
// listWhipEnvironmentTargets enumerates and exactly the orchestrators
// listWhipOrchestratorTargets enumerates, with IDs that round-trip through
// WhipNow's own uiWhipTargetRef.
func TestWhipTargetsListsEnvironmentsAndOrchestrators(t *testing.T) {
	app := NewApp(erunUIDeps{store: whipTestStore(t)})
	app.orchestrators["alive"] = &orchestratorSession{id: "alive", serial: 1, name: "Alive", startedAt: time.Now()}
	key := orchestratorSessionKey("alive")
	app.sessions[key] = &managedTerminal{session: newCallRecordingSession(), key: key, serial: 1, kind: sessionKindOrchestrator}

	list, err := app.WhipTargets()
	if err != nil {
		t.Fatalf("WhipTargets failed: %v", err)
	}
	if len(list.Environments) != 1 || list.Environments[0].ID != "erun/ux" {
		t.Fatalf("expected exactly one environment target erun/ux, got %+v", list.Environments)
	}
	if list.Environments[0].Tenant != "erun" || list.Environments[0].Environment != "ux" {
		t.Fatalf("expected the tenant/environment fields to match the id, got %+v", list.Environments[0])
	}
	if len(list.Orchestrators) != 1 || list.Orchestrators[0].ID != "alive" || list.Orchestrators[0].Name != "Alive" {
		t.Fatalf("expected exactly one orchestrator target alive/Alive, got %+v", list.Orchestrators)
	}
}

// TestOrchestratorWhipOutcomeToResultRoundTripsEveryDecision pins the
// inverse of orchestratorPacingDecisionFromWhip/orchestratorPacingReasonFromWhip
// used to fold an orchestrator outcome into the shared eruncommon.WhipResult
// shape: every decision/reason pair the reconciler can produce must survive
// the round trip, or WhipNow's report would misname an orchestrator's actual
// outcome.
func TestOrchestratorWhipOutcomeToResultRoundTripsEveryDecision(t *testing.T) {
	cases := []struct {
		decision orchestratorPacingDecision
		reason   orchestratorPacingReason
		want     eruncommon.WhipDecision
		alive    bool
		pushed   bool
	}{
		{orchestratorPacingNudge, orchestratorPacingReasonNudge, eruncommon.WhipDecisionNudge, true, true},
		{orchestratorPacingCap, orchestratorPacingReasonCapCrossed, eruncommon.WhipDecisionCap, true, false},
		{orchestratorPacingNone, orchestratorPacingReasonNotAlive, eruncommon.WhipDecisionNone, false, false},
		{orchestratorPacingNone, orchestratorPacingReasonAlreadyCapped, eruncommon.WhipDecisionNone, true, false},
	}
	for _, tc := range cases {
		result := orchestratorWhipOutcomeToResult(orchestratorWhipOutcome{id: "o1", name: "One", decision: tc.decision, reason: tc.reason})
		if result.Decision != tc.want {
			t.Fatalf("%v/%v: got decision %v, want %v", tc.decision, tc.reason, result.Decision, tc.want)
		}
		if result.Candidate.Alive != tc.alive {
			t.Fatalf("%v/%v: got alive=%v, want %v", tc.decision, tc.reason, result.Candidate.Alive, tc.alive)
		}
		if result.Pushed != tc.pushed {
			t.Fatalf("%v/%v: got pushed=%v, want %v", tc.decision, tc.reason, result.Pushed, tc.pushed)
		}
	}
}

// TestWhipResultToUIRendersEveryOutcome pins the UI facade's rendering rules:
// a pushed nudge reads "pushed", a capped target reads "capped" with its own
// recovery text, and a skip always carries a human-readable reason so a
// skipped row never reaches the frontend blank.
func TestWhipResultToUIRendersEveryOutcome(t *testing.T) {
	pushed := whipResultToUI(eruncommon.WhipResult{
		Candidate: eruncommon.WhipCandidate{Kind: eruncommon.WhipTargetEnvironment, ID: "erun/ux", Name: "erun/ux"},
		Decision:  eruncommon.WhipDecisionNudge,
		Pushed:    true,
	})
	if pushed.Outcome != "pushed" || pushed.Kind != "environment" {
		t.Fatalf("got %+v, want pushed/environment", pushed)
	}

	capped := whipResultToUI(eruncommon.WhipResult{
		Candidate: eruncommon.WhipCandidate{Kind: eruncommon.WhipTargetOrchestrator, ID: "o1", Name: "One"},
		Decision:  eruncommon.WhipDecisionCap,
	})
	if capped.Outcome != "capped" || capped.Reason == "" {
		t.Fatalf("got %+v, want capped with a non-empty reason", capped)
	}

	skipped := whipResultToUI(eruncommon.WhipResult{
		Candidate: eruncommon.WhipCandidate{Kind: eruncommon.WhipTargetEnvironment, ID: "erun/dev", Name: "erun/dev"},
		Decision:  eruncommon.WhipDecisionNone,
		Reason:    eruncommon.WhipReasonNotAlive,
	})
	if skipped.Outcome != "skipped" || !strings.Contains(skipped.Reason, "not alive") {
		t.Fatalf("got %+v, want skipped naming not-alive", skipped)
	}

	failedPush := whipResultToUI(eruncommon.WhipResult{
		Candidate: eruncommon.WhipCandidate{Kind: eruncommon.WhipTargetEnvironment, ID: "erun/dev", Name: "erun/dev"},
		Decision:  eruncommon.WhipDecisionNudge,
		Pushed:    false,
		Error:     "writing nudge text: boom",
	})
	if failedPush.Outcome != "failed" || failedPush.Error == "" {
		t.Fatalf("got %+v, want a decided-but-failed push reported failed with its error", failedPush)
	}

	failedCall := whipResultToUI(eruncommon.WhipResult{
		Candidate: eruncommon.WhipCandidate{Kind: eruncommon.WhipTargetEnvironment, ID: "erun/dev", Name: "erun/dev"},
		Decision:  eruncommon.WhipDecisionNone,
		Reason:    eruncommon.WhipReasonCallFailed,
		Error:     "tenant/environment not resolved",
	})
	if failedCall.Outcome != "failed" || failedCall.Error == "" {
		t.Fatalf("got %+v, want a call failure reported failed with its error, never folded into skipped", failedCall)
	}
}
