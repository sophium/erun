package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// The readiness indicator has to tell three conditions apart: running, stopped,
// and unhealthy. A scaled-to-zero environment is the operator's own doing and
// must never render as a failure, while pods that exist but are not ready must
// keep rendering as one. These tests lock that mapping and the Stop action that
// produces the stopped condition.

func stopTestApp(t *testing.T, emits *capturedEmits, deps erunUIDeps) *App {
	t.Helper()
	projectRoot := t.TempDir()
	deps.store = stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "cluster-local"},
		},
	}
	deps.findProjectRoot = func() (string, string, error) { return "erun", projectRoot, nil }
	deps.resolveCLIPath = func() string { return "/tmp/erun" }
	app := NewApp(deps)
	t.Cleanup(func() { app.shutdown(context.Background()) })
	app.SetEmitter(emits.fn())
	return app
}

func TestRuntimeStoppedForSelectionMapsClusterStateToTheIndicator(t *testing.T) {
	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	cases := []struct {
		name  string
		state eruncommon.RuntimeRunState
		err   error
		want  bool
	}{
		{
			name:  "scaled to zero with no pods is stopped",
			state: eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 0, ReadyReplicas: 0},
			want:  true,
		},
		{
			// The termination window the reconnect gate used to lose. The scale
			// has landed and every attached session has already dropped, but the
			// old pod stays Ready for the length of its grace period. Reading that
			// as "running" let the respawn through, and the respawned `erun open`
			// — reading the same Deployment — scaled the environment straight back
			// up about a second after the stop.
			name:  "scaled to zero while the old pod drains is stopped",
			state: eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 0, ReadyReplicas: 1},
			want:  true,
		},
		{
			// The distinction the issue turns on: a pod that exists but is not
			// ready is an unhealthy environment, and softening it into "stopped"
			// would hide a real failure behind a benign-looking indicator.
			name:  "pods wanted but not ready is not stopped",
			state: eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 0},
			want:  false,
		},
		{
			name:  "running is not stopped",
			state: eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1},
			want:  false,
		},
		{
			// An undeployed environment is a deploy problem, not a stopped one;
			// the runtime health check is what reports it.
			name:  "absent deployment is not stopped",
			state: eruncommon.RuntimeRunState{},
			want:  false,
		},
		{
			// An unreadable cluster is a diagnostic problem. Reporting it as
			// stopped would tell the operator to start something that is already
			// running.
			name: "unreadable cluster is not stopped",
			err:  errors.New("connection refused"),
			want: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			emits := newCapturedEmits()
			app := stopTestApp(t, emits, erunUIDeps{
				readRuntimeRunState: func(eruncommon.Context, eruncommon.RuntimeScaleTarget) (eruncommon.RuntimeRunState, error) {
					return testCase.state, testCase.err
				},
			})
			if got := app.runtimeStoppedForSelection(selection); got != testCase.want {
				t.Fatalf("runtimeStoppedForSelection = %v, want %v", got, testCase.want)
			}
		})
	}
}

// The latch closes a race the cluster read cannot: dropping the pod wakes every
// open tab's reconnect loop at once, well before a `kubectl get` could observe
// the new replica count. Until it does, the desktop's own stop must still read
// as stopped.
func TestRuntimeStoppedForSelectionHonoursTheLatchBeforeTheClusterCatchesUp(t *testing.T) {
	emits := newCapturedEmits()
	app := stopTestApp(t, emits, erunUIDeps{
		readRuntimeRunState: func(eruncommon.Context, eruncommon.RuntimeScaleTarget) (eruncommon.RuntimeRunState, error) {
			return eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1}, nil
		},
	})

	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	if app.runtimeStoppedForSelection(selection) {
		t.Fatal("a running env must not read as stopped before anything stopped it")
	}
	app.markRuntimeStopped(selection)
	if !app.runtimeStoppedForSelection(selection) {
		t.Fatal("the latch must win while the cluster still reports the old replica count")
	}
	app.clearRuntimeStopped(selection)
	if app.runtimeStoppedForSelection(selection) {
		t.Fatal("clearing the latch must hand the decision back to the cluster")
	}
}

func TestStopEnvironmentFlagsTheRowStoppedAndTargetsTheRuntimeDeployment(t *testing.T) {
	emits := newCapturedEmits()
	var captured eruncommon.StopEnvironmentParams
	app := stopTestApp(t, emits, erunUIDeps{
		stopEnvironmentRuntime: func(_ eruncommon.Context, params eruncommon.StopEnvironmentParams) (eruncommon.StopEnvironmentResult, error) {
			captured = params
			return eruncommon.StopEnvironmentResult{
				Tenant:      params.Result.Tenant,
				Environment: params.Result.Environment,
				Release:     eruncommon.RuntimeReleaseName(params.Result.Tenant),
				Namespace:   eruncommon.KubernetesNamespaceName(params.Result.Tenant, params.Result.Environment),
				Stopped:     true,
			}, nil
		},
	})

	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	result, err := app.StopEnvironment(selection)
	if err != nil {
		t.Fatalf("StopEnvironment failed: %v", err)
	}
	if result.Release != "erun-devops" || result.Namespace != "erun-remote" {
		t.Fatalf("stop targeted the wrong runtime: %+v", result)
	}
	if captured.Result.Environment != "remote" {
		t.Fatalf("stop resolved the wrong environment: %+v", captured.Result)
	}
	waitForEnvStatus(t, emits, envStatusRuntimeStopped)
	if !app.isRuntimeStopped(selection) {
		t.Fatal("stop did not latch the intent, so an open tab's reconnect would wake the env straight back up")
	}
}

