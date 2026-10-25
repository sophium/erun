package envops

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Scaled-to-zero is stopped, never a failure; pods wanted but not ready stay unhealthy.

func stopTestService(t *testing.T, emits *capturedEmits, deps Deps) *Service {
	t.Helper()
	deps.Store = fakeStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: t.TempDir(), KubernetesContext: "cluster-local"},
		},
	}
	deps.Emit = emits.fn()
	return newTestService(deps)
}

func TestRuntimeStoppedMapsClusterStateToTheIndicator(t *testing.T) {
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
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
			service := stopTestService(t, emits, Deps{
				ReadRuntimeRunState: func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
					return testCase.state, testCase.err
				},
			})
			if got := service.RuntimeStopped(selection); got != testCase.want {
				t.Fatalf("RuntimeStopped = %v, want %v", got, testCase.want)
			}
		})
	}
}

// The latch covers the window before kubectl sees the new replica count.
func TestRuntimeStoppedHonoursTheLatchBeforeTheClusterCatchesUp(t *testing.T) {
	emits := newCapturedEmits()
	service := stopTestService(t, emits, Deps{
		ReadRuntimeRunState: func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
			return lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1}, nil
		},
	})

	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	if service.RuntimeStopped(selection) {
		t.Fatal("a running env must not read as stopped before anything stopped it")
	}
	service.markRuntimeStopped(selection)
	if !service.RuntimeStopped(selection) {
		t.Fatal("the latch must win while the cluster still reports the old replica count")
	}
	service.ClearRuntimeStopped(selection)
	if service.RuntimeStopped(selection) {
		t.Fatal("clearing the latch must hand the decision back to the cluster")
	}
}

func TestStopEnvironmentFlagsTheRowStoppedAndTargetsTheRuntimeDeployment(t *testing.T) {
	emits := newCapturedEmits()
	var captured lifecycle.StopEnvironmentParams
	service := stopTestService(t, emits, Deps{
		StopRuntime: func(_ core.Context, params lifecycle.StopEnvironmentParams) (model.StopEnvironmentResult, error) {
			captured = params
			return model.StopEnvironmentResult{
				Tenant:      params.Result.Tenant,
				Environment: params.Result.Environment,
				Release:     config.RuntimeReleaseName(params.Result.Tenant),
				Namespace:   config.KubernetesNamespaceName(params.Result.Tenant, params.Result.Environment),
				Stopped:     true,
			}, nil
		},
	})

	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	result, err := service.StopEnvironment(selection)
	if err != nil {
		t.Fatalf("StopEnvironment failed: %v", err)
	}
	if result.Release != "erun-devops" || result.Namespace != "erun-remote" {
		t.Fatalf("stop targeted the wrong runtime: %+v", result)
	}
	if captured.Result.Environment != "remote" {
		t.Fatalf("stop resolved the wrong environment: %+v", captured.Result)
	}
	if got := envStatuses(emits); len(got) != 1 || got[0].Status != uimodel.EnvStatusRuntimeStopped {
		t.Fatalf("env status = %+v, want a single %q", got, uimodel.EnvStatusRuntimeStopped)
	}
	if !service.isRuntimeStopped(selection) {
		t.Fatal("stop did not latch the intent, so an open tab's reconnect would wake the env straight back up")
	}
}

func TestLoadRuntimeRunStateReportsTheAlreadyStoppedRuntime(t *testing.T) {
	cases := []struct {
		name  string
		state lifecycle.RuntimeRunState
		want  RuntimeRunState
	}{
		{
			name:  "scaled to zero is stopped with nothing to stop",
			state: lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 0, ReadyReplicas: 0},
			want:  RuntimeRunState{Present: true, DesiredReplicas: 0, Stopped: true},
		},
		{
			name:  "running carries the replica counts the operator is shown",
			state: lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1},
			want:  RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1},
		},
		{
			name:  "running but not ready is not stopped",
			state: lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 1},
			want:  RuntimeRunState{Present: true, DesiredReplicas: 1},
		},
		{
			name:  "an absent Deployment is not deployed rather than stopped",
			state: lifecycle.RuntimeRunState{},
			want:  RuntimeRunState{},
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
func loadRunStateReading(t *testing.T, state lifecycle.RuntimeRunState, readErr error) RuntimeRunState {
	t.Helper()
	service := stopTestService(t, newCapturedEmits(), Deps{
		ReadRuntimeRunState: func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
			return state, readErr
		},
	})
	got, err := service.LoadRuntimeRunState(uimodel.Selection{Tenant: "erun", Environment: "remote"})
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
	service := stopTestService(t, newCapturedEmits(), Deps{
		ReadRuntimeRunState: func(core.Context, lifecycle.RuntimeScaleTarget) (lifecycle.RuntimeRunState, error) {
			return lifecycle.RuntimeRunState{Present: true, DesiredReplicas: 1, ReadyReplicas: 1}, nil
		},
	})
	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	service.markRuntimeStopped(selection)
	got, err := service.LoadRuntimeRunState(selection)
	if err != nil {
		t.Fatalf("LoadRuntimeRunState failed: %v", err)
	}
	if !got.Stopped {
		t.Fatalf("the latch must report stopped while the cluster still reports the old count: %+v", got)
	}
}

func TestStopEnvironmentNoticeNamesTheEndedSessionsAndTheWayBack(t *testing.T) {
	notice := stopEnvironmentNotice(model.StopEnvironmentResult{
		Tenant:        "erun",
		Environment:   "remote",
		EndedSessions: []string{"open-0", "ai"},
	})
	for _, want := range []string{"Stopped erun/remote", "2 attached session(s) ended with the pod", "Open it to start it again"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice = %q, want it to contain %q", notice, want)
		}
	}

	quiet := stopEnvironmentNotice(model.StopEnvironmentResult{Tenant: "erun", Environment: "remote"})
	if strings.Contains(quiet, "session") {
		t.Fatalf("an environment nobody had open must not mention sessions: %q", quiet)
	}
	if strings.Contains(quiet, "component") {
		t.Fatalf("a runtime-only environment must not gain a component sentence: %q", quiet)
	}
}

func TestStopEnvironmentNoticeNamesTheComponentsItLeftRunning(t *testing.T) {
	result := model.StopEnvironmentResult{
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

	alreadyStopped := stopEnvironmentNotice(model.StopEnvironmentResult{
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
	service := stopTestService(t, emits, Deps{
		StopRuntime: func(core.Context, lifecycle.StopEnvironmentParams) (model.StopEnvironmentResult, error) {
			return model.StopEnvironmentResult{}, errors.New("forbidden")
		},
	})

	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	if _, err := service.StopEnvironment(selection); err == nil {
		t.Fatal("expected StopEnvironment to surface the failure")
	}
	if service.isRuntimeStopped(selection) {
		t.Fatal("a failed stop must not leave the reconnect latch set")
	}
	if got := envStatuses(emits); len(got) != 0 {
		t.Fatalf("a failed stop must not flag the row stopped, got %+v", got)
	}
}
