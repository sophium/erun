package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/lifecycle"
)

// Scaled-to-zero is stopped, never a failure; pods wanted but not ready stay unhealthy.

func stopTestApp(t *testing.T, emits *capturedEmits, deps erunUIDeps) *App {
	t.Helper()
	projectRoot := t.TempDir()
	deps.store = stubUIStore{
		tenants: map[string]config.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
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
		state lifecycle.RuntimeRunState
		err   error
		want  bool
	}{
		{
			name:  "scaled to zero with no pods is stopped",
			state: lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 0, ReadyReplicas: 0},
			want:  true,
		},
		{
			// The old pod stays Ready through its grace period after the scale lands.
			name:  "scaled to zero while the old pod drains is stopped",
			state: lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 0, ReadyReplicas: 1},
			want:  true,
		},
		{
			name:  "pods wanted but not ready is not stopped",
			state: lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 0},
			want:  false,
		},
		{
			name:  "running is not stopped",
			state: lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1},
			want:  false,
		},
		{
			name:  "absent deployment is not stopped",
			state: lifecycle.RuntimeRunState{},
			want:  false,
		},
		{
			name: "unreadable cluster is not stopped",
			err:  errors.New("connection refused"),
			want: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			emits := newCapturedEmits()
			app := stopTestApp(t, emits, erunUIDeps{
				readRuntimeRunState: func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
					return testCase.state, testCase.err
				},
			})
			if got := app.runtimeStoppedForSelection(selection); got != testCase.want {
				t.Fatalf("runtimeStoppedForSelection = %v, want %v", got, testCase.want)
			}
		})
	}
}

// The latch covers the window before kubectl sees the new replica count.
func TestRuntimeStoppedForSelectionHonoursTheLatchBeforeTheClusterCatchesUp(t *testing.T) {
	emits := newCapturedEmits()
	app := stopTestApp(t, emits, erunUIDeps{
		readRuntimeRunState: func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
			return lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1}, nil
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
	var captured lifecycle.StopEnvironmentParams
	app := stopTestApp(t, emits, erunUIDeps{
		stopEnvironmentRuntime: func(_ core.Context, params lifecycle.StopEnvironmentParams) (lifecycle.StopEnvironmentResult, error) {
			captured = params
			return lifecycle.StopEnvironmentResult{
				Tenant:      params.Result.Tenant,
				Environment: params.Result.Environment,
				Release:     config.RuntimeReleaseName(params.Result.Tenant),
				Namespace:   config.KubernetesNamespaceName(params.Result.Tenant, params.Result.Environment),
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

func TestLoadRuntimeRunStateReportsTheAlreadyStoppedRuntime(t *testing.T) {
	cases := []struct {
		name  string
		state lifecycle.RuntimeRunState
		want  uiRuntimeRunState
	}{
		{
			name:  "scaled to zero is stopped with nothing to stop",
			state: lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 0, ReadyReplicas: 0},
			want:  uiRuntimeRunState{Present: true, DesiredReplicas: 0, Stopped: true},
		},
		{
			name:  "running carries the replica counts the operator is shown",
			state: lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1},
			want:  uiRuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1},
		},
		{
			name:  "running but not ready is not stopped",
			state: lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 1},
			want:  uiRuntimeRunState{Present: true, DesiredReplicas: 1},
		},
		{
			name:  "an absent Deployment is not deployed rather than stopped",
			state: lifecycle.RuntimeRunState{},
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

// loadRunStateReading fails the test on an error: the reading must be fail-soft.
func loadRunStateReading(t *testing.T, state lifecycle.RuntimeRunState, readErr error) uiRuntimeRunState {
	t.Helper()
	app := stopTestApp(t, newCapturedEmits(), erunUIDeps{
		readRuntimeRunState: func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
			return state, readErr
		},
	})
	got, err := app.LoadRuntimeRunState(uiSelection{Tenant: "erun", Environment: "remote"})
	if err != nil {
		t.Fatalf("LoadRuntimeRunState must fail soft, got error %v", err)
	}
	return got
}

func TestLoadRuntimeRunStateStatesAFailedReadRatherThanGuessing(t *testing.T) {
	got := loadRunStateReading(t, lifecycle.RuntimeRunState{}, errors.New("connection refused"))
	if got.Present || got.Stopped {
		t.Fatalf("an unread cluster must claim neither state, got %+v", got)
	}
	if !strings.HasPrefix(got.Message, "Cannot read this environment's runtime run state: ") {
		t.Fatalf("a failed read must say what could not be read, got %q", got.Message)
	}
}

func TestLoadRuntimeRunStateAnswersFromTheStopLatchBeforeTheClusterCatchesUp(t *testing.T) {
	app := stopTestApp(t, newCapturedEmits(), erunUIDeps{
		readRuntimeRunState: func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
			return lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1}, nil
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

func TestStopEnvironmentNoticeNamesTheEndedSessionsAndTheWayBack(t *testing.T) {
	notice := stopEnvironmentNotice(lifecycle.StopEnvironmentResult{
		Tenant:        "erun",
		Environment:   "remote",
		EndedSessions: []string{"open-0", "ai"},
	})
	for _, want := range []string{"Stopped erun/remote", "2 attached session(s) ended with the pod", "Open it to start it again"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice = %q, want it to contain %q", notice, want)
		}
	}

	quiet := stopEnvironmentNotice(lifecycle.StopEnvironmentResult{Tenant: "erun", Environment: "remote"})
	if strings.Contains(quiet, "session") {
		t.Fatalf("an environment nobody had open must not mention sessions: %q", quiet)
	}
	if strings.Contains(quiet, "component") {
		t.Fatalf("a runtime-only environment must not gain a component sentence: %q", quiet)
	}
}

func TestStopEnvironmentNoticeNamesTheComponentsItLeftRunning(t *testing.T) {
	result := lifecycle.StopEnvironmentResult{
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

	alreadyStopped := stopEnvironmentNotice(lifecycle.StopEnvironmentResult{
		Tenant:              "erun",
		Environment:         "local",
		AlreadyStopped:      true,
		RemainingComponents: []string{"erun-backend-api"},
	})
	if !strings.Contains(alreadyStopped, "erun-backend-api") {
		t.Fatalf("the no-op outcome must still say what is left running: %q", alreadyStopped)
	}
}

func TestStopEnvironmentClearsTheLatchWhenTheStopFails(t *testing.T) {
	emits := newCapturedEmits()
	app := stopTestApp(t, emits, erunUIDeps{
		stopEnvironmentRuntime: func(core.Context, lifecycle.StopEnvironmentParams) (lifecycle.StopEnvironmentResult, error) {
			return lifecycle.StopEnvironmentResult{}, errors.New("forbidden")
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

// `erun open` wakes a stopped env, so a respawn would undo the operator's stop.
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
		readRuntimeRunState: func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
			return lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 0}, nil
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
