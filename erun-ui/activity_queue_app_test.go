package main

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

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

// Starting a deploy acts on any outstanding env-scoped warning.
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

func TestLockNewlyJoinedSessionIfDeployInFlightLocksImmediately(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0", Release: "team-devops",
	})

	joinSession := &managedTerminal{selection: selection, key: "ai\x00team\x00dev", serial: 7, kind: sessionKindAI}
	app.mu.Lock()
	app.sessions[joinSession.key] = joinSession
	app.mu.Unlock()

	app.lockNewlyJoinedSessionIfDeployInFlight(selection, joinSession.serial)

	if joinSession.lockedByActivity != entry.ID {
		t.Fatalf("joinSession.lockedByActivity = %q, want %q", joinSession.lockedByActivity, entry.ID)
	}
	if got := len(emits.events(activityQueueLockEvent)); got != 1 {
		t.Fatalf("lock events emitted = %d, want 1", got)
	}
}

func TestLockNewlyJoinedSessionIfDeployInFlightNoOpWithoutADeploy(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	selection := uiSelection{Tenant: "team", Environment: "dev"}
	joinSession := &managedTerminal{selection: selection, key: "ai\x00team\x00dev", serial: 7, kind: sessionKindAI}
	app.mu.Lock()
	app.sessions[joinSession.key] = joinSession
	app.mu.Unlock()

	app.lockNewlyJoinedSessionIfDeployInFlight(selection, joinSession.serial)

	if joinSession.lockedByActivity != "" {
		t.Fatalf("joinSession.lockedByActivity = %q, want empty", joinSession.lockedByActivity)
	}
	if got := len(emits.events(activityQueueLockEvent)); got != 0 {
		t.Fatalf("lock events emitted = %d, want 0", got)
	}
}

// The overlay has no fallback, so every Locked=true event needs a Reason.
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

// Pods can be Ready before helm --wait returns, so the poller must not finalize.
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

// With a separately stated chart, appVersion is the chart's version, not the runtime's.
func TestObservedRuntimeVersionPrefersTheRecordedVersionWhenTheChartIsStated(t *testing.T) {
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{"petios": {Name: "petios"}},
		envs: map[string]config.EnvConfig{
			"petios/develop": {
				Name:           "develop",
				RuntimeVersion: "1.0.322-snapshot-20260813072257",
				RuntimeChart:   "oci://ghcr.io/sophium/charts/erun-devops:1.0.178",
			},
			"petios/paired": {
				Name:           "paired",
				RuntimeVersion: "1.0.178",
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})
	release := helmReleaseSnapshot{Name: "petios-devops", AppVersion: "1.0.178"}

	if got := app.observedRuntimeVersion("petios", "develop", release); got != "1.0.322-snapshot-20260813072257" {
		t.Fatalf("expected the recorded runtime version, got %q", got)
	}
	// When chart and image share a line, appVersion is the live version.
	if got := app.observedRuntimeVersion("petios", "paired", release); got != "1.0.178" {
		t.Fatalf("expected the release appVersion for a paired env, got %q", got)
	}
	if got := app.observedRuntimeVersion("petios", "unknown", release); got != "1.0.178" {
		t.Fatalf("expected the release appVersion when the env is unknown, got %q", got)
	}
}
