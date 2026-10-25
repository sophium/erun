package main

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func newTestAppForActivityQueue(t *testing.T) *App {
	t.Helper()
	app := withTestTerminal(&App{})
	app.activityQueue = newActivityQueueStore(nil, nil)
	return withTestEnvops(app)
}

func TestLockTerminalsForActivityLocksMatchingSessions(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uimodel.Selection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := &terminal.Session{Selection: selection, Key: "env\x00team\x00dev", Serial: 1, Kind: terminal.KindOpen}
	aiSession := &terminal.Session{Selection: selection, Key: "ai\x00team\x00dev", Serial: 2, Kind: terminal.KindAI}
	localSession := &terminal.Session{Selection: selection, Key: "local\x00team\x00dev", Serial: 3, Kind: terminal.KindLocal}
	otherSelection := uimodel.Selection{Tenant: "other", Environment: "dev", Version: "1.0.0"}
	unrelated := &terminal.Session{Selection: otherSelection, Key: "env\x00other\x00dev", Serial: 4, Kind: terminal.KindOpen}
	setTestSession(app, envSession.Key, envSession)
	setTestSession(app, aiSession.Key, aiSession)
	setTestSession(app, localSession.Key, localSession)
	setTestSession(app, unrelated.Key, unrelated)

	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
		Release:     "team-devops",
	})
	app.lockTerminalsForActivity(entry)

	if envSession.LockedByActivity != entry.ID {
		t.Fatalf("env session not locked: %q want %q", envSession.LockedByActivity, entry.ID)
	}
	if aiSession.LockedByActivity != entry.ID {
		t.Fatalf("ai session not locked: %q", aiSession.LockedByActivity)
	}
	if localSession.LockedByActivity != "" {
		t.Fatalf("local session unexpectedly locked: %q", localSession.LockedByActivity)
	}
	if unrelated.LockedByActivity != "" {
		t.Fatalf("unrelated tenant locked: %q", unrelated.LockedByActivity)
	}
}

// Starting a deploy acts on any outstanding env-scoped warning.
func TestLockTerminalsForActivityClearsEnvNotifications(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	selection := uimodel.Selection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := &terminal.Session{Selection: selection, Key: "env\x00team\x00dev", Serial: 1, Kind: terminal.KindOpen}
	setTestSession(app, envSession.Key, envSession)

	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
		Release:     "team-devops",
	})
	app.lockTerminalsForActivity(entry)

	events := emits.events(uimodel.AppNotificationClearEvent)
	if len(events) != 1 {
		t.Fatalf("clear emitted %d times, want exactly 1", len(events))
	}
	payload, ok := events[0].(uimodel.AppNotificationClearPayload)
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
	selection := uimodel.Selection{Tenant: "frs", Environment: "local"}
	onLine := newActivityTraceLineHandler(app, selection, terminal.KindLocal)

	onLine(`==> Deploy failed frs/local: values file not found for environment "local"`)

	notes := emits.events(uimodel.AppNotificationEvent)
	if len(notes) != 1 {
		t.Fatalf("deploy failure emitted %d toolbar notifications, want 1", len(notes))
	}
	note, ok := notes[0].(uimodel.AppNotificationPayload)
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
	if got := len(emits.events(uimodel.EnvStatusEvent)); got != 1 {
		t.Fatalf("failed sidebar status emitted %d times, want 1", got)
	}
}

func TestLockTerminalsForActivityWithoutMatchClearsNothing(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	other := &terminal.Session{
		Selection: uimodel.Selection{Tenant: "other", Environment: "dev"},
		Key:       "env\x00other\x00dev", Serial: 1, Kind: terminal.KindOpen,
	}
	setTestSession(app, other.Key, other)

	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0", Release: "team-devops",
	})
	app.lockTerminalsForActivity(entry)

	if got := len(emits.events(uimodel.AppNotificationClearEvent)); got != 0 {
		t.Fatalf("clear emitted %d times with no matching session, want 0", got)
	}
}

func TestLockNewlyJoinedSessionIfDeployInFlightLocksImmediately(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	selection := uimodel.Selection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	entry, _ := app.activityQueue.start(activityQueueEntry{
		Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0", Release: "team-devops",
	})

	joinSession := &terminal.Session{Selection: selection, Key: "ai\x00team\x00dev", Serial: 7, Kind: terminal.KindAI}
	l, unlock := lockTerminalForTest(app)
	l.Register(joinSession)
	unlock()

	app.lockNewlyJoinedSessionIfDeployInFlight(selection, joinSession.Serial)

	if joinSession.LockedByActivity != entry.ID {
		t.Fatalf("joinSession.lockedByActivity = %q, want %q", joinSession.LockedByActivity, entry.ID)
	}
	if got := len(emits.events(activityQueueLockEvent)); got != 1 {
		t.Fatalf("lock events emitted = %d, want 1", got)
	}
}

func TestLockNewlyJoinedSessionIfDeployInFlightNoOpWithoutADeploy(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	joinSession := &terminal.Session{Selection: selection, Key: "ai\x00team\x00dev", Serial: 7, Kind: terminal.KindAI}
	l, unlock := lockTerminalForTest(app)
	l.Register(joinSession)
	unlock()

	app.lockNewlyJoinedSessionIfDeployInFlight(selection, joinSession.Serial)

	if joinSession.LockedByActivity != "" {
		t.Fatalf("joinSession.lockedByActivity = %q, want empty", joinSession.LockedByActivity)
	}
	if got := len(emits.events(activityQueueLockEvent)); got != 0 {
		t.Fatalf("lock events emitted = %d, want 0", got)
	}
}

// The overlay has no fallback, so every Locked=true event needs a Reason.
func TestLockTerminalEventsAlwaysCarryReason(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uimodel.Selection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := &terminal.Session{Selection: selection, Key: "env\x00team\x00dev", Serial: 1, Kind: terminal.KindOpen}
	setTestSession(app, envSession.Key, envSession)

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

	joinSession := &terminal.Session{Selection: selection, Key: "ai\x00team\x00dev", Serial: 2, Kind: terminal.KindAI}
	l, unlock := lockTerminalForTest(app)
	l.Register(joinSession)
	unlock()
	app.lockTerminalForActivity(joinSession.Serial, entry)

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
	selection := uimodel.Selection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := &terminal.Session{Selection: selection, Key: "env\x00team\x00dev", Serial: 1, Kind: terminal.KindOpen}
	setTestSession(app, envSession.Key, envSession)
	entry, _ := app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0"})
	app.lockTerminalsForActivity(entry)
	if envSession.LockedByActivity == "" {
		t.Fatal("session not locked at start")
	}
	final, _ := app.activityQueue.finish(entry.ID, activityQueueStatusSucceeded, "")
	app.unlockTerminalsForActivity(final)
	if envSession.LockedByActivity != "" {
		t.Fatalf("session still locked after unlock: %q", envSession.LockedByActivity)
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
