package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// These tests lock the repair for the failure that has no other witness: an
// environment that was open loses the port-forward carrying its MCP edge, and
// nothing starts a replacement. Both shapes are covered, because both produce
// the same silence. The pod replacement usually makes kubectl exit outright and
// free the local port (dropped), which every check reads as an environment
// nobody opened; occasionally the listener outlives its far end (stale), which
// every check that stops at the listener reads as healthy.
//
// The behaviours below are the contract: a dropped forward and a bound-but-dead
// one are both re-established, a working one is left alone, an environment that
// was never opened is not touched at all, a repair that cannot succeed reports
// instead of looping, and an environment the operator stopped is not woken by
// any of it.

const forwardRepairTestPort = 17500

// forwardRepairProbe is the environment's side of the sweep: whether anything
// holds its local port, whether its edge answers, and how many rebinds the
// desktop has asked for. hold, when set, keeps a rebind in flight until the
// test releases it; repairs, when set, makes a rebind actually work, which is
// what `erun open --reconnect` does when the runtime is there to reconnect to.
type forwardRepairProbe struct {
	mu sync.Mutex
	// forwardDropped is the ordinary shape of the failure: kubectl exited with
	// the pod it targeted, so nothing holds the local port at all. The zero
	// value is the rarer one — a listener that is still bound.
	forwardDropped bool
	edgeAnswers    bool
	repairs        bool
	reconnects     int
	hold           chan struct{}
}

