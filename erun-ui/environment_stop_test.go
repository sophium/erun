package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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
	waitForEnvStatus(t, emits, envStatusRuntimeStopped, 2*time.Second)
	if !app.isRuntimeStopped(selection) {
		t.Fatal("stop did not latch the intent, so an open tab's reconnect would wake the env straight back up")
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
	waitForEnvStatus(t, emits, "", 2*time.Second)

	sessionsMu.Lock()
	current := sessions[0]
	sessionsMu.Unlock()
	_ = current.Close()

	waitForEnvStatus(t, emits, envStatusRuntimeStopped, 2*time.Second)
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
