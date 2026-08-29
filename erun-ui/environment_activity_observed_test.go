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
	eruncommon "github.com/sophium/erun/erun-common"
)

// The sidebar lets an environment's own answer stop a row spinning, which makes
// the difference between "it answered, and reports no work" and "nobody got an
// answer" load-bearing. Both leave busy false, so the observation has to carry
// the distinction itself — otherwise an edge that has wedged behind a port that
// still accepts connections would clear a latch on the environment's behalf and
// hide work that is still running.

func seedMCPForward(t *testing.T, tenant, environment string, port int) {
	t.Helper()
	// Redirect both roots os.UserConfigDir consults, so the seeded forward is
	// found on every host rather than only the ones that honour XDG.
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	// LoadPortForwardState's environmentIsConfigured guard (added for #1049) reads
	// through the real on-disk ConfigStore, not any App-injected store stub — on
	// purpose, so a stale record for a deleted environment reads as "no forward"
	// rather than a live one. adrg/xdg caches ConfigHome at process init instead
	// of re-reading the environment per call, so the Setenv calls above alone do
	// not redirect it; Reload is what makes it honour this test's temp root, and
	// SaveEnvConfig is what makes the guard see this tenant/environment as
	// configured there.
	xdg.Reload()
	t.Cleanup(xdg.Reload)
	if err := eruncommon.SaveEnvConfig(tenant, eruncommon.EnvConfig{Name: environment}); err != nil {
		t.Fatalf("SaveEnvConfig: %v", err)
	}
	path, err := eruncommon.PortForwardStatePath("mcp", tenant, environment)
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.Marshal(eruncommon.PortForwardState{
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
	load func(context.Context, string, string) (eruncommon.EnvironmentIdleStatus, error),
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
	got := observeWith(t, true, func(context.Context, string, string) (eruncommon.EnvironmentIdleStatus, error) {
		return eruncommon.EnvironmentIdleStatus{}, errors.New("edge did not answer")
	})
	if !got.reachable {
		t.Fatal("a port that accepts connections is still reachable")
	}
	// The claim that matters: nobody asked the environment anything, so the
	// observation must not read as the environment reporting no work.
	if got.observed {
		t.Fatal("a wedged edge must not be reported as an answered idle question")
	}
	if got.busy {
		t.Fatal("no answer is not evidence of work either")
	}
}

func TestObserveEnvironmentActivityIdleEnvironmentAnswers(t *testing.T) {
	got := observeWith(t, true, func(context.Context, string, string) (eruncommon.EnvironmentIdleStatus, error) {
		return eruncommon.EnvironmentIdleStatus{
			Markers: []eruncommon.EnvironmentIdleMarker{{Name: eruncommon.ActivityKindProcess, Idle: true}},
		}, nil
	})
	if !got.reachable || !got.observed {
		t.Fatalf("an environment that answered is reachable and observed, got %+v", got)
	}
	if got.busy {
		t.Fatal("every marker idle means no work")
	}
}

// A dropped forward is a repair, not a verdict: the environment cannot be asked
// anything through a port nothing holds, so the observation stays empty while
// the repair runs beside it. What the repair then does with it — and the
// distinction between an environment that had a forward and one that never did
// — belongs to environment_forward_repair_test.go.
func TestObserveEnvironmentActivityUnreachableGivesNoVerdict(t *testing.T) {
	got := observeWith(t, false, func(context.Context, string, string) (eruncommon.EnvironmentIdleStatus, error) {
		t.Fatal("the idle question must not be asked when the port does not answer")
		return eruncommon.EnvironmentIdleStatus{}, nil
	})
	if got.reachable || got.observed || got.busy {
		t.Fatalf("an unreachable environment reports nothing, got %+v", got)
	}
}

// A transition in the verdict alone has to reach the sidebar: an environment
// that stops answering while still holding its port changes nothing else about
// the observation, and that is exactly the moment the row must stop trusting it.
func TestEnvActivityStateComparesTheVerdict(t *testing.T) {
	answered := environmentActivityState{reachable: true, observed: true}
	silent := environmentActivityState{reachable: true}
	if answered == silent {
		t.Fatal("the verdict must take part in the transition check")
	}
}

// An environment not open in this desktop is not the same as an environment
// nobody is using. The activity lease this poller looks for is
// environment-side state, held by whatever is driving the environment — a CLI
// orchestrator, an agent over MCP from another machine — not by whoever
// happened to open a local forward to it. These tests lock in the fallback
// that asks such an environment directly, over its own runtime pod, instead
// of reporting "not open here" about one that might be busy right now.

func observeViaPodWith(
	t *testing.T,
	kubernetesContext string,
	execRuntimePod func(context.Context, uiSelection, string) (string, error),
) environmentActivityState {
	t.Helper()
	store := stubUIStore{envs: map[string]eruncommon.EnvConfig{
		"acme/dev": {Name: "dev", KubernetesContext: kubernetesContext},
	}}
	app := NewApp(erunUIDeps{store: store, execRuntimePod: execRuntimePod})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	selection := uiSelection{Tenant: "acme", Environment: "dev"}
	return app.observeEnvironmentActivityViaPod(selection, environmentActivity{selection: selection}).state
}

// TestObserveEnvironmentActivityViaPodReportsAHeldLease is the exact case that
// was broken: an environment this desktop never opened, but that another
// session is holding busy right now, must read as busy — not as unreachable.
func TestObserveEnvironmentActivityViaPodReportsAHeldLease(t *testing.T) {
	got := observeViaPodWith(t, "ctx", func(context.Context, uiSelection, string) (string, error) {
		status := eruncommon.EnvironmentIdleStatus{
			Leases: []eruncommon.EnvironmentActivityLease{
				{ID: "job-fix-1570", Name: "job-fix-1570", ExpiresAt: time.Now().Add(time.Hour)},
			},
			Markers: []eruncommon.EnvironmentIdleMarker{{Name: "lease", Idle: false}},
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

// TestObserveEnvironmentActivityViaPodReportsIdle is the other real answer: the
// pod is reachable and genuinely has nothing running.
func TestObserveEnvironmentActivityViaPodReportsIdle(t *testing.T) {
	got := observeViaPodWith(t, "ctx", func(context.Context, uiSelection, string) (string, error) {
		data, err := json.Marshal(eruncommon.EnvironmentIdleStatus{
			Markers: []eruncommon.EnvironmentIdleMarker{{Name: eruncommon.ActivityKindProcess, Idle: true}},
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

// TestObserveEnvironmentActivityViaPodNamesAFailedCheckDistinctly is the third
// state this fallback introduces: a real attempt that did not come back must
// not collapse into "nobody asked" — see checkFailed's own comment.
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

// TestObserveEnvironmentActivityViaPodNamesAFailedCheckOnGarbledOutput covers
// the other failure shape: the exec succeeded but what came back cannot be
// trusted as the environment's answer.
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

// TestObserveEnvironmentActivityViaPodSkipsEnvironmentsWithNoCluster is the
// cheap-common-case guard: an environment that has never named a cluster it
// could run in has nothing to probe, so it must not pay for an exec attempt
// that could only ever fail.
func TestObserveEnvironmentActivityViaPodSkipsEnvironmentsWithNoCluster(t *testing.T) {
	got := observeViaPodWith(t, "", func(context.Context, uiSelection, string) (string, error) {
		t.Fatal("an environment with no kubernetes context must never be exec'd into")
		return "", nil
	})
	if got.reachable || got.observed || got.busy || got.checkFailed {
		t.Fatalf("an environment with no cluster reports nothing, got %+v", got)
	}
}
