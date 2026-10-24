package main

import (
	"strings"
	"testing"
)

func TestActivityTraceLineHandlerFinalizesOnDeployedAndFailed(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0"})
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)
	handler("==> Deployed team/dev 1.0.0 in 12s")
	if _, ok := app.activityQueue.findActive("team", "dev"); ok {
		t.Fatal("entry should be finished after ==> Deployed")
	}
	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}

	app2 := newTestAppForActivityQueue(t)
	app2.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0"})
	handler2 := newActivityTraceLineHandler(app2, selection, sessionKindLocal)
	handler2("Error: UPGRADE FAILED: timeout")
	handler2("==> Deploy failed after 2m0s")
	all2 := app2.activityQueue.list()
	if len(all2) != 1 || all2[0].Status != activityQueueStatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all2)
	}
	if !strings.Contains(all2[0].Error, "Deploy failed") && !strings.Contains(all2[0].Error, "UPGRADE FAILED") {
		t.Fatalf("error not captured: %q", all2[0].Error)
	}
}

func TestActivityTraceLineHandlerFinalizesOnReleaseNamedFailure(t *testing.T) {
	// This matcher is coupled to the runtime CLI's failure-line wording; a
	// wording change silently stops finalizing the entry unless the matcher is
	// updated with it.
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev"}
	app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "team", Environment: "dev"})
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)
	handler("Error: UPGRADE FAILED: timeout")
	handler("==> Deploy of team-devops failed after 2m0s")
	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
}

func TestSessionReadyFailedMatchesReleaseNamedFailure(t *testing.T) {
	// The session-ready gate matcher must track the same wording so a
	// failed deploy still releases the action runner.
	if !sessionReadyFailedRe.MatchString("==> Deploy of team-devops failed after 2m0s") {
		t.Fatal("sessionReadyFailedRe must match the release-named failure line")
	}
	if !sessionReadyFailedRe.MatchString("==> Deploy failed after 2m0s") {
		t.Fatal("sessionReadyFailedRe must still match the no-release fallback")
	}
}

func TestActivityTraceLineHandlerLabelsComponentDeployByRelease(t *testing.T) {
	// A component deploy must be labeled by its own release and version so the
	// drawer does not read like a full-env redeploy.
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)
	handler("==> Deploying erun/local · erun-backend-postgres 18.3")
	entry, ok := app.activityQueue.findActiveByCommand("deploy", "erun", "local")
	if !ok {
		t.Fatal("expected a deploy entry after the component ==> Deploying line")
	}
	if entry.Release != "erun-backend-postgres" {
		t.Fatalf("expected release erun-backend-postgres, got %q", entry.Release)
	}
	if entry.Version != "18.3" {
		t.Fatalf("expected the component version 18.3, got %q", entry.Version)
	}
	if !strings.Contains(entry.Summary, "· erun-backend-postgres") {
		t.Fatalf("expected a component-labeled summary, got %q", entry.Summary)
	}
}

func TestActivityTraceLineHandlerRuntimeDeployFallsBackToRuntimeRelease(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)
	handler("==> Deploying erun/local 1.0.0")
	entry, ok := app.activityQueue.findActiveByCommand("deploy", "erun", "local")
	if !ok {
		t.Fatal("expected a deploy entry after the runtime ==> Deploying line")
	}
	if entry.Release != releaseNameForTenant("erun") {
		t.Fatalf("expected runtime release %q, got %q", releaseNameForTenant("erun"), entry.Release)
	}
	if entry.Version != "1.0.0" {
		t.Fatalf("expected version 1.0.0, got %q", entry.Version)
	}
	if strings.Contains(entry.Summary, "·") {
		t.Fatalf("runtime deploy summary must not be component-labeled, got %q", entry.Summary)
	}
}

