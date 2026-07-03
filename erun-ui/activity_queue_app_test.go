package main

import (
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// newTestAppForActivityQueue builds a minimal App with an in-memory queue
// and no background pollers.
func newTestAppForActivityQueue(t *testing.T) *App {
	t.Helper()
	app := &App{
		sessions: make(map[string]*managedTerminal),
	}
	app.activityQueue = newActivityQueueStore(nil, nil)
	return app
}

func TestLockTerminalsForActivityLocksMatchingSessions(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := &managedTerminal{selection: selection, key: "env\x00team\x00dev", serial: 1, kind: sessionKindOpen}
	aiSession := &managedTerminal{selection: selection, key: "ai\x00team\x00dev", serial: 2, kind: sessionKindAI}
	localSession := &managedTerminal{selection: selection, key: "local\x00team\x00dev", serial: 3, kind: sessionKindLocal}
	otherSelection := uiSelection{Tenant: "other", Environment: "dev", Version: "1.0.0"}
	unrelated := &managedTerminal{selection: otherSelection, key: "env\x00other\x00dev", serial: 4, kind: sessionKindOpen}
	app.sessions[envSession.key] = envSession
	app.sessions[aiSession.key] = aiSession
	app.sessions[localSession.key] = localSession
	app.sessions[unrelated.key] = unrelated

	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
		Release:     "team-devops",
	})
	app.lockTerminalsForActivity(entry)

	if envSession.lockedByActivity != entry.ID {
		t.Fatalf("env session not locked: %q want %q", envSession.lockedByActivity, entry.ID)
	}
	if aiSession.lockedByActivity != entry.ID {
		t.Fatalf("ai session not locked: %q", aiSession.lockedByActivity)
	}
	if localSession.lockedByActivity != "" {
		t.Fatalf("local session unexpectedly locked: %q", localSession.lockedByActivity)
	}
	if unrelated.lockedByActivity != "" {
		t.Fatalf("unrelated tenant locked: %q", unrelated.lockedByActivity)
	}
}

// TestLockTerminalsForActivityClearsEnvNotifications locks the fix: when a
// deploy starts locking an env's terminals, any env-scoped warning that told the
// operator to act (the runtime-unreachable banner, or a prior deploy-failed
// error) is now being acted on, so an env-wide app-notification-clear fires
// (empty source = clear any env-scoped notification for the env).
func TestLockTerminalsForActivityClearsEnvNotifications(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := &managedTerminal{selection: selection, key: "env\x00team\x00dev", serial: 1, kind: sessionKindOpen}
	app.sessions[envSession.key] = envSession

	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
		Release:     "team-devops",
	})
	app.lockTerminalsForActivity(entry)

	events := emits.events(appNotificationClearEvent)
	if len(events) != 1 {
		t.Fatalf("clear emitted %d times, want exactly 1", len(events))
	}
	payload, ok := events[0].(appNotificationClearPayload)
	if !ok {
		t.Fatalf("clear payload has unexpected type %T", events[0])
	}
	if payload.Tenant != "team" || payload.Environment != "dev" || payload.Source != "" {
		t.Fatalf("clear payload = %+v, want team/dev with empty (any) source", payload)
	}
}

// TestDeployFailedTraceSurfacesToToolbar locks the fix: a `==> Deploy
// failed tenant/env: reason` trace (emitted by `erun deploy` on any failure,
// including a pre-rollout spec-resolution failure) surfaces the failure in the
// toolbar — an env-tagged error notification plus a failed sidebar status — so a
// failed deploy is not silent.
func TestDeployFailedTraceSurfacesToToolbar(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	selection := uiSelection{Tenant: "frs", Environment: "local"}
	onLine := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	onLine(`==> Deploy failed frs/local: values file not found for environment "local"`)

	notes := emits.events(appNotificationEvent)
	if len(notes) != 1 {
		t.Fatalf("deploy failure emitted %d toolbar notifications, want 1", len(notes))
	}
	note, ok := notes[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("notification payload has unexpected type %T", notes[0])
	}
	if note.Kind != "error" || note.Tenant != "frs" || note.Environment != "local" ||
		note.Source != notificationSourceDeployFailed {
		t.Fatalf("notification = %+v, want error frs/local %q", note, notificationSourceDeployFailed)
	}
	if !strings.Contains(note.Message, "Deploy of frs/local failed") ||
		!strings.Contains(note.Message, "values file not found") {
		t.Fatalf("notification message = %q, want the failed target + reason", note.Message)
	}
	if got := len(emits.events(envStatusEvent)); got != 1 {
		t.Fatalf("failed sidebar status emitted %d times, want 1", got)
	}
}