// LoadRuntimeRunState is what the Runtime tab's Stop control reads before it
// decides whether to offer the action at all. The reported defect was a control
// that never asked: pressing Stop on an already-stopped runtime produced a
// correct no-op whose only feedback was a terminal line, so the button read as
// broken. These cases pin the three answers that control has to tell apart —
// stopped, not deployed, and unreadable — because an unreadable cluster folded
// into "not deployed" would state a fact nobody observed.
func TestLoadRuntimeRunStateReportsTheAlreadyStoppedRuntime(t *testing.T) {
	cases := []struct {
		name  string
		state eruncommon.RuntimeRunState
		want  uiRuntimeRunState
	}{
		{
			// The state the report describes.
			name:  "scaled to zero is stopped with nothing to stop",
			state: eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 0, ReadyReplicas: 0},
			want:  uiRuntimeRunState{Present: true, DesiredReplicas: 0, Stopped: true},
		},
		{
			name:  "running carries the replica counts the operator is shown",
			state: eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1},
			want:  uiRuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1},
		},
		{
			// Wants a pod, has none: unhealthy, and still a legitimate thing to
			// stop. It must not read as stopped.
			name:  "running but not ready is not stopped",
			state: eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 1},
			want:  uiRuntimeRunState{Present: true, DesiredReplicas: 1},
		},
		{
			name:  "an absent Deployment is not deployed rather than stopped",
			state: eruncommon.RuntimeRunState{},
			want:  uiRuntimeRunState{},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := loadRunStateReading(t, testCase.state, nil)
			if got.Message != "" {
				t.Fatalf("a readable cluster must not carry a message, got %q", got.Message)
			}
			testCase.want.Tenant, testCase.want.Environment = "erun", "remote"
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("LoadRuntimeRunState = %+v, want %+v", got, testCase.want)
			}
		})
	}
}

// loadRunStateReading drives LoadRuntimeRunState through one stubbed cluster
// read. A read that surfaces as an error rather than a fail-soft reading fails
// the test, because that is what would turn the Runtime tab into a failure
// surface.
func loadRunStateReading(t *testing.T, state eruncommon.RuntimeRunState, readErr error) uiRuntimeRunState {
	t.Helper()
	app := stopTestApp(t, newCapturedEmits(), erunUIDeps{
		readRuntimeRunState: func(eruncommon.Context, eruncommon.RuntimeScaleTarget) (eruncommon.RuntimeRunState, error) {
			return state, readErr
		},
	})
	got, err := app.LoadRuntimeRunState(uiSelection{Tenant: "erun", Environment: "remote"})
	if err != nil {
		t.Fatalf("LoadRuntimeRunState must fail soft, got error %v", err)
	}
	return got
}

// An unreadable cluster is a third answer, and folding it into "not deployed"
// would state a fact nobody observed — with a recovery ("deploy it") that is
// wrong for the environment in front of the operator.
func TestLoadRuntimeRunStateStatesAFailedReadRatherThanGuessing(t *testing.T) {
	got := loadRunStateReading(t, eruncommon.RuntimeRunState{}, errors.New("connection refused"))
	if got.Present || got.Stopped {
		t.Fatalf("an unread cluster must claim neither state, got %+v", got)
	}
	if !strings.HasPrefix(got.Message, "Cannot read this environment's runtime run state: ") {
		t.Fatalf("a failed read must say what could not be read, got %q", got.Message)
	}
}

// A stop this desktop just issued is latched: the pod is already dropping while
// the cluster read still reports the old replica count, so without the latch the
// panel would flash "running" at the operator who just stopped it.
func TestLoadRuntimeRunStateAnswersFromTheStopLatchBeforeTheClusterCatchesUp(t *testing.T) {
	app := stopTestApp(t, newCapturedEmits(), erunUIDeps{
		readRuntimeRunState: func(eruncommon.Context, eruncommon.RuntimeScaleTarget) (eruncommon.RuntimeRunState, error) {
			return eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1}, nil
		},
	})
	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	app.markRuntimeStopped(selection)
	got, err := app.LoadRuntimeRunState(selection)
	if err != nil {
		t.Fatalf("LoadRuntimeRunState failed: %v", err)
	}
	if !got.Stopped {
		t.Fatalf("the latch must report stopped while the cluster still reports the old count: %+v", got)
	}
}

