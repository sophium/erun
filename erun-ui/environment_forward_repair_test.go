package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// These tests lock the repair for the failure that has no other witness: a
// kubectl port-forward whose pod was replaced keeps its local listener bound
// and answers nothing, so every check that stops at the listener reports the
// environment as healthy while every client of it is dead.
//
// The four behaviours below are the contract: a bound-but-dead forward is
// re-established, a working one is left alone, a repair that cannot succeed
// reports instead of looping, and an environment the operator stopped is not
// woken by any of it.

const forwardRepairTestPort = 17500

// forwardRepairProbe is the environment's side of the sweep: whether its edge
// answers, and how many rebinds the desktop has asked for. hold, when set, keeps
// a rebind in flight until the test releases it.
type forwardRepairProbe struct {
	mu          sync.Mutex
	edgeAnswers bool
	reconnects  int
	hold        chan struct{}
}

func (p *forwardRepairProbe) answers() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.edgeAnswers
}

func (p *forwardRepairProbe) setAnswers(value bool) {
	p.mu.Lock()
	p.edgeAnswers = value
	p.mu.Unlock()
}

func (p *forwardRepairProbe) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reconnects
}

func (p *forwardRepairProbe) reconnect() error {
	p.mu.Lock()
	p.reconnects++
	hold := p.hold
	p.mu.Unlock()
	if hold != nil {
		<-hold
	}
	return nil
}

func forwardRepairTestApp(
	t *testing.T,
	probe *forwardRepairProbe,
	readRunState func(eruncommon.Context, eruncommon.RuntimeScaleTarget) (eruncommon.RuntimeRunState, error),
) (*App, *capturedEmits) {
	t.Helper()
	seedMCPForward(t, "erun", "remote", forwardRepairTestPort)
	emits := newCapturedEmits()
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}
	app := NewApp(erunUIDeps{
		store:               store,
		findProjectRoot:     func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:      func() string { return "/tmp/erun" },
		canConnectLocalPort: func(int) bool { return true },
		canReachMCPEndpoint: func(int) bool { return probe.answers() },
		loadIdleStatus: func(context.Context, string, string) (eruncommon.EnvironmentIdleStatus, error) {
			if !probe.answers() {
				return eruncommon.EnvironmentIdleStatus{}, errors.New("edge did not answer")
			}
			return eruncommon.EnvironmentIdleStatus{
				Markers: []eruncommon.EnvironmentIdleMarker{{Name: eruncommon.ActivityKindProcess, Idle: true}},
			}, nil
		},
		reconnectMCP: func(context.Context, eruncommon.OpenResult, func(string)) error {
			return probe.reconnect()
		},
		readRuntimeRunState: readRunState,
	})
	// A non-nil ctx un-gates the rebind (it no-ops while ctx is nil); the
	// emitter must be stubbed alongside it or emits reach the real Wails
	// runtime outside a lifecycle context.
	app.ctx = context.Background()
	app.SetEmitter(emits.fn())
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app, emits
}

func forwardRepairSelection() uiSelection {
	return uiSelection{Tenant: "erun", Environment: "remote"}
}

