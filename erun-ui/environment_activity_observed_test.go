package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