// The notice is the whole visible outcome of a stop for the tabs it ends: they
// go dark a moment later, and without being told they went dark *because of
// this*, the operator reads their own command as the environment breaking.
func TestStopEnvironmentNoticeNamesTheEndedSessionsAndTheWayBack(t *testing.T) {
	notice := stopEnvironmentNotice(eruncommon.StopEnvironmentResult{
		Tenant:        "erun",
		Environment:   "remote",
		EndedSessions: []string{"open-0", "ai"},
	})
	for _, want := range []string{"Stopped erun/remote", "2 attached session(s) ended with the pod", "Open it to start it again"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice = %q, want it to contain %q", notice, want)
		}
	}

	quiet := stopEnvironmentNotice(eruncommon.StopEnvironmentResult{Tenant: "erun", Environment: "remote"})
	if strings.Contains(quiet, "session") {
		t.Fatalf("an environment nobody had open must not mention sessions: %q", quiet)
	}
	if strings.Contains(quiet, "component") {
		t.Fatalf("a runtime-only environment must not gain a component sentence: %q", quiet)
	}
}

// A stop scales one Deployment. The platform components rolled out alongside it
// keep running and keep holding their capacity, so the outcome has to name
// them: they are the pods still standing afterwards, and on an already-stopped
// environment they are the entire explanation for why nothing changed.
func TestStopEnvironmentNoticeNamesTheComponentsItLeftRunning(t *testing.T) {
	result := eruncommon.StopEnvironmentResult{
		Tenant:              "erun",
		Environment:         "local",
		RemainingComponents: []string{"erun-backend-api", "erun-backend-postgres"},
	}
	notice := stopEnvironmentNotice(result)
	for _, want := range []string{
		"keep running and keep holding capacity",
		"erun-backend-api, erun-backend-postgres",
	} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice = %q, want it to contain %q", notice, want)
		}
	}
	if !strings.Contains(notice, "returned its runtime's capacity") {
		t.Fatalf("the notice must not claim the whole environment's capacity came back: %q", notice)
	}

	// The already-stopped outcome changed nothing at all, so it needs the
	// explanation most.
	alreadyStopped := stopEnvironmentNotice(eruncommon.StopEnvironmentResult{
		Tenant:              "erun",
		Environment:         "local",
		AlreadyStopped:      true,
		RemainingComponents: []string{"erun-backend-api"},
	})
	if !strings.Contains(alreadyStopped, "erun-backend-api") {
		t.Fatalf("the no-op outcome must still say what is left running: %q", alreadyStopped)
	}
}

// A failed stop must leave nothing behind: the latch would otherwise suppress
// reconnect for an environment that is still running.
func TestStopEnvironmentClearsTheLatchWhenTheStopFails(t *testing.T) {
	emits := newCapturedEmits()
	app := stopTestApp(t, emits, erunUIDeps{
		stopEnvironmentRuntime: func(eruncommon.Context, eruncommon.StopEnvironmentParams) (eruncommon.StopEnvironmentResult, error) {
			return eruncommon.StopEnvironmentResult{}, errors.New("forbidden")
		},
	})

	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	if _, err := app.StopEnvironment(selection); err == nil {
		t.Fatal("expected StopEnvironment to surface the failure")
	}
	if app.isRuntimeStopped(selection) {
		t.Fatal("a failed stop must not leave the reconnect latch set")
	}
	if got := envStatuses(emits); len(got) != 0 {
		t.Fatalf("a failed stop must not flag the row stopped, got %+v", got)
	}
}

// A stopped runtime must refuse the automatic respawn and flag the row stopped
// rather than failed: `erun open` now wakes a stopped env, so respawning would
// undo the operator's own stop, and the deploy-failure guards further down would
// otherwise claim the env is broken.
func TestReconnectRefusedFlagsStoppedRuntimeInsteadOfFailed(t *testing.T) {
	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	emits := newCapturedEmits()
	app := stopTestApp(t, emits, erunUIDeps{
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			sessionsMu.Lock()
			sessions = append(sessions, session)
			sessionsMu.Unlock()
			return session, nil
		},
		readRuntimeRunState: func(eruncommon.Context, eruncommon.RuntimeScaleTarget) (eruncommon.RuntimeRunState, error) {
			return eruncommon.RuntimeRunState{Present: true, DesiredReplicas: 0}, nil
		},
	})

	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	if _, err := app.StartSession(selection, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	waitForEnvStatus(t, emits, "")

	sessionsMu.Lock()
	current := sessions[0]
	sessionsMu.Unlock()
	_ = current.Close()

	waitForEnvStatus(t, emits, envStatusRuntimeStopped)
	for _, payload := range envStatuses(emits) {
		if payload.Status == envStatusFailed {
			t.Fatalf("a stopped runtime must not read as a failure: %+v", envStatuses(emits))
		}
	}
	sessionsMu.Lock()
	got := len(sessions)
	sessionsMu.Unlock()
	if got != 1 {
		t.Fatalf("expected no respawn against a stopped runtime, got %d sessions", got)
	}
}