// TestLockTerminalsForActivityWithoutMatchClearsNothing guards the gate: a
// deploy that locks no local sessions (nothing to act on for this desktop) must
// not fire a stray notification-clear.
func TestLockTerminalsForActivityWithoutMatchClearsNothing(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	other := &managedTerminal{
		selection: uiSelection{Tenant: "other", Environment: "dev"},
		key:       "env\x00other\x00dev", serial: 1, kind: sessionKindOpen,
	}
	app.sessions[other.key] = other

	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0", Release: "team-devops",
	})
	app.lockTerminalsForActivity(entry)

	if got := len(emits.events(appNotificationClearEvent)); got != 0 {
		t.Fatalf("clear emitted %d times with no matching session, want 0", got)
	}
}

// TestLockTerminalEventsAlwaysCarryReason pins the contract documented in
// erun-ui/AGENTS.md "Professional UX": the ActivityLockOverlay relies on
// the backend always populating Reason on Locked=true events. The
// frontend no longer carries a generic fallback string, so a missing
// reason would render a blank overlay header. This test guards both
// emit paths (lockTerminalsForActivity bulk + lockTerminalForActivity
// per-session).
func TestLockTerminalEventsAlwaysCarryReason(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := &managedTerminal{selection: selection, key: "env\x00team\x00dev", serial: 1, kind: sessionKindOpen}
	app.sessions[envSession.key] = envSession

	var emitted []activityLockEvent
	app.SetEmitter(func(name string, data ...any) {
		if name != activityQueueLockEvent || len(data) == 0 {
			return
		}
		if ev, ok := data[0].(activityLockEvent); ok {
			emitted = append(emitted, ev)
		}
	})

	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
		Release:     "team-devops",
	})

	app.lockTerminalsForActivity(entry)

	joinSession := &managedTerminal{selection: selection, key: "ai\x00team\x00dev", serial: 2, kind: sessionKindAI}
	app.mu.Lock()
	app.sessions[joinSession.key] = joinSession
	app.mu.Unlock()
	app.lockTerminalForActivity(joinSession.serial, entry)

	if len(emitted) == 0 {
		t.Fatal("expected lock events to be emitted")
	}
	lockedCount := 0
	for _, ev := range emitted {
		if !ev.Locked {
			continue
		}
		lockedCount++
		if strings.TrimSpace(ev.Reason) == "" {
			t.Fatalf("Locked=true event has empty Reason: %+v", ev)
		}
	}
	if lockedCount < 2 {
		t.Fatalf("expected at least 2 locked events (bulk + per-session join), got %d in %+v", lockedCount, emitted)
	}
}

func TestUnlockTerminalsForActivityClearsMatchingLocks(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := &managedTerminal{selection: selection, key: "env\x00team\x00dev", serial: 1, kind: sessionKindOpen}
	app.sessions[envSession.key] = envSession
	entry, _ := app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0"})
	app.lockTerminalsForActivity(entry)
	if envSession.lockedByActivity == "" {
		t.Fatal("session not locked at start")
	}
	final, _ := app.activityQueue.finish(entry.ID, activityQueueStatusSucceeded, "")
	app.unlockTerminalsForActivity(final)
	if envSession.lockedByActivity != "" {
		t.Fatalf("session still locked after unlock: %q", envSession.lockedByActivity)
	}
}

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
	// The failure line changed to "==> Deploy of <rel> failed after
	// <elapsed>"; the desktop matcher must still finalize the entry as
	// failed. The matcher was silently broken by that wording change.
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
	// A non-runtime component names the release after a ` · ` separator
	// ("erun/local · erun-backend-postgres 18.3"). The entry must be labeled
	// by component so the drawer does not read like a full-env redeploy, and
	// the version is the component's own.
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
	// The runtime chart's ==> Deploying line carries no release token; the
	// entry falls back to the runtime release name and is not
	// component-labeled (the runtime line shape is unchanged).
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
	// Regression: the trace handler used to look up the active deploy
	// entry by the *session selection's* tenant/env when ==> Deployed
	// arrived. If the trace appeared in a tab whose selection was empty
	// (a generic Local shell where the user invoked `erun open foo bar`
	// manually), the lookup failed and the entry stayed running forever.
	// The fix parses tenant/env directly out of the ==> Deployed line so
	// finalization works regardless of which tab observed it.
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
	// `==> Skipping <tenant>/<env> ...` is the dedup-skip outcome from
	// the deploy singleflight. Same finalization shape as ==> Deployed:
	// parse tenant/env from the line so a tab with no selection still
	// closes out the queue entry.
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
	// `erun build` umbrella traces don't carry tenant/env (build has no
	// deploy target), so the handler must key off the session selection.
	// Without this wiring the sidebar shows no busy spinner during a
	// build that the user explicitly invoked in a tenant/env-bound
	// terminal — that gap is the regression this scenario locks down.
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

