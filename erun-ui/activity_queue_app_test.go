package main

import (
	"strings"
	"testing"
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

func TestFinalizeDeployFromPodReadinessTransitionsToSucceeded(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.0",
		Release:     "erun-devops",
	})
	app.finalizeDeployFromPodReadiness(entry.ID)
	if _, stillActive := app.activityQueue.findActive("erun", "local"); stillActive {
		t.Fatal("entry should be moved out of active after pod-readiness finalize")
	}
	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusSucceeded {
		t.Fatalf("expected one succeeded entry in history, got %+v", all)
	}
}

func TestAllContainersReadyAndHealthyHandlesFailureReasons(t *testing.T) {
	healthy := []activityQueueContainerStatus{
		{Name: "a", Phase: "Running", Ready: true},
		{Name: "b", Phase: "Running", Ready: true},
	}
	if !allContainersReadyAndHealthy(healthy) {
		t.Fatal("expected healthy snapshot to be Ready+healthy")
	}
	withImagePullBackoff := []activityQueueContainerStatus{
		{Name: "a", Phase: "Running", Ready: true},
		{Name: "b", Phase: "Waiting", Ready: true, Reason: "ImagePullBackOff"},
	}
	if allContainersReadyAndHealthy(withImagePullBackoff) {
		t.Fatal("a Ready=true container with ImagePullBackOff reason must not pass the gate")
	}
	withTerminated := []activityQueueContainerStatus{
		{Name: "a", Phase: "Running", Ready: true},
		{Name: "b", Phase: "Terminated", Ready: true},
	}
	if allContainersReadyAndHealthy(withTerminated) {
		t.Fatal("a terminated container must not pass the gate")
	}
	if allContainersReadyAndHealthy(nil) {
		t.Fatal("empty snapshot must not pass the gate")
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