func (p *forwardRepairProbe) bound() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.forwardDropped
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
	if p.repairs {
		p.forwardDropped = false
		p.edgeAnswers = true
	}
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
			// "fresh" is configured and has no recorded forward: the environment
			// nobody opened, which the sweep must leave entirely alone.
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
			"erun/fresh":  {Name: "fresh", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}
	app := NewApp(erunUIDeps{
		store:               store,
		findProjectRoot:     func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:      func() string { return "/tmp/erun" },
		canConnectLocalPort: func(int) bool { return probe.bound() },
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
		// Only "erun/fresh" (no seeded forward) ever reaches this: every other
		// selection in this file has a forward state file seeded above, so it
		// resolves through the reachable/outage branches this file exists to
		// test and never falls back to a pod probe. Idle-and-not-busy is a
		// neutral default so that fallback answering does not itself look like
		// a failure in tests that do not care about it.
		execRuntimePod: func(context.Context, uiSelection, string) (string, error) {
			data, err := json.Marshal(eruncommon.EnvironmentIdleStatus{
				Markers: []eruncommon.EnvironmentIdleMarker{{Name: eruncommon.ActivityKindProcess, Idle: true}},
			})
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
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
	return sweepEnvUntilRebindSettles(t, app, forwardRepairSelection())
}

func sweepEnvUntilRebindSettles(t *testing.T, app *App, selection uiSelection) environmentActivityState {
	t.Helper()
	state := app.observeEnvironmentActivity(selection).state
	key := selectionKey(selection)
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

// TestDroppedForwardIsRestarted is the common case, and the one that used to be
// silent: a pod replacement takes the forward with it, so the local port is
// free and the environment answers nobody. The sweep that finds it starts a
// replacement, and once that lands the environment is reachable again with the
// episode closed behind it.
func TestDroppedForwardIsRestarted(t *testing.T) {
	probe := &forwardRepairProbe{forwardDropped: true, repairs: true}
	app, emits := forwardRepairTestApp(t, probe, nil)

	state := sweepUntilRebindSettles(t, app)
	if state.reachable {
		t.Fatal("a forward whose port nothing holds is not reachable")
	}
	if state.outage {
		t.Fatal("the first failing sweep restarts the forward; it does not yet diagnose it")
	}
	waitForReconnects(t, probe, 1)

	// The replacement is up. The environment answers again, and no further
	// rebinds are spent on it.
	state = sweepUntilRebindSettles(t, app)
	if !state.reachable || !state.observed {
		t.Fatalf("a restarted forward makes the environment reachable and observed, got %+v", state)
	}
	if got := probe.count(); got != 1 {
		t.Fatalf("restarting one dropped forward took %d rebinds, want 1", got)
	}
	if got := notificationsFromSource(emits, notificationSourceForwardOutage); len(got) != 0 {
		t.Fatalf("a forward that was restarted reported %d outages, want 0", len(got))
	}
}

// TestDroppedForwardIsNotRestartedTwiceByOverlappingSweeps is the idempotency
// half: two sweeps landing while one repair is still running must not put two
// forwards on one local port.
func TestDroppedForwardIsNotRestartedTwiceByOverlappingSweeps(t *testing.T) {
	probe := &forwardRepairProbe{forwardDropped: true, hold: make(chan struct{})}
	app, _ := forwardRepairTestApp(t, probe, nil)

	app.observeEnvironmentActivity(forwardRepairSelection())
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

// TestUnopenedEnvironmentIsProbedButNeverRebound is the guard the dropped case
// makes load-bearing, updated for erun#1572: "no forward is bound" describes
// an environment nobody opened here and an environment whose forward just
// died equally well, and only the second may trigger the rebind/reconnect
// machinery this file exists to test. It no longer describes "reports
// nothing" — an environment nobody opened here can still be busy right now
// (a CLI orchestrator, an agent driving it over MCP from another machine), so
// the sweep asks it directly over its runtime pod instead of leaving it
// silent. What must stay true is that this read-only ask never feeds the
// forward-repair episode: no rebind, no outage, because there was never a
// forward here to repair.
func TestUnopenedEnvironmentIsProbedButNeverRebound(t *testing.T) {
	probe := &forwardRepairProbe{forwardDropped: true, repairs: true}
	app, emits := forwardRepairTestApp(t, probe, nil)
	unopened := uiSelection{Tenant: "erun", Environment: "fresh"}

	for range forwardRepairAttempts + 2 {
		state := sweepEnvUntilRebindSettles(t, app, unopened)
		if state.outage || state.checkFailed {
			t.Fatalf("a successful pod probe is neither an outage nor a failed check, got %+v", state)
		}
		if !state.reachable || !state.observed || state.busy {
			t.Fatalf("an idle pod probe reports the environment idle, got %+v", state)
		}
	}
	if got := probe.count(); got != 0 {
		t.Fatalf("an environment with no recorded forward was rebound %d times, want 0", got)
	}
	if got := notificationsFromSource(emits, notificationSourceForwardOutage); len(got) != 0 {
		t.Fatalf("an environment with no recorded forward reported %d outages, want 0", len(got))
	}
}

// TestStaleForwardIsReestablishedWithoutDuplicatingIt is the rarer shape: the
// forward keeps its local port bound while its far end is gone, so every check
// that stops at the listener calls it healthy. The sweep re-establishes it, and
// a second sweep landing while that repair is still running does not start a
// competing one for the same local port.
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
	if state.outage {
		t.Fatal("the first failing sweep repairs the forward; it does not yet diagnose it as unrepairable")
	}
	waitForReconnects(t, probe, 1)

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
		if state.outage {
			t.Fatal("an environment that answered is not a broken forward")
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
	if state.outage {
		t.Fatal("an edge that replies is not a broken forward")
	}
	if got := probe.count(); got != 0 {
		t.Fatalf("a declined idle query caused %d rebinds, want 0", got)
	}
}

// TestUnrepairableForwardReportsInsteadOfLooping locks the bound, on the shape
// that produces it most often: a dropped forward the repair cannot bring back
// stops being retried and starts being reported — once — and the row renders it
// as an outage rather than as an environment nobody opened. Recovering ends the
// episode and retires the report.
func TestUnrepairableForwardReportsInsteadOfLooping(t *testing.T) {
	probe := &forwardRepairProbe{forwardDropped: true}
	app, emits := forwardRepairTestApp(t, probe, nil)

	for attempt := range forwardRepairAttempts {
		if state := sweepUntilRebindSettles(t, app); state.outage {
			t.Fatalf("sweep %d diagnosed the forward while attempts remained", attempt+1)
		}
	}
	if got := probe.count(); got != forwardRepairAttempts {
		t.Fatalf("rebind count = %d, want the episode's bound of %d", got, forwardRepairAttempts)
	}

	// Attempts are spent. From here the sweep reports and stops spending.
	for range 3 {
		state := sweepUntilRebindSettles(t, app)
		if !state.outage {
			t.Fatal("an exhausted episode must render the environment as unreachable, not as unopened")
		}
		if state.reachable {
			t.Fatal("a dropped forward is not reachable; the outage is what carries the row")
		}
	}
	if got := probe.count(); got != forwardRepairAttempts {
		t.Fatalf("the repair kept looping: %d rebinds, want it bounded at %d", got, forwardRepairAttempts)
	}

	// The message has to name which failure it is: a port nothing holds and a
	// port held by something silent send a reader to different places.
	assertOneOutageNotification(t, emits, "is gone and nothing holds the port")
}

// assertOneOutageNotification checks the one report an episode gets, and that
// it describes the fault it found rather than the family it belongs to.
func assertOneOutageNotification(t *testing.T, emits *capturedEmits, fault string) {
	t.Helper()
	notes := notificationsFromSource(emits, notificationSourceForwardOutage)
	if len(notes) != 1 {
		t.Fatalf("outage notification posted %d times, want exactly 1 per episode", len(notes))
	}
	if notes[0].Kind != "warning" {
		t.Fatalf("notification kind = %q, want warning", notes[0].Kind)
	}
	if !strings.Contains(notes[0].Message, fault) {
		t.Fatalf("notification does not describe the fault %q: %q", fault, notes[0].Message)
	}
}

// TestUnrepairableStaleForwardReportsItsOwnFault is the sibling: the same bound
// and the same single report, described as the fault it actually is.
func TestUnrepairableStaleForwardReportsItsOwnFault(t *testing.T) {
	probe := &forwardRepairProbe{}
	app, emits := forwardRepairTestApp(t, probe, nil)

	for range forwardRepairAttempts + 1 {
		sweepUntilRebindSettles(t, app)
	}
	assertOneOutageNotification(t, emits, "holds the local port but its edge never answers")
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
	if state := sweepUntilRebindSettles(t, app); state.outage {
		t.Fatal("a forward that carries traffic again is not broken")
	}
	if got := len(clearedNotificationSources(emits, notificationSourceForwardOutage)); got != 1 {
		t.Fatalf("recovery cleared the outage notification %d times, want 1", got)
	}

	probe.setAnswers(false)
	if state := sweepUntilRebindSettles(t, app); state.outage {
		t.Fatal("a fresh failure must be repaired before it is reported")
	}
	if got := probe.count(); got != forwardRepairAttempts+1 {
		t.Fatalf("rebind count = %d, want a fresh episode's first attempt", got)
	}
}

// TestStoppedEnvironmentIsNotResurrected is the guard on the repair's blast
// radius, and it applies to the dropped shape most of all: stopping an
// environment frees its local port, so a stop is indistinguishable from the
// failure this repair exists for unless the cluster is asked. A stopped
// environment's forward is *supposed* to be dead, and repairing it would undo
// the operator's own stop and rewrite it as an outage.
func TestStoppedEnvironmentIsNotResurrected(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		dropped bool
	}{
		{name: "dropped_forward", dropped: true},
		{name: "stale_forward"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			probe := &forwardRepairProbe{forwardDropped: testCase.dropped}
			app, emits := forwardRepairTestApp(t, probe,
				func(eruncommon.Context, eruncommon.RuntimeScaleTarget) (eruncommon.RuntimeRunState, error) {
					return eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 0}, nil
				})

			for range forwardRepairAttempts + 2 {
				if state := sweepUntilRebindSettles(t, app); state.outage {
					t.Fatal("a stopped environment is stopped, not an unreachable one")
				}
			}
			if got := probe.count(); got != 0 {
				t.Fatalf("a stopped environment was woken by %d rebinds, want 0", got)
			}
			if got := notificationsFromSource(emits, notificationSourceForwardOutage); len(got) != 0 {
				t.Fatalf("a stopped environment reported %d outages, want 0", len(got))
			}
		})
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
