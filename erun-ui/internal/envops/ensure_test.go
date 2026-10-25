package envops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle/stop"
	"github.com/sophium/erun/erun-common/primitive/deploy/kubecheck"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// deploymentAbsentErrorForTest builds ensureRuntimeDeployed's exact "is not deployed" error.
func deploymentAbsentErrorForTest(tenant, environment string) error {
	return fmt.Errorf("runtime for %s/%s %s %q not found in namespace %q); run `erun deploy %s %s` first",
		tenant, environment, kubecheck.KubernetesDeploymentAbsentMessageMarker, tenant+"-devops", tenant+"-"+environment, tenant, environment)
}

func ensureTestStore(t *testing.T) fakeStore {
	t.Helper()
	return fakeStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: t.TempDir(), KubernetesContext: "ctx"},
		},
	}
}

// ensureCounter counts reconnects; err is what the next reconnect returns.
type ensureCounter struct {
	mu    sync.Mutex
	count int
	err   error
}

func (c *ensureCounter) reconnect(context.Context, config.OpenResult, func(string)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	return c.err
}

func (c *ensureCounter) setErr(err error) {
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
}

func (c *ensureCounter) waitFor(t *testing.T, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := c.count
		c.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.mu.Lock()
	got := c.count
	c.mu.Unlock()
	t.Fatalf("ensure count = %d, want %d", got, want)
}

// ensureTestService has started, so the ensure runs; a nil readRunState reads
// as "not stopped".
func ensureTestService(
	t *testing.T,
	reconnect func(context.Context, config.OpenResult, func(string)) error,
	readRunState func(core.Context, stop.RuntimeScaleTarget) (stop.RuntimeRunState, error),
	deployInFlight func(uimodel.Selection) bool,
) (*Service, *capturedEmits) {
	t.Helper()
	emits := newCapturedEmits()
	return newTestService(Deps{
		Store:               ensureTestStore(t),
		Started:             func() bool { return true },
		Emit:                emits.fn(),
		ReconnectMCP:        reconnect,
		ReadRuntimeRunState: readRunState,
		DeployInFlight:      deployInFlight,
	}), emits
}

func reconnectSucceeds(context.Context, config.OpenResult, func(string)) error { return nil }

func TestEnsureRuntimeOnceRunsAgainAfterTheWindow(t *testing.T) {
	counter := &ensureCounter{}
	service, _ := ensureTestService(t, counter.reconnect, nil, nil)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	service.EnsureRuntimeOnce(selection)
	counter.waitFor(t, 1, 2*time.Second)

	// Age the window out by hand instead of sleeping out the TTL.
	key := uimodel.SelectionKey(selection)
	service.ensureMu.Lock()
	service.ensureDone[key] = time.Now().Add(-ensureTTL)
	service.ensureMu.Unlock()

	service.EnsureRuntimeOnce(selection)
	counter.waitFor(t, 2, 2*time.Second)
}

func TestEnsureFailureNotificationDedupesPerEpisode(t *testing.T) {
	counter := &ensureCounter{err: errors.New("timed out waiting for API port-forward")}
	service, emits := ensureTestService(t, counter.reconnect, nil, nil)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	key := uimodel.SelectionKey(selection)

	service.surfaceEnsureFailure(selection, errors.New("boom"))
	service.surfaceEnsureFailure(selection, errors.New("boom"))
	if got := len(emits.events(uimodel.AppNotificationEvent)); got != 1 {
		t.Fatalf("notification emitted %d times in one failure episode, want 1", got)
	}
	if got := len(emits.events(uimodel.EnvStatusEvent)); got != 2 {
		t.Fatalf("env-status emitted %d times, want 2 (flagged on each attempt)", got)
	}

	counter.setErr(nil)
	service.EnsureRuntimeOnce(selection)
	counter.waitFor(t, 1, 2*time.Second)
	waitForFailEpisodeCleared(t, service, key, 2*time.Second)

	service.surfaceEnsureFailure(selection, errors.New("boom again"))
	if got := len(emits.events(uimodel.AppNotificationEvent)); got != 2 {
		t.Fatalf("notification emitted %d times after recovery, want 2", got)
	}
}

