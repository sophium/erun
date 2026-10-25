package runtimeview

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// "Answered with no work" and "nobody got an answer" both leave busy false, so
// observed must carry the distinction.

func seedMCPForward(t *testing.T, tenant, environment string, port int) {
	t.Helper()
	// Redirect both roots os.UserConfigDir consults.
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	// adrg/xdg caches ConfigHome at init, so Reload is needed.
	xdg.Reload()
	t.Cleanup(xdg.Reload)
	if err := config.SaveEnvConfig(tenant, config.EnvConfig{Name: environment}); err != nil {
		t.Fatalf("SaveEnvConfig: %v", err)
	}
	path, err := kube.PortForwardStatePath("mcp", tenant, environment)
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.Marshal(model.PortForwardState{
		Tenant: tenant, Environment: environment, LocalPort: port,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func observeWith(
	t *testing.T,
	canConnect bool,
	load func(context.Context, string, string) (model.EnvironmentIdleStatus, error),
) ActivityState {
	t.Helper()
	seedMCPForward(t, "acme", "dev", 17500)
	service := newTestService(Deps{
		CanConnectLocalPort: func(int) bool { return canConnect },
		LoadIdleStatus:      load,
	})
	return service.ObserveActivity(uimodel.Selection{Tenant: "acme", Environment: "dev"})
}

func TestObserveEnvironmentActivityWedgedEdgeGivesNoVerdict(t *testing.T) {
	got := observeWith(t, true, func(context.Context, string, string) (model.EnvironmentIdleStatus, error) {
		return model.EnvironmentIdleStatus{}, errors.New("edge did not answer")
	})
	if !got.Reachable {
		t.Fatal("a port that accepts connections is still reachable")
	}
	if got.Observed {
		t.Fatal("a wedged edge must not be reported as an answered idle question")
	}
	if got.Busy {
		t.Fatal("no answer is not evidence of work either")
	}
}

func TestObserveEnvironmentActivityIdleEnvironmentAnswers(t *testing.T) {
	got := observeWith(t, true, func(context.Context, string, string) (model.EnvironmentIdleStatus, error) {
		return model.EnvironmentIdleStatus{
			Markers: []model.EnvironmentIdleMarker{{Name: activity.ActivityKindProcess, Idle: true}},
		}, nil
	})
	if !got.Reachable || !got.Observed {
		t.Fatalf("an environment that answered is reachable and observed, got %+v", got)
	}
	if got.Busy {
		t.Fatal("every marker idle means no work")
	}
}

func TestObserveEnvironmentActivityUnreachableGivesNoVerdict(t *testing.T) {
	got := observeWith(t, false, func(context.Context, string, string) (model.EnvironmentIdleStatus, error) {
		t.Fatal("the idle question must not be asked when the port does not answer")
		return model.EnvironmentIdleStatus{}, nil
	})
	if got.Reachable || got.Observed || got.Busy {
		t.Fatalf("an unreachable environment reports nothing, got %+v", got)
	}
}

func TestEnvActivityStateComparesTheVerdict(t *testing.T) {
	answered := ActivityState{Reachable: true, Observed: true}
	silent := ActivityState{Reachable: true}
	if answered == silent {
		t.Fatal("the verdict must take part in the transition check")
	}
}

// An environment not open in this desktop may still be busy.

func observeViaPodWith(
	t *testing.T,
	kubernetesContext string,
	execRuntimePod func(context.Context, uimodel.Selection, string) (string, error),
) ActivityState {
	t.Helper()
	store := fakeStore{envs: map[string]config.EnvConfig{
		"acme/dev": {Name: "dev", KubernetesContext: kubernetesContext},
	}}
	service := newTestService(Deps{Store: store, ExecRuntimePod: execRuntimePod})
	selection := uimodel.Selection{Tenant: "acme", Environment: "dev"}
	return service.observeEnvironmentActivityViaPod(selection, envObservation{selection: selection}).state
}

func TestObserveEnvironmentActivityViaPodReportsAHeldLease(t *testing.T) {
	got := observeViaPodWith(t, "ctx", func(context.Context, uimodel.Selection, string) (string, error) {
		status := model.EnvironmentIdleStatus{
			Leases: []model.EnvironmentActivityLease{
				{ID: "job-fix-1570", Name: "job-fix-1570", ExpiresAt: time.Now().Add(time.Hour)},
			},
			Markers: []model.EnvironmentIdleMarker{{Name: "lease", Idle: false}},
		}
		data, err := json.Marshal(status)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		return string(data), nil
	})
	if !got.Reachable || !got.Observed {
		t.Fatalf("a pod that answers is reachable and observed, got %+v", got)
	}
	if !got.Busy || got.Detail != "holding: job-fix-1570" {
		t.Fatalf("a held lease must read as busy and name the holder, got %+v", got)
	}
	if got.CheckFailed {
		t.Fatalf("a successful probe is not a failed check, got %+v", got)
	}
}

func TestObserveEnvironmentActivityViaPodReportsIdle(t *testing.T) {
	got := observeViaPodWith(t, "ctx", func(context.Context, uimodel.Selection, string) (string, error) {
		data, err := json.Marshal(model.EnvironmentIdleStatus{
			Markers: []model.EnvironmentIdleMarker{{Name: activity.ActivityKindProcess, Idle: true}},
		})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		return string(data), nil
	})
	if !got.Reachable || !got.Observed || got.Busy || got.CheckFailed {
		t.Fatalf("an idle pod answer must not read as busy or unconfirmed, got %+v", got)
	}
}

func TestObserveEnvironmentActivityViaPodNamesAFailedCheckDistinctly(t *testing.T) {
	got := observeViaPodWith(t, "ctx", func(context.Context, uimodel.Selection, string) (string, error) {
		return "", errors.New("kubectl exec: the connection to the server was refused")
	})
	if got.Reachable || got.Observed || got.Busy {
		t.Fatalf("a failed probe carries no verdict about the environment, got %+v", got)
	}
	if !got.CheckFailed {
		t.Fatal("a real attempt that failed must be named distinctly from never asking")
	}
}

func TestObserveEnvironmentActivityViaPodNamesAFailedCheckOnGarbledOutput(t *testing.T) {
	got := observeViaPodWith(t, "ctx", func(context.Context, uimodel.Selection, string) (string, error) {
		return "not json", nil
	})
	if got.Reachable || got.Observed || got.Busy {
		t.Fatalf("unparseable output carries no verdict about the environment, got %+v", got)
	}
	if !got.CheckFailed {
		t.Fatal("unparseable output must be named as a failed check, not silence")
	}
}

func TestObserveEnvironmentActivityViaPodSkipsEnvironmentsWithNoCluster(t *testing.T) {
	got := observeViaPodWith(t, "", func(context.Context, uimodel.Selection, string) (string, error) {
		t.Fatal("an environment with no kubernetes context must never be exec'd into")
		return "", nil
	})
	if got.Reachable || got.Observed || got.Busy || got.CheckFailed {
		t.Fatalf("an environment with no cluster reports nothing, got %+v", got)
	}
}