func TestStartCommandFromTraceDoesNotLockTerminal(t *testing.T) {
	// Build/release/push run IN the user's terminal, so locking the
	// session would freeze the very tab they are reading output in.
	// Verify the path skips the lock that deploy uses to prevent
	// concurrent helm runs, for every command keyed off the selection.
	for _, command := range []string{"build", "release", "push"} {
		app := newTestAppForActivityQueue(t)
		selection := uiSelection{Tenant: "erun", Environment: "local"}
		envSession := &managedTerminal{selection: selection, key: "env\x00erun\x00local", serial: 1, kind: sessionKindOpen}
		app.sessions[envSession.key] = envSession
		app.startCommandFromTrace(selection, command)
		if envSession.lockedByActivity != "" {
			t.Fatalf("expected %s to not lock terminal, got %q", command, envSession.lockedByActivity)
		}
	}
}

func TestActivityTraceLineHandlerStartsAndFinishesRelease(t *testing.T) {
	// `erun release` (standalone) emits `==> Releasing`/`==> Released`
	// with no tenant/env, so the handler keys the activity off the
	// session selection — the same contract as build. Without this the
	// sidebar shows no spinner while a release the user kicked off in a
	// tenant/env-bound terminal runs.
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
	// `erun push` (standalone) emits `==> Pushing`/`==> Pushed`, keyed
	// off the session selection like build/release.
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

func TestResolveActivityKubeContextFallsBackToEnvConfig(t *testing.T) {
	// startDeployFromTrace registers entries with a kube context drawn
	// first from the session selection, falling back to the env config
	// on file. Without this fallback, generic Local tabs (selection
	// empty) emit entries with an empty KubernetesContext, and the
	// container-status poller's kubectl invocation hits whatever the
	// host's `current-context` happens to be — orbstack on a developer
	// machine — instead of the env's real cluster, so the deploy card
	// renders without container pills.
	app := newTestAppForActivityQueue(t)
	app.deps.store = stubUIStore{
		envs: map[string]eruncommon.EnvConfig{
			"erun/ux": {Name: "ux", KubernetesContext: "erun"},
		},
	}
	got := app.resolveActivityKubeContext(uiSelection{}, "erun", "ux")
	if got != "erun" {
		t.Fatalf("kube context = %q, want %q", got, "erun")
	}
	got = app.resolveActivityKubeContext(uiSelection{KubernetesContext: "session-context"}, "erun", "ux")
	if got != "session-context" {
		t.Fatalf("selection should win when present, got %q", got)
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

// TestApplyHelmReleaseSnapshotIgnoresStaleVersionOnDeployed guards the
// race the user observed: at deploy start the previous release still
// shows status="deployed" for a brief window before helm flips it to
// pending-upgrade. The helm poller must not finalize on that stale
// snapshot — its AppVersion is still the prior deploy's, not the
// version this entry is rolling out.
func TestApplyHelmReleaseSnapshotIgnoresStaleVersionOnDeployed(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.51-snapshot-20260510135933",
		Release:     "erun-devops",
		Namespace:   "erun-local",
		Source:      "trace",
	})

	app.applyHelmReleaseSnapshot("orbstack", helmReleaseSnapshot{
		Name:       "erun-devops",
		Namespace:  "erun-local",
		Status:     "deployed",
		AppVersion: "1.0.50-snapshot-prior",
		Updated:    helmUpdatedNow(t),
	})

	entry, ok := app.activityQueue.findActive("erun", "local")
	if !ok {
		t.Fatal("entry must remain active when helm reports a different AppVersion as deployed")
	}
	if entry.Status != activityQueueStatusRunning {
		t.Fatalf("entry status = %q, want running", entry.Status)
	}
}

// TestApplyHelmReleaseSnapshotIgnoresStaleTimestampOnDeployed covers
// the same-version redeploy case (common in snapshot workflows):
// AppVersion alone cannot distinguish the prior "deployed" snapshot
// from the new one when both carry the identical version string.
// The Updated freshness check rejects snapshots whose Updated is
// older than entry.StartedAt by more than the skew tolerance.
func TestApplyHelmReleaseSnapshotIgnoresStaleTimestampOnDeployed(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	now := time.Now()
	app.activityQueue.now = func() time.Time { return now }
	app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.51-snapshot-20260510135933",
		Release:     "erun-devops",
		Namespace:   "erun-local",
		Source:      "trace",
	})

	staleUpdated := now.Add(-(helmDeployedFreshnessSkew + 5*time.Minute))
	app.applyHelmReleaseSnapshot("orbstack", helmReleaseSnapshot{
		Name:       "erun-devops",
		Namespace:  "erun-local",
		Status:     "deployed",
		AppVersion: "1.0.51-snapshot-20260510135933",
		Updated:    staleUpdated.Format("2006-01-02 15:04:05.999999999 -0700 MST"),
	})

	entry, ok := app.activityQueue.findActive("erun", "local")
	if !ok {
		t.Fatal("entry must remain active when helm 'deployed' Updated predates entry.StartedAt")
	}
	if entry.Status != activityQueueStatusRunning {
		t.Fatalf("entry status = %q, want running", entry.Status)
	}
}

