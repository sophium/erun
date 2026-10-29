package activity

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestLockTerminalsForActivityLocksMatchingSessions(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := Session{Selection: selection, Serial: 1, Kind: KindOpen}
	aiSession := Session{Selection: selection, Serial: 2, Kind: KindAI}
	localSession := Session{Selection: selection, Serial: 3, Kind: KindLocal}
	otherSelection := uimodel.Selection{Tenant: "other", Environment: "dev", Version: "1.0.0"}
	unrelated := Session{Selection: otherSelection, Serial: 4, Kind: KindOpen}
	app.sessions.add(envSession)
	app.sessions.add(aiSession)
	app.sessions.add(localSession)
	app.sessions.add(unrelated)

	entry, _ := app.queue.start(Entry{
		Command:     "deploy",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
		Release:     "team-devops",
	})
	app.lockTerminalsForActivity(entry)

	if app.sessions.lockedBy(envSession.Serial) != entry.ID {
		t.Fatalf("env session not locked: %q want %q", app.sessions.lockedBy(envSession.Serial), entry.ID)
	}
	if app.sessions.lockedBy(aiSession.Serial) != entry.ID {
		t.Fatalf("ai session not locked: %q", app.sessions.lockedBy(aiSession.Serial))
	}
	if app.sessions.lockedBy(localSession.Serial) != "" {
		t.Fatalf("local session unexpectedly locked: %q", app.sessions.lockedBy(localSession.Serial))
	}
	if app.sessions.lockedBy(unrelated.Serial) != "" {
		t.Fatalf("unrelated tenant locked: %q", app.sessions.lockedBy(unrelated.Serial))
	}
}

// Starting a deploy acts on any outstanding env-scoped warning.
func TestLockTerminalsForActivityClearsEnvNotifications(t *testing.T) {
	app := newHarness(Deps{})
	emits := app.emits
	selection := uimodel.Selection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := Session{Selection: selection, Serial: 1, Kind: KindOpen}
	app.sessions.add(envSession)

	entry, _ := app.queue.start(Entry{
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
	app := newHarness(Deps{})
	emits := app.emits
	selection := uimodel.Selection{Tenant: "frs", Environment: "local"}
	onLine := app.TraceLineHandler(selection, KindLocal)

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
		note.Source != NotificationSourceDeployFailed {
		t.Fatalf("notification = %+v, want error frs/local %q", note, NotificationSourceDeployFailed)
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
	app := newHarness(Deps{})
	emits := app.emits
	other := Session{
		Selection: uimodel.Selection{Tenant: "other", Environment: "dev"},
		Serial:    1, Kind: KindOpen,
	}
	app.sessions.add(other)

	entry, _ := app.queue.start(Entry{
		Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0", Release: "team-devops",
	})
	app.lockTerminalsForActivity(entry)

	if got := len(emits.events(uimodel.AppNotificationClearEvent)); got != 0 {
		t.Fatalf("clear emitted %d times with no matching session, want 0", got)
	}
}

func TestLockNewlyJoinedSessionIfDeployInFlightLocksImmediately(t *testing.T) {
	app := newHarness(Deps{})
	emits := app.emits

	selection := uimodel.Selection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	entry, _ := app.queue.start(Entry{
		Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0", Release: "team-devops",
	})

	joinSession := Session{Selection: selection, Serial: 7, Kind: KindAI}
	app.sessions.add(joinSession)

	app.LockIfDeployInFlight(selection, joinSession.Serial)

	if app.sessions.lockedBy(joinSession.Serial) != entry.ID {
		t.Fatalf("joinSession.lockedByActivity = %q, want %q", app.sessions.lockedBy(joinSession.Serial), entry.ID)
	}
	if got := len(emits.events(lockEvent)); got != 1 {
		t.Fatalf("lock events emitted = %d, want 1", got)
	}
}

func TestLockNewlyJoinedSessionIfDeployInFlightNoOpWithoutADeploy(t *testing.T) {
	app := newHarness(Deps{})
	emits := app.emits

	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	joinSession := Session{Selection: selection, Serial: 7, Kind: KindAI}
	app.sessions.add(joinSession)

	app.LockIfDeployInFlight(selection, joinSession.Serial)

	if app.sessions.lockedBy(joinSession.Serial) != "" {
		t.Fatalf("joinSession.lockedByActivity = %q, want empty", app.sessions.lockedBy(joinSession.Serial))
	}
	if got := len(emits.events(lockEvent)); got != 0 {
		t.Fatalf("lock events emitted = %d, want 0", got)
	}
}

// The overlay has no fallback, so every Locked=true event needs a Reason.
func TestLockTerminalEventsAlwaysCarryReason(t *testing.T) {
	var emitted []lockPayload
	app := newHarness(Deps{Emit: func(name string, data ...any) {
		if name != lockEvent || len(data) == 0 {
			return
		}
		if ev, ok := data[0].(lockPayload); ok {
			emitted = append(emitted, ev)
		}
	}})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := Session{Selection: selection, Serial: 1, Kind: KindOpen}
	app.sessions.add(envSession)

	entry, _ := app.queue.start(Entry{
		Command:     "deploy",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
		Release:     "team-devops",
	})

	app.lockTerminalsForActivity(entry)

	joinSession := Session{Selection: selection, Serial: 2, Kind: KindAI}
	app.sessions.add(joinSession)
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
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := Session{Selection: selection, Serial: 1, Kind: KindOpen}
	app.sessions.add(envSession)
	entry, _ := app.queue.start(Entry{Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0"})
	app.lockTerminalsForActivity(entry)
	if app.sessions.lockedBy(envSession.Serial) == "" {
		t.Fatal("session not locked at start")
	}
	final, _ := app.queue.finish(entry.ID, StatusSucceeded, "")
	app.unlockTerminalsForActivity(final)
	if app.sessions.lockedBy(envSession.Serial) != "" {
		t.Fatalf("session still locked after unlock: %q", app.sessions.lockedBy(envSession.Serial))
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
	app := newHarness(Deps{})
	entry, _ := app.queue.start(Entry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.0",
		Release:     "erun-devops",
		Source:      "trace",
	})

	allReady := []ContainerStatus{
		{Name: "erun-devops", Phase: "Running", Ready: true},
		{Name: "erun-dind", Phase: "Running", Ready: true},
		{Name: "erun-mcp", Phase: "Running", Ready: true},
	}
	for i := 0; i < 5; i++ {
		app.queue.updateContainers(entry.ID, allReady)
	}

	if _, stillActive := app.queue.findActive("erun", "local"); !stillActive {
		t.Fatal("trace-source entry must remain active even when every container reports Ready")
	}
}

func TestForceDismissActivityRemovesActiveEntry(t *testing.T) {
	app := newHarness(Deps{})
	entry, _ := app.queue.start(Entry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.0",
		Release:     "erun-devops",
	})
	if !app.ForceDismissActivity(entry.ID) {
		t.Fatal("ForceDismissActivity should return true for an active entry")
	}
	if _, ok := app.queue.findActive("erun", "local"); ok {
		t.Fatal("entry should be removed from active after force dismiss")
	}
	if app.ForceDismissActivity(entry.ID) {
		t.Fatal("second ForceDismissActivity should return false (entry already gone)")
	}
}

// With a separately stated chart, appVersion is the chart's version, not the runtime's.
func TestObservedRuntimeVersionPrefersTheRecordedVersionWhenTheChartIsStated(t *testing.T) {
	store := fakeStore{
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
	app := newTestService(Deps{Store: store})
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
