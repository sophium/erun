package main

import (
	"strings"
	"testing"
	"time"
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