// TestApplyHelmReleaseSnapshotFinalizesOnFreshDeployedMatch verifies
// the happy path: AppVersion matches the entry's Version and Updated
// is fresh, so the snapshot describes the entry's own deploy.
func TestApplyHelmReleaseSnapshotFinalizesOnFreshDeployedMatch(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.51-snapshot-20260510135933",
		Release:     "erun-devops",
		Namespace:   "erun-local",
		Source:      "trace",
	})

	app.applyHelmReleaseSnapshot("orbstack", helmReleaseSnapshot{
		Name:       "erun-devops",
		Namespace:  "erun-local",
		Status:     "deployed",
		AppVersion: "1.0.51-snapshot-20260510135933",
		Updated:    helmUpdatedNow(t),
	})

	if _, stillActive := app.activityQueue.findActive("erun", "local"); stillActive {
		t.Fatal("entry should be finalized when version matches and Updated is fresh")
	}
	history := app.activityQueue.list()
	if len(history) != 1 || history[0].Status != activityQueueStatusSucceeded {
		t.Fatalf("expected one succeeded entry in history, got %+v", history)
	}
}

// TestApplyHelmReleaseSnapshotFinalizesOnFailedRegardlessOfVersion
// pins the failure path: the gating only applies to "deployed". A
// "failed" status must still finalize even when AppVersion doesn't
// match — if the PTY dies mid-deploy the trace handler can't fire
// `==> Deploy failed`, and we don't want entries stuck running.
func TestApplyHelmReleaseSnapshotFinalizesOnFailedRegardlessOfVersion(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.51-snapshot-20260510135933",
		Release:     "erun-devops",
		Namespace:   "erun-local",
		Source:      "trace",
	})

	app.applyHelmReleaseSnapshot("orbstack", helmReleaseSnapshot{
		Name:       "erun-devops",
		Namespace:  "erun-local",
		Status:     "failed",
		AppVersion: "1.0.50-snapshot-prior",
	})

	if _, stillActive := app.activityQueue.findActive("erun", "local"); stillActive {
		t.Fatal("entry should be finalized when helm reports failed")
	}
	history := app.activityQueue.list()
	if len(history) != 1 || history[0].Status != activityQueueStatusFailed {
		t.Fatalf("expected one failed entry in history, got %+v", history)
	}
}

// TestParseHelmUpdatedAcceptsHelmFormats covers the `helm list -o json`
// timestamp shapes parseHelmUpdated must handle.
func TestParseHelmUpdatedAcceptsHelmFormats(t *testing.T) {
	cases := []string{
		"2026-05-10 17:00:26.926452 +0300 EEST",
		"2026-05-10 17:00:26 +0300 EEST",
		"2026-05-10 17:00:26.926452 +0300",
		"2026-05-10T17:00:26.926452+03:00",
		"2026-05-10T17:00:26+03:00",
	}
	for _, value := range cases {
		if _, ok := parseHelmUpdated(value); !ok {
			t.Errorf("parseHelmUpdated rejected %q", value)
		}
	}
	if _, ok := parseHelmUpdated(""); ok {
		t.Error("parseHelmUpdated must reject empty input")
	}
	if _, ok := parseHelmUpdated("not a timestamp"); ok {
		t.Error("parseHelmUpdated must reject garbage input")
	}
}

