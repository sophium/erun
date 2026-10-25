package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

const forwardRepairTestPort = 17500

// hold keeps a rebind in flight until released; repairs makes a rebind succeed.
type forwardRepairProbe struct {
	mu             sync.Mutex
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
	readRunState func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error),
) (*App, *capturedEmits) {
	t.Helper()
	seedMCPForward(t, "erun", "remote", forwardRepairTestPort)
	emits := newCapturedEmits()
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			// "fresh" has no recorded forward: never opened here.
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
		loadIdleStatus: func(context.Context, string, string) (model.EnvironmentIdleStatus, error) {
			if !probe.answers() {
				return model.EnvironmentIdleStatus{}, errors.New("edge did not answer")
			}
			return model.EnvironmentIdleStatus{
				Markers: []model.EnvironmentIdleMarker{{Name: activity.ActivityKindProcess, Idle: true}},
			}, nil
		},
		reconnectMCP: func(context.Context, config.OpenResult, func(string)) error {
			return probe.reconnect()
		},
		readRuntimeRunState: readRunState,
		// Only "erun/fresh" reaches the pod fallback; answer idle as a neutral default.
		execRuntimePod: func(context.Context, uimodel.Selection, string) (string, error) {
			data, err := json.Marshal(model.EnvironmentIdleStatus{
				Markers: []model.EnvironmentIdleMarker{{Name: activity.ActivityKindProcess, Idle: true}},
			})
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	})
	// A non-nil ctx un-gates the rebind; stub the emitter so emits avoid the real
	// Wails runtime.
	app.ctx = context.Background()
	app.SetEmitter(emits.fn())
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app, emits
}

func forwardRepairSelection() uimodel.Selection {
	return uimodel.Selection{Tenant: "erun", Environment: "remote"}
}

// sweepUntilRebindSettles waits for the started rebind so the in-flight latch
// does not suppress the next sweep.
func sweepUntilRebindSettles(t *testing.T, app *App) environmentActivityState {
	t.Helper()
	return sweepEnvUntilRebindSettles(t, app, forwardRepairSelection())
}

func sweepEnvUntilRebindSettles(t *testing.T, app *App, selection uimodel.Selection) environmentActivityState {
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

func TestDroppedForwardIsNotRestartedTwiceByOverlappingSweeps(t *testing.T) {
	probe := &forwardRepairProbe{forwardDropped: true, hold: make(chan struct{})}
	app, _ := forwardRepairTestApp(t, probe, nil)

	app.observeEnvironmentActivity(forwardRepairSelection())
	waitForReconnects(t, probe, 1)

	app.observeEnvironmentActivity(forwardRepairSelection())
	close(probe.hold)
	waitForReconnects(t, probe, 1)
	if got := probe.count(); got != 1 {
		t.Fatalf("overlapping sweeps started %d rebinds, want 1", got)
	}
}

// An unopened environment may be asked over its pod but never feeds a repair.
func TestUnopenedEnvironmentIsProbedButNeverRebound(t *testing.T) {
	probe := &forwardRepairProbe{forwardDropped: true, repairs: true}
	app, emits := forwardRepairTestApp(t, probe, nil)
	unopened := uimodel.Selection{Tenant: "erun", Environment: "fresh"}

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

	// The tunnel carries traffic but the idle query fails.
	app.deps.loadIdleStatus = func(context.Context, string, string) (model.EnvironmentIdleStatus, error) {
		return model.EnvironmentIdleStatus{}, errors.New("401 unauthorized")
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

	assertOneOutageNotification(t, emits, "is gone and nothing holds the port")
}

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

func TestUnrepairableStaleForwardReportsItsOwnFault(t *testing.T) {
	probe := &forwardRepairProbe{}
	app, emits := forwardRepairTestApp(t, probe, nil)

	for range forwardRepairAttempts + 1 {
		sweepUntilRebindSettles(t, app)
	}
	assertOneOutageNotification(t, emits, "holds the local port but its edge never answers")
}

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

// A stop frees the local port exactly like the failure, so only the cluster
// can tell them apart.
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
				func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
					return lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 0}, nil
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

func notificationsFromSource(emits *capturedEmits, source string) []uimodel.AppNotificationPayload {
	var out []uimodel.AppNotificationPayload
	for _, event := range emits.events(uimodel.AppNotificationEvent) {
		note, ok := event.(uimodel.AppNotificationPayload)
		if ok && note.Source == source {
			out = append(out, note)
		}
	}
	return out
}

func clearedNotificationSources(emits *capturedEmits, source string) []uimodel.AppNotificationClearPayload {
	var out []uimodel.AppNotificationClearPayload
	for _, event := range emits.events(uimodel.AppNotificationClearEvent) {
		cleared, ok := event.(uimodel.AppNotificationClearPayload)
		if ok && cleared.Source == source {
			out = append(out, cleared)
		}
	}
	return out
}