func TestActivityTraceLineHandlerFinalizesUsingTenantEnvFromLine(t *testing.T) {
	// Regression: an entry stayed running forever when `==> Deployed` landed in
	// a selection-less tab (a generic Local shell running `erun open ...` by
	// hand). Finalization must key off tenant/env parsed from the line, not the
	// session selection.
	app := newTestAppForActivityQueue(t)
	app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "ux",
		Version:     "1.0.51-snapshot-20260508135009",
		Source:      "trace",
	})
	emptySelection := uiSelection{}
	handler := newActivityTraceLineHandler(app, emptySelection, sessionKindLocal)
	handler("==> Deployed erun/ux 1.0.51-snapshot-20260508135009 in 52s")
	if _, ok := app.activityQueue.findActive("erun", "ux"); ok {
		t.Fatal("entry should be finished from line tenant/env even with empty selection")
	}
	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerFinalizesSkippingFromLine(t *testing.T) {
	// `==> Skipping` is the deploy singleflight's dedup-skip outcome; like
	// `==> Deployed`, it must finalize from tenant/env parsed off the line so a
	// selection-less tab still closes the entry.
	app := newTestAppForActivityQueue(t)
	app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "ux",
		Source:      "trace",
	})
	handler := newActivityTraceLineHandler(app, uiSelection{}, sessionKindLocal)
	handler("==> Skipping erun/ux 1.0.51 (identical deploy already in progress)")
	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusSkipped {
		t.Fatalf("expected one skipped entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerStartsAndFinishesBuild(t *testing.T) {
	// `erun build` traces carry no tenant/env (build has no deploy target), so
	// the handler keys off the session selection — otherwise the sidebar shows
	// no busy spinner for a build the user kicked off in a bound terminal.
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	handler("==> Building")
	entry, ok := app.activityQueue.findActiveByCommand("build", "erun", "local")
	if !ok {
		t.Fatal("expected build entry to be active after ==> Building")
	}
	if entry.Source != "trace" {
		t.Fatalf("expected source=trace, got %q", entry.Source)
	}

	handler("==> Built in 42s")
	if _, ok := app.activityQueue.findActiveByCommand("build", "erun", "local"); ok {
		t.Fatal("expected build entry to be finished after ==> Built")
	}
	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerFinalizesBuildOnFailure(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	handler("==> Building")
	handler("==> Build failed after 5s")

	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	if !strings.Contains(all[0].Error, "Build failed") {
		t.Fatalf("expected Build failed in error, got %q", all[0].Error)
	}
}

// TestActivityTraceLineHandlerRecordsDetailWithoutAPTY locks down that
// recordOutputLine's only caller used to be the PTY reader
// (feedActivityTraceFromTerminal), so the desktop's subprocess-captured
// build/push/deploy orchestration (deploy_orchestration.go, which has no PTY
// and calls the trace handler directly) never populated entry.Detail. The
// handler itself must record every line it sees, regardless of caller.
func TestActivityTraceLineHandlerRecordsDetailWithoutAPTY(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	// Call the handler directly, exactly as runErunCaptured's onLine callback
	// does for the desktop's orchestrated build — no managedTerminal, no PTY.
	handler("==> Building")
	handler("./main.go:12:2: undefined: fmt.Sprintfff")
	handler("==> Build failed after 3m12s")

	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	if !strings.Contains(all[0].Detail, "undefined: fmt.Sprintfff") {
		t.Fatalf("Detail missing the actual compiler error, got %q", all[0].Detail)
	}
	if strings.Contains(all[0].Error, "==>") || strings.Contains(all[0].Error, "after 3m12s") {
		t.Fatalf("Error should be cleaned of the trace marker and elapsed clause, got %q", all[0].Error)
	}
}

// TestActivityTraceLineHandlerFinalizesCorrectCardWhenBuildAndDeployBothActive
// locks down that the wrong card cannot fail: a bare deploy-failure line (no
// tenant/env, e.g. "==> Deploy failed after 4s") must finalize the deploy
// entry specifically, never a same-tenant/env build entry that is still
// running. Before the fix this used findActive, whose plain map iteration
// picks an arbitrary entry when both are active — this test is deterministic
// under the fix (findActiveByCommand) regardless of Go's map iteration order.
func TestActivityTraceLineHandlerFinalizesCorrectCardWhenBuildAndDeployBothActive(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	handler("==> Building")
	if _, ok := app.activityQueue.findActiveByCommand("build", "erun", "local"); !ok {
		t.Fatal("expected an active build entry")
	}
	app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "erun", Environment: "local"})

	handler("Error: UPGRADE FAILED: timeout")
	handler("==> Deploy failed after 4s")

	if _, ok := app.activityQueue.findActiveByCommand("build", "erun", "local"); !ok {
		t.Fatal("the still-running build entry must not have been finalized by the deploy failure")
	}
	if _, ok := app.activityQueue.findActiveByCommand("deploy", "erun", "local"); ok {
		t.Fatal("the deploy entry should have been finalized")
	}
	all := app.activityQueue.list()
	var deployEntry *activityQueueEntry
	for i := range all {
		if all[i].Command == "deploy" {
			deployEntry = &all[i]
		}
	}
	if deployEntry == nil || deployEntry.Status != activityQueueStatusFailed {
		t.Fatalf("expected a failed deploy entry in history, got %+v", all)
	}
	if !strings.Contains(deployEntry.Error, "UPGRADE FAILED") && !strings.Contains(deployEntry.Detail, "UPGRADE FAILED") {
		t.Fatalf("expected the captured tool error on the deploy entry, got %+v", deployEntry)
	}
}

func TestActivityTraceLineHandlerSkipsBuildWithoutSelection(t *testing.T) {
	// A generic Local shell at the repo root has no tenant/env bound to
	// it. Build traces observed there must NOT register an entry — a
	// stray (empty, empty) row would highlight every sidebar entry
	// because the spinner selector walks all entries by tenant/env.
	app := newTestAppForActivityQueue(t)
	handler := newActivityTraceLineHandler(app, uiSelection{}, sessionKindLocal)
	handler("==> Building")
	if len(app.activityQueue.list()) != 0 {
		t.Fatalf("expected no entries registered without a selection, got %+v", app.activityQueue.list())
	}
}