func waitForFailEpisodeCleared(t *testing.T, service *Service, key string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		service.ensureMu.Lock()
		_, still := service.ensureFailNotified[key]
		service.ensureMu.Unlock()
		if !still {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("successful ensure did not clear the failure episode")
}

func TestSurfaceEnsureFailureSuppressedWhileDeployInFlight(t *testing.T) {
	var deployMu sync.Mutex
	deploying := true
	service, emits := ensureTestService(t, reconnectSucceeds, nil, func(uimodel.Selection) bool {
		deployMu.Lock()
		defer deployMu.Unlock()
		return deploying
	})
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	absentErr := deploymentAbsentErrorForTest("erun", "remote")

	service.surfaceEnsureFailure(selection, absentErr)

	if got := len(emits.events(uimodel.AppNotificationEvent)); got != 0 {
		t.Fatalf("banner emitted %d times during an in-flight deploy, want 0", got)
	}
	if got := len(emits.events(uimodel.EnvStatusEvent)); got != 0 {
		t.Fatalf("failed status emitted %d times during an in-flight deploy, want 0", got)
	}

	deployMu.Lock()
	deploying = false
	deployMu.Unlock()
	service.surfaceEnsureFailure(selection, absentErr)
	notes := notifications(emits)
	if len(notes) != 1 {
		t.Fatalf("banner emitted %d times once the deploy cleared, want 1", len(notes))
	}
	note := notes[0]
	if note.Kind != "warning" || note.Source != SourceRuntimeUnreachable {
		t.Fatalf("notification = %+v, want kind=warning source=%q", note, SourceRuntimeUnreachable)
	}
	if note.Action != uimodel.NotificationActionDeploy {
		t.Fatalf("notification action = %q, want %q", note.Action, uimodel.NotificationActionDeploy)
	}
}

// `open --reconnect` refuses to start a stopped runtime; that error is the operator's own Stop.
func TestSurfaceEnsureFailureRendersAStoppedRuntimeAsStopped(t *testing.T) {
	service, emits := ensureTestService(t, reconnectSucceeds, func(core.Context, stop.RuntimeScaleTarget) (stop.RuntimeRunState, error) {
		return stop.RuntimeRunState{Present: true, DesiredReplicas: 0, ReadyReplicas: 1}, nil
	}, nil)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	service.surfaceEnsureFailure(selection, errors.New("erun/remote is stopped, and a session reconnect does not start it"))

	statuses := envStatuses(emits)
	if len(statuses) != 1 || statuses[0].Status != uimodel.EnvStatusRuntimeStopped {
		t.Fatalf("env status = %+v, want a single %q", statuses, uimodel.EnvStatusRuntimeStopped)
	}
	notes := notifications(emits)
	if len(notes) != 1 {
		t.Fatalf("notification emitted %d times, want 1", len(notes))
	}
	if !strings.Contains(notes[0].Message, "Open it to start it again") {
		t.Fatalf("notification must name the way back, got %q", notes[0].Message)
	}
	// Opening is the remedy and the row already offers it, so no action button.
	if notes[0].Action != "" {
		t.Fatalf("notification action = %q, want none", notes[0].Action)
	}
}

func TestEnsureSuccessClearsRuntimeUnreachableNotification(t *testing.T) {
	service, emits := ensureTestService(t, reconnectSucceeds, nil, nil)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	service.EnsureRuntimeOnce(selection)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(emits.events(uimodel.AppNotificationClearEvent)) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	events := emits.events(uimodel.AppNotificationClearEvent)
	if len(events) == 0 {
		t.Fatal("a successful ensure did not clear the runtime-unreachable banner")
	}
	payload, ok := events[0].(uimodel.AppNotificationClearPayload)
	if !ok {
		t.Fatalf("clear payload has unexpected type %T", events[0])
	}
	if payload.Tenant != "erun" || payload.Environment != "remote" || payload.Source != "" {
		t.Fatalf("clear payload = %+v, want erun/remote with empty (any) source", payload)
	}
}

func TestEnsureRuntimeNeverRunsBeforeStartup(t *testing.T) {
	counter := &ensureCounter{}
	service := newTestService(Deps{Store: ensureTestStore(t), ReconnectMCP: counter.reconnect})
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	service.EnsureRuntimeOnce(selection)
	if service.EnsureRuntimeWithin(selection, EnsureForBrokenForward, time.Second) {
		t.Fatal("a rebind before startup must not report the runtime reached")
	}
	if service.EnsureInFlight(selection) {
		t.Fatal("no rebind may start before startup, or a unit test reaches the real CLI")
	}
	counter.waitFor(t, 0, 50*time.Millisecond)
}

func TestSurfaceEnsureFailureOffersDeployForGenuinelyAbsentDeployment(t *testing.T) {
	service, emits := ensureTestService(t, reconnectSucceeds, nil, nil)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	service.surfaceEnsureFailure(selection, deploymentAbsentErrorForTest("erun", "remote"))

	notes := notifications(emits)
	if len(notes) != 1 {
		t.Fatalf("notification emitted %d times, want 1", len(notes))
	}
	if notes[0].Action != uimodel.NotificationActionDeploy {
		t.Fatalf("notification action = %q, want %q for a genuinely absent deployment", notes[0].Action, uimodel.NotificationActionDeploy)
	}
	if !strings.Contains(notes[0].Message, "Deploy the environment to bring it up") {
		t.Fatalf("notification must name deploy as the fix, got %q", notes[0].Message)
	}
}

// The kube context was unavailable; a Deploy button would roll a healthy runtime.
func TestSurfaceEnsureFailureOffersNoDeployForACheckThatCouldNotAsk(t *testing.T) {
	service, emits := ensureTestService(t, reconnectSucceeds, nil, nil)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	checkErr := errors.New(`could not determine whether deployment "frs-devops" is deployed (the configured kubernetes context was not found in kubeconfig): error: context "frs-prod" does not exist`)

	service.surfaceEnsureFailure(selection, checkErr)

	notes := notifications(emits)
	if len(notes) != 1 {
		t.Fatalf("notification emitted %d times, want 1", len(notes))
	}
	if notes[0].Action != "" {
		t.Fatalf("notification action = %q, want none — a could-not-ask failure must not offer deploy", notes[0].Action)
	}
	if strings.Contains(notes[0].Message, "Deploy the environment") {
		t.Fatalf("notification must not suggest deploy for a check that never confirmed absence, got %q", notes[0].Message)
	}
	if !strings.Contains(notes[0].Message, "context") {
		t.Fatalf("notification must carry the underlying cause, got %q", notes[0].Message)
	}
}

func TestSurfaceEnsureFailureOffersNoDeployForAnUnclassifiedFailure(t *testing.T) {
	service, emits := ensureTestService(t, reconnectSucceeds, nil, nil)
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	service.surfaceEnsureFailure(selection, errors.New("timed out waiting for API port-forward"))

	notes := notifications(emits)
	if len(notes) != 1 {
		t.Fatalf("notification emitted %d times, want 1", len(notes))
	}
	if notes[0].Action != "" {
		t.Fatalf("notification action = %q, want none for an unrecognized failure", notes[0].Action)
	}
}