// sweepUntilRebindSettles runs one sweep and waits for whatever rebind it
// started to finish, so the next sweep is not silently suppressed by the
// in-flight latch. Bounded by a real condition, never by a fixed sleep.
func sweepUntilRebindSettles(t *testing.T, app *App) environmentActivityState {
	t.Helper()
	state := app.observeEnvironmentActivity(forwardRepairSelection()).state
	key := selectionKey(forwardRepairSelection())
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		app.envEnsureMu.Lock()
		_, inflight := app.envEnsureInflight[key]
		app.envEnsureMu.Unlock()
		if !inflight {
			return state
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("a rebind started by the sweep never finished")
	return state
}

func waitForReconnects(t *testing.T, probe *forwardRepairProbe, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if probe.count() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("rebind count = %d, want %d", probe.count(), want)
}

// TestStaleForwardIsReestablishedWithoutDuplicatingIt is the headline
// behaviour: the sweep that finds a bound-but-dead forward re-establishes it,
// and a second sweep landing while that repair is still running does not start
// a competing one for the same local port.
func TestStaleForwardIsReestablishedWithoutDuplicatingIt(t *testing.T) {
	probe := &forwardRepairProbe{hold: make(chan struct{})}
	app, _ := forwardRepairTestApp(t, probe, nil)

	state := app.observeEnvironmentActivity(forwardRepairSelection()).state
	if !state.reachable {
		t.Fatal("a forward whose port is held is still reachable; that is exactly what makes it deceptive")
	}
	if state.observed {
		t.Fatal("an edge that answers nothing has not answered the idle question")
	}
	if state.stale {
		t.Fatal("the first failing sweep repairs the forward; it does not yet diagnose it as unrepairable")
	}
	waitForReconnects(t, probe, 1)

	// The repair is still in flight. A second sweep must not race it for the
	// port — one rebind per environment at a time is what keeps two forwards
	// off one local port.
	app.observeEnvironmentActivity(forwardRepairSelection())
	close(probe.hold)
	waitForReconnects(t, probe, 1)
	if got := probe.count(); got != 1 {
		t.Fatalf("overlapping sweeps started %d rebinds, want 1", got)
	}
}

// TestServingForwardIsLeftAlone covers the other half of "detect it honestly":
// a forward that carries traffic is never restarted, including when the idle
// question itself fails. An edge that replies at all is a live tunnel, and
// restarting one on the strength of a failed query would trade an outage
// nobody repairs for an outage the desktop causes.
func TestServingForwardIsLeftAlone(t *testing.T) {
	probe := &forwardRepairProbe{edgeAnswers: true}
	app, _ := forwardRepairTestApp(t, probe, nil)

	for range 3 {
		state := sweepUntilRebindSettles(t, app)
		if !state.reachable || !state.observed {
			t.Fatalf("an environment that answered is reachable and observed, got %+v", state)
		}
		if state.stale {
			t.Fatal("an environment that answered is not a stale forward")
		}
	}
	if got := probe.count(); got != 0 {
		t.Fatalf("a working forward was restarted %d times, want 0", got)
	}

	// The fussy-edge case: the tunnel carries traffic, but the idle query
	// against it fails. That is a question the environment declined, not a
	// forward pointed at a vanished pod.
	app.deps.loadIdleStatus = func(context.Context, string, string) (eruncommon.EnvironmentIdleStatus, error) {
		return eruncommon.EnvironmentIdleStatus{}, errors.New("401 unauthorized")
	}
	state := sweepUntilRebindSettles(t, app)
	if !state.reachable || state.observed {
		t.Fatalf("a declined idle query is reachable without a verdict, got %+v", state)
	}
	if state.stale {
		t.Fatal("an edge that replies is not a stale forward")
	}
	if got := probe.count(); got != 0 {
		t.Fatalf("a declined idle query caused %d rebinds, want 0", got)
	}
}

// TestUnrepairableForwardReportsInsteadOfLooping locks the bound: a forward
// that stays dead through every attempt stops being retried and starts being
// reported — once — and the row renders it as an outage rather than as an
// ordinary quiet environment. Recovering ends the episode and retires the
// report.
func TestUnrepairableForwardReportsInsteadOfLooping(t *testing.T) {
	probe := &forwardRepairProbe{}
	app, emits := forwardRepairTestApp(t, probe, nil)

	for attempt := range forwardRepairAttempts {
		if state := sweepUntilRebindSettles(t, app); state.stale {
			t.Fatalf("sweep %d diagnosed the forward while attempts remained", attempt+1)
		}
	}
	if got := probe.count(); got != forwardRepairAttempts {
		t.Fatalf("rebind count = %d, want the episode's bound of %d", got, forwardRepairAttempts)
	}

	// Attempts are spent. From here the sweep reports and stops spending.
	for range 3 {
		if state := sweepUntilRebindSettles(t, app); !state.stale {
			t.Fatal("an exhausted episode must render the environment as unreachable, not as quiet")
		}
	}
	if got := probe.count(); got != forwardRepairAttempts {
		t.Fatalf("the repair kept looping: %d rebinds, want it bounded at %d", got, forwardRepairAttempts)
	}

	notes := notificationsFromSource(emits, notificationSourceForwardStale)
	if len(notes) != 1 {
		t.Fatalf("stale-forward notification posted %d times, want exactly 1 per episode", len(notes))
	}
	if notes[0].Kind != "warning" {
		t.Fatalf("notification kind = %q, want warning", notes[0].Kind)
	}
}

// TestRecoveredForwardEndsTheEpisode is the other side of the bound: an
// exhausted episode must not be a permanent verdict. A forward that carries
// traffic again retires its report, and the next failure is repaired from zero
// rather than reported on sight.
func TestRecoveredForwardEndsTheEpisode(t *testing.T) {
	probe := &forwardRepairProbe{}
	app, emits := forwardRepairTestApp(t, probe, nil)

	for range forwardRepairAttempts + 1 {
		sweepUntilRebindSettles(t, app)
	}

	probe.setAnswers(true)
	if state := sweepUntilRebindSettles(t, app); state.stale {
		t.Fatal("a forward that carries traffic again is not stale")
	}
	if got := len(clearedNotificationSources(emits, notificationSourceForwardStale)); got != 1 {
		t.Fatalf("recovery cleared the stale-forward notification %d times, want 1", got)
	}

	probe.setAnswers(false)
	if state := sweepUntilRebindSettles(t, app); state.stale {
		t.Fatal("a fresh failure must be repaired before it is reported")
	}
	if got := probe.count(); got != forwardRepairAttempts+1 {
		t.Fatalf("rebind count = %d, want a fresh episode's first attempt", got)
	}
}

// TestStoppedEnvironmentIsNotResurrected is the guard on the repair's blast
// radius. A stopped environment's forward is *supposed* to be dead, so
// repairing it would undo the operator's own stop and rewrite it as an outage.
func TestStoppedEnvironmentIsNotResurrected(t *testing.T) {
	probe := &forwardRepairProbe{}
	app, emits := forwardRepairTestApp(t, probe,
		func(eruncommon.Context, eruncommon.RuntimeScaleTarget) (eruncommon.RuntimeRunState, error) {
			return eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 0}, nil
		})

	for range forwardRepairAttempts + 2 {
		if state := sweepUntilRebindSettles(t, app); state.stale {
			t.Fatal("a stopped environment is stopped, not an unreachable one")
		}
	}
	if got := probe.count(); got != 0 {
		t.Fatalf("a stopped environment was woken by %d rebinds, want 0", got)
	}
	if got := notificationsFromSource(emits, notificationSourceForwardStale); len(got) != 0 {
		t.Fatalf("a stopped environment reported %d outages, want 0", len(got))
	}
}

func notificationsFromSource(emits *capturedEmits, source string) []appNotificationPayload {
	var out []appNotificationPayload
	for _, event := range emits.events(appNotificationEvent) {
		note, ok := event.(appNotificationPayload)
		if ok && note.Source == source {
			out = append(out, note)
		}
	}
	return out
}

func clearedNotificationSources(emits *capturedEmits, source string) []appNotificationClearPayload {
	var out []appNotificationClearPayload
	for _, event := range emits.events(appNotificationClearEvent) {
		cleared, ok := event.(appNotificationClearPayload)
		if ok && cleared.Source == source {
			out = append(out, cleared)
		}
	}
	return out
}