func TestActivityTraceLineHandlerStartsAndFinishesRelease(t *testing.T) {
	// `erun release` traces carry no tenant/env (same as build), so the handler
	// keys off the session selection — otherwise no spinner while a release runs
	// in a bound terminal.
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindOpen)

	handler("==> Releasing 1.4.2")
	entry, ok := app.activityQueue.findActiveByCommand("release", "erun", "local")
	if !ok {
		t.Fatal("expected release entry to be active after ==> Releasing")
	}
	if entry.Source != "trace" {
		t.Fatalf("expected source=trace, got %q", entry.Source)
	}

	handler("==> Released 1.4.2 in 12s")
	if _, ok := app.activityQueue.findActiveByCommand("release", "erun", "local"); ok {
		t.Fatal("expected release entry to be finished after ==> Released")
	}
	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerFinalizesReleaseOnFailure(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	handler("==> Releasing 1.4.2")
	handler("==> Release failed after 5s")

	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	if !strings.Contains(all[0].Error, "Release failed") {
		t.Fatalf("expected Release failed in error, got %q", all[0].Error)
	}
}

func TestActivityTraceLineHandlerStartsAndFinishesPush(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindOpen)

	handler("==> Pushing")
	if _, ok := app.activityQueue.findActiveByCommand("push", "erun", "local"); !ok {
		t.Fatal("expected push entry to be active after ==> Pushing")
	}

	handler("==> Pushed in 8s")
	if _, ok := app.activityQueue.findActiveByCommand("push", "erun", "local"); ok {
		t.Fatal("expected push entry to be finished after ==> Pushed")
	}
	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerFinalizesPushOnFailure(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	handler("==> Pushing")
	handler("==> Push failed after 3s")

	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	if !strings.Contains(all[0].Error, "Push failed") {
		t.Fatalf("expected Push failed in error, got %q", all[0].Error)
	}
}

func TestActivityTraceLineHandlerRegistersForAllSessionKinds(t *testing.T) {
	// The PTY trace handler is the universal early-detection signal
	// for deploys, regardless of session kind. Host-side sessions
	// (Local, Command) and in-pod sessions (Open, AI) all register an
	// entry from `==> Deploying`; the helm poller converges onto the
	// same record by ID, so duplicates can't drift.
	cases := []sessionKind{sessionKindLocal, sessionKindCommand, sessionKindOpen, sessionKindAI}
	for _, kind := range cases {
		app := newTestAppForActivityQueue(t)
		selection := uiSelection{Tenant: "erun", Environment: "local", KubernetesContext: "orbstack"}
		handler := newActivityTraceLineHandler(app, selection, kind)
		handler("==> Deploying erun/local 1.0.51-snapshot-20260510080136")
		entry, ok := app.activityQueue.findActiveByCommand("deploy", "erun", "local")
		if !ok {
			t.Fatalf("kind %q: expected deploy auto-registered from trace", kind)
		}
		if entry.Version != "1.0.51-snapshot-20260510080136" {
			t.Fatalf("kind %q: version = %q", kind, entry.Version)
		}
		if entry.Source != "trace" {
			t.Fatalf("kind %q: source = %q, want trace", kind, entry.Source)
		}
	}
}

// TestFeedActivityTraceCapturesFailureDetail locks the record-before-finalize
// ordering: the failing PTY output must already be buffered when the
// "==> Deploy failed" line finalizes the entry, so it lands in entry.Detail.
func TestFeedActivityTraceCapturesFailureDetail(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	managed := &managedTerminal{selection: selection, kind: sessionKindLocal, key: "local\x00team\x00dev", serial: 1}
	app.sessions[managed.key] = managed

	// Seed the entry directly rather than via the `==> Deploying` trace, whose
	// start path needs env config on disk this test does not stage.
	app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
	})

	feed := func(s string) { app.feedActivityTraceFromTerminal(managed, []byte(s)) }
	feed("helm upgrade --install team-devops ./chart\r\n")
	feed("Error: UPGRADE FAILED: timed out waiting for the condition\r\n")
	feed("==> Deploy failed after 4s\r\n")

	if _, ok := app.activityQueue.findActive("team", "dev"); ok {
		t.Fatal("entry should be finalized out of active after the failure line")
	}
	var failed *activityQueueEntry
	for _, entry := range app.activityQueue.list() {
		if entry.Tenant == "team" && entry.Environment == "dev" {
			e := entry
			failed = &e
			break
		}
	}
	if failed == nil {
		t.Fatal("expected a finalized entry for team/dev")
	}
	if failed.Status != activityQueueStatusFailed {
		t.Fatalf("status = %q, want failed", failed.Status)
	}
	for _, want := range []string{"helm upgrade --install", "UPGRADE FAILED", "==> Deploy failed after 4s"} {
		if !strings.Contains(failed.Detail, want) {
			t.Fatalf("Detail missing %q, got %q", want, failed.Detail)
		}
	}
}