// TestHelmListArgsAvoidsDeprecatedAllFlag pins the arguments we pass to
// `helm list`. helm v4 removed the `--all` umbrella flag; if it slips
// back in, every poll errors out, the whole reconcile channel goes
// silent, and entries get stuck running in the activity panel without
// any visible failure mode.
func TestHelmListArgsAvoidsDeprecatedAllFlag(t *testing.T) {
	args := helmListTenantDevopsArgs("erun")
	for _, a := range args {
		if a == "--all" {
			t.Fatalf("--all is deprecated in helm v4; args = %v", args)
		}
	}
	for _, want := range []string{"--deployed", "--pending", "--failed", "--uninstalling"} {
		if !containsString(args, want) {
			t.Errorf("missing %s in helm list args: %v", want, args)
		}
	}
	if !containsPair(args, "--kube-context", "erun") {
		t.Errorf("expected --kube-context erun in args: %v", args)
	}
	bare := helmListTenantDevopsArgs("")
	if containsString(bare, "--kube-context") {
		t.Errorf("empty kube context should not append --kube-context; args = %v", bare)
	}
}

func containsString(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsPair(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

// helmUpdatedNow returns the current time formatted in helm's default
// `helm list -o json` Updated layout, for use in test snapshots.
func helmUpdatedNow(t *testing.T) string {
	t.Helper()
	return time.Now().Format("2006-01-02 15:04:05.999999999 -0700 MST")
}

func TestNamespaceForTenantEnv(t *testing.T) {
	cases := []struct {
		tenant, environment, want string
	}{
		{"team", "dev", "team-dev"},
		{"team", "", "team"},
		{"", "dev", "dev"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := namespaceForTenantEnv(c.tenant, c.environment); got != c.want {
			t.Fatalf("namespaceForTenantEnv(%q,%q) = %q, want %q", c.tenant, c.environment, got, c.want)
		}
	}
}

func TestReleaseNameForTenant(t *testing.T) {
	if got := releaseNameForTenant("team"); got != "team-devops" {
		t.Fatalf("got %q, want team-devops", got)
	}
	if got := releaseNameForTenant("  spaced  "); got != "spaced-devops" {
		t.Fatalf("got %q, want spaced-devops", got)
	}
	if got := releaseNameForTenant(""); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// TestPollActivityContainerStatusesDoesNotFinalizeOnReadyPods pins the
// display-only contract for the pod-status poller. It must not mark an
// entry succeeded just because every container is currently Ready —
// pod readiness can flip a few seconds before helm's `--wait` returns,
// so finalizing here would beat the trace handler's `==> Deployed` and
// the activity panel would show "done" while the user's terminal still
// shows the deploy spinning. Completion is owned by the trace handler
// and the helm poller's version+freshness check, both of which match
// the runtime CLI's actual return.
func TestPollActivityContainerStatusesDoesNotFinalizeOnReadyPods(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.0",
		Release:     "erun-devops",
		Source:      "trace",
	})

	allReady := []activityQueueContainerStatus{
		{Name: "erun-devops", Phase: "Running", Ready: true},
		{Name: "erun-dind", Phase: "Running", Ready: true},
		{Name: "erun-mcp", Phase: "Running", Ready: true},
	}
	for i := 0; i < 5; i++ {
		app.activityQueue.updateContainers(entry.ID, allReady)
	}

	if _, stillActive := app.activityQueue.findActive("erun", "local"); !stillActive {
		t.Fatal("trace-source entry must remain active even when every container reports Ready")
	}
}

func TestForceDismissActivityRemovesActiveEntry(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.0",
		Release:     "erun-devops",
	})
	if !app.ForceDismissActivity(entry.ID) {
		t.Fatal("ForceDismissActivity should return true for an active entry")
	}
	if _, ok := app.activityQueue.findActive("erun", "local"); ok {
		t.Fatal("entry should be removed from active after force dismiss")
	}
	if app.ForceDismissActivity(entry.ID) {
		t.Fatal("second ForceDismissActivity should return false (entry already gone)")
	}
}

// TestFeedActivityTraceCapturesFailureDetail pins the end-to-end wiring: PTY
// output fed through feedActivityTraceFromTerminal is buffered against the
// active entry and snapshotted into entry.Detail when the "==> Deploy failed"
// trace line finalizes the entry. This locks the record-before-finalize
// ordering — the failing output must already be buffered when finish() runs.
func TestFeedActivityTraceCapturesFailureDetail(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	managed := &managedTerminal{selection: selection, kind: sessionKindLocal, key: "local\x00team\x00dev", serial: 1}
	app.sessions[managed.key] = managed

	// Seed the active entry the trace handler would otherwise create on the
	// "==> Deploying" line; this test isolates the capture path from the
	// trace-driven start (which needs env config on disk).
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
