package main

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
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// "Answered with no work" and "nobody got an answer" both leave busy false, so
// observed must carry the distinction.

func seedMCPForward(t *testing.T, tenant, environment string, port int) {
	t.Helper()
	// Redirect both roots os.UserConfigDir consults.
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	// adrg/xdg caches ConfigHome at init, so Reload is needed; the
	// environmentIsConfigured guard reads the real on-disk ConfigStore.
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
	data, err := json.Marshal(kube.PortForwardState{
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
	load func(context.Context, string, string) (activity.EnvironmentIdleStatus, error),
) environmentActivityState {
	t.Helper()
	seedMCPForward(t, "acme", "dev", 17500)
	app := NewApp(erunUIDeps{
		store:               stubUIStore{},
		canConnectLocalPort: func(int) bool { return canConnect },
		loadIdleStatus:      load,
	})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app.observeEnvironmentActivity(uiSelection{Tenant: "acme", Environment: "dev"}).state
}

func TestObserveEnvironmentActivityWedgedEdgeGivesNoVerdict(t *testing.T) {
	got := observeWith(t, true, func(context.Context, string, string) (activity.EnvironmentIdleStatus, error) {
		return activity.EnvironmentIdleStatus{}, errors.New("edge did not answer")
	})
	if !got.reachable {
		t.Fatal("a port that accepts connections is still reachable")
	}
	if got.observed {
		t.Fatal("a wedged edge must not be reported as an answered idle question")
	}
	if got.busy {
		t.Fatal("no answer is not evidence of work either")
	}
}

func TestObserveEnvironmentActivityIdleEnvironmentAnswers(t *testing.T) {
	got := observeWith(t, true, func(context.Context, string, string) (activity.EnvironmentIdleStatus, error) {
		return activity.EnvironmentIdleStatus{
			Markers: []activity.EnvironmentIdleMarker{{Name: activity.ActivityKindProcess, Idle: true}},
		}, nil
	})
	if !got.reachable || !got.observed {
		t.Fatalf("an environment that answered is reachable and observed, got %+v", got)
	}
	if got.busy {
		t.Fatal("every marker idle means no work")
	}
}

func TestObserveEnvironmentActivityUnreachableGivesNoVerdict(t *testing.T) {
	got := observeWith(t, false, func(context.Context, string, string) (activity.EnvironmentIdleStatus, error) {
		t.Fatal("the idle question must not be asked when the port does not answer")
		return activity.EnvironmentIdleStatus{}, nil
	})
	if got.reachable || got.observed || got.busy {
		t.Fatalf("an unreachable environment reports nothing, got %+v", got)
	}
}

func TestEnvActivityStateComparesTheVerdict(t *testing.T) {
	answered := environmentActivityState{reachable: true, observed: true}
	silent := environmentActivityState{reachable: true}
	if answered == silent {
		t.Fatal("the verdict must take part in the transition check")
	}
}

// An environment not open in this desktop may still be busy.

func observeViaPodWith(
	t *testing.T,
	kubernetesContext string,
	execRuntimePod func(context.Context, uiSelection, string) (string, error),
) environmentActivityState {
	t.Helper()
	store := stubUIStore{envs: map[string]config.EnvConfig{
		"acme/dev": {Name: "dev", KubernetesContext: kubernetesContext},
	}}
	app := NewApp(erunUIDeps{store: store, execRuntimePod: execRuntimePod})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	selection := uiSelection{Tenant: "acme", Environment: "dev"}
	return app.observeEnvironmentActivityViaPod(selection, environmentActivity{selection: selection}).state
}

func TestObserveEnvironmentActivityViaPodReportsAHeldLease(t *testing.T) {
	got := observeViaPodWith(t, "ctx", func(context.Context, uiSelection, string) (string, error) {
		status := activity.EnvironmentIdleStatus{
			Leases: []activity.EnvironmentActivityLease{
				{ID: "job-fix-1570", Name: "job-fix-1570", ExpiresAt: time.Now().Add(time.Hour)},
			},
			Markers: []activity.EnvironmentIdleMarker{{Name: "lease", Idle: false}},
		}
		data, err := json.Marshal(status)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		return string(data), nil
	})
	if !got.reachable || !got.observed {
		t.Fatalf("a pod that answers is reachable and observed, got %+v", got)
	}
	if !got.busy || got.detail != "holding: job-fix-1570" {
		t.Fatalf("a held lease must read as busy and name the holder, got %+v", got)
	}
	if got.checkFailed {
		t.Fatalf("a successful probe is not a failed check, got %+v", got)
	}
}

func TestObserveEnvironmentActivityViaPodReportsIdle(t *testing.T) {
	got := observeViaPodWith(t, "ctx", func(context.Context, uiSelection, string) (string, error) {
		data, err := json.Marshal(activity.EnvironmentIdleStatus{
			Markers: []activity.EnvironmentIdleMarker{{Name: activity.ActivityKindProcess, Idle: true}},
		})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		return string(data), nil
	})
	if !got.reachable || !got.observed || got.busy || got.checkFailed {
		t.Fatalf("an idle pod answer must not read as busy or unconfirmed, got %+v", got)
	}
}

func TestObserveEnvironmentActivityViaPodNamesAFailedCheckDistinctly(t *testing.T) {
	got := observeViaPodWith(t, "ctx", func(context.Context, uiSelection, string) (string, error) {
		return "", errors.New("kubectl exec: the connection to the server was refused")
	})
	if got.reachable || got.observed || got.busy {
		t.Fatalf("a failed probe carries no verdict about the environment, got %+v", got)
	}
	if !got.checkFailed {
		t.Fatal("a real attempt that failed must be named distinctly from never asking")
	}
}

func TestObserveEnvironmentActivityViaPodNamesAFailedCheckOnGarbledOutput(t *testing.T) {
	got := observeViaPodWith(t, "ctx", func(context.Context, uiSelection, string) (string, error) {
		return "not json", nil
	})
	if got.reachable || got.observed || got.busy {
		t.Fatalf("unparseable output carries no verdict about the environment, got %+v", got)
	}
	if !got.checkFailed {
		t.Fatal("unparseable output must be named as a failed check, not silence")
	}
}

func TestObserveEnvironmentActivityViaPodSkipsEnvironmentsWithNoCluster(t *testing.T) {
	got := observeViaPodWith(t, "", func(context.Context, uiSelection, string) (string, error) {
		t.Fatal("an environment with no kubernetes context must never be exec'd into")
		return "", nil
	})
	if got.reachable || got.observed || got.busy || got.checkFailed {
		t.Fatalf("an environment with no cluster reports nothing, got %+v", got)
	}
}
