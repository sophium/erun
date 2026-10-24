package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/cloud"
	openpkg "github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func TestMergeNewerActivityMarkersPrefersLocalTerminalActivity(t *testing.T) {
	now := time.Now()
	remote := activity.EnvironmentIdleStatus{
		Markers: []activity.EnvironmentIdleMarker{
			{Name: "working-hours", LastActivity: now},
			{Name: configpkg.ActivityKindCLI, LastActivity: now.Add(-4 * time.Minute), SecondsRemaining: 60},
			{Name: configpkg.ActivityKindMCP, LastActivity: now.Add(-1 * time.Minute), SecondsRemaining: 240},
		},
	}
	local := activity.EnvironmentIdleStatus{
		Markers: []activity.EnvironmentIdleMarker{
			{Name: configpkg.ActivityKindCLI, LastActivity: now, SecondsRemaining: 300},
			{Name: configpkg.ActivityKindMCP, LastActivity: now.Add(-2 * time.Minute), SecondsRemaining: 180},
		},
	}

	merged := mergeNewerActivityMarkers(remote, local)
	if got := activitySecondsUntilIdle(merged); got != 300 {
		t.Fatalf("activitySecondsUntilIdle = %d, want 300", got)
	}
	for _, marker := range merged.Markers {
		if marker.Name == configpkg.ActivityKindMCP && marker.SecondsRemaining != 240 {
			t.Fatalf("older local MCP marker should not replace remote marker: %+v", marker)
		}
	}
}

func TestMergeNewerActivityMarkersAddsMissingLocalTerminalActivity(t *testing.T) {
	now := time.Now()
	remote := activity.EnvironmentIdleStatus{
		ManagedCloud: true,
		Markers: []activity.EnvironmentIdleMarker{
			{Name: "working-hours", LastActivity: now},
			{Name: configpkg.ActivityKindMCP, Idle: true, LastActivity: now.Add(-10 * time.Minute), SecondsRemaining: 0},
		},
	}
	local := activity.EnvironmentIdleStatus{
		ManagedCloud: true,
		Markers: []activity.EnvironmentIdleMarker{
			{Name: configpkg.ActivityKindCLI, Idle: false, LastActivity: now, SecondsRemaining: 60},
		},
	}

	merged := mergeNewerActivityMarkers(remote, local)
	if merged.StopEligible {
		t.Fatal("expected local CLI activity to block idle stop")
	}
	if got := activitySecondsUntilIdle(merged); got != 60 {
		t.Fatalf("activitySecondsUntilIdle = %d, want 60", got)
	}
	if merged.StopBlockedReason != configpkg.ActivityKindCLI {
		t.Fatalf("StopBlockedReason = %q, want %q", merged.StopBlockedReason, configpkg.ActivityKindCLI)
	}
}

func TestMergeLocalIdleActivityUsesSavedPolicyWithRemoteActivity(t *testing.T) {
	now := time.Now()
	store := stubUIStore{
		envs: map[string]configpkg.EnvConfig{
			"team-stop/dev-stop": {
				Name: "dev-stop",
				Idle: configpkg.EnvironmentIdleConfig{
					Timeout:      "10s",
					WorkingHours: "00:00-23:59",
				},
				ManagedCloud: true,
				Type:         configpkg.EnvironmentTypeRuntime,
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})
	defer app.shutdown(context.Background())

	remote := activity.EnvironmentIdleStatus{
		Policy: configpkg.EnvironmentIdlePolicy{
			Timeout:      5 * time.Minute,
			WorkingHours: "00:00-23:59",
		},
		ManagedCloud: true,
		Markers: []activity.EnvironmentIdleMarker{
			{Name: configpkg.ActivityKindCLI, Idle: false, SecondsRemaining: 295},
		},
		Activity: map[string]activity.EnvironmentActivitySnapshot{
			configpkg.ActivityKindCLI: {LastActivity: now.Add(-5 * time.Second), LastSeen: now.Add(-5 * time.Second)},
		},
	}

	merged := app.mergeLocalIdleActivity(configpkg.OpenResult{
		Tenant:      "team-stop",
		Environment: "dev-stop",
		EnvConfig:   store.envs["team-stop/dev-stop"],
	}, remote)

	if merged.Policy.Timeout != 10*time.Second {
		t.Fatalf("expected saved local timeout, got %s", merged.Policy.Timeout)
	}
	if got := activitySecondsUntilIdle(merged); got <= 0 || got > 10 {
		t.Fatalf("activitySecondsUntilIdle = %d, want local 10s policy countdown", got)
	}
}

func TestStartCloudContextClearsPreviousIdleStop(t *testing.T) {
	store := stubUIStore{
		config: &configpkg.ERunConfig{
			CloudContexts: []configpkg.CloudContextConfig{{
				Name:               "cloud-ctx",
				CloudProviderAlias: "team-cloud",
				KubernetesContext:  "cluster-cloud",
			}},
		},
		tenants: map[string]configpkg.TenantConfig{
			"team-stop": {
				Name:               "team-stop",
				DefaultEnvironment: "dev-stop",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"team-stop/dev-stop": {
				Name:               "dev-stop",
				KubernetesContext:  "cluster-cloud",
				CloudProviderAlias: "team-cloud",
				ManagedCloud:       true,
				Type:               configpkg.EnvironmentTypeRuntime,
			},
		},
	}
	key := selectionKey(uiSelection{Tenant: "team-stop", Environment: "dev-stop"})
	app := NewApp(erunUIDeps{store: store})
	defer app.shutdown(context.Background())

	app.idleStops[key] = struct{}{}
	app.clearIdleStopsForCloudContext("cloud-ctx")

	if _, exists := app.idleStops[key]; exists {
		t.Fatal("expected previous idle stop marker to be cleared")
	}
}

func TestLoadIdleStatusDoesNotStopWhileEnvironmentCommandRunning(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &configpkg.ERunConfig{
			CloudContexts: []configpkg.CloudContextConfig{{
				Name:               "cloud-ctx",
				CloudProviderAlias: "team-cloud",
				KubernetesContext:  "cluster-cloud",
			}},
		},
		tenants: map[string]configpkg.TenantConfig{
			"team-busy": {
				Name:               "team-busy",
				DefaultEnvironment: "dev-busy",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"team-busy/dev-busy": {
				Name:               "dev-busy",
				LocalRepoPath:      projectRoot,
				KubernetesContext:  "cluster-cloud",
				CloudProviderAlias: "team-cloud",
				ManagedCloud:       true,
				Type:               configpkg.EnvironmentTypeRuntime,
			},
		},
	}
	stopped := make(chan string, 1)
	app := NewApp(erunUIDeps{
		store:           store,
		resolveCLIPath:  func() string { return "/tmp/erun" },
		findProjectRoot: func() (string, string, error) { return "project", projectRoot, nil },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		canConnectLocalPort: func(int) bool { return true },
		canReachMCPEndpoint: func(int) bool { return true },
		loadIdleStatus: func(context.Context, string, string) (activity.EnvironmentIdleStatus, error) {
			return activity.EnvironmentIdleStatus{
				ManagedCloud: true,
				StopEligible: true,
				Policy: configpkg.EnvironmentIdlePolicy{
					Timeout: 5 * time.Minute,
				},
				Markers: []activity.EnvironmentIdleMarker{
					{Name: "working-hours", Idle: true},
					{Name: configpkg.ActivityKindSSH, Idle: true},
					{Name: configpkg.ActivityKindMCP, Idle: true},
					{Name: configpkg.ActivityKindCLI, Idle: true},
					{Name: configpkg.ActivityKindCodex, Idle: true},
				},
			}, nil
		},
		stopCloudContext: func(_ context.Context, name string) (configpkg.CloudContextStatus, error) {
			stopped <- name
			return configpkg.CloudContextStatus{}, nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartDeploySession(uiSelection{Tenant: "team-busy", Environment: "dev-busy", Version: "1.0.0"}, 80, 24); err != nil {
		t.Fatalf("StartDeploySession failed: %v", err)
	}
	if _, err := app.LoadIdleStatus(uiSelection{Tenant: "team-busy", Environment: "dev-busy"}); err != nil {
		t.Fatalf("LoadIdleStatus failed: %v", err)
	}

	select {
	case got := <-stopped:
		t.Fatalf("did not expect cloud context stop while deploy is running, got %q", got)
	default:
	}
}

func TestIdleStatusToUIIncludesBlockerDetails(t *testing.T) {
	status := idleStatusToUI(activity.EnvironmentIdleStatus{
		ManagedCloud:      true,
		StopEligible:      false,
		StopBlockedReason: "waiting for activity timeout",
		StopError:         "failed to stop instance: access denied",
		Policy: configpkg.EnvironmentIdlePolicy{
			Timeout: 5 * time.Minute,
		},
		Markers: []activity.EnvironmentIdleMarker{
			{Name: configpkg.ActivityKindSSH, Idle: false, Reason: "recent activity", SecondsRemaining: 42},
			{Name: configpkg.ActivityKindMCP, Idle: true, Reason: "last activity exceeded timeout"},
		},
	})

	assertIdleStatusBlockers(t, status)
}

func assertIdleStatusBlockers(t *testing.T, status uiIdleStatus) {
	t.Helper()

	if status.TimeoutSeconds != 300 || status.SecondsUntilStop != 42 || !status.ManagedCloud || status.StopEligible {
		t.Fatalf("unexpected idle status: %+v", status)
	}
	if status.StopBlockedReason != "waiting for activity timeout" {
		t.Fatalf("unexpected stop blocked reason: %q", status.StopBlockedReason)
	}
	if status.StopError != "failed to stop instance: access denied" {
		t.Fatalf("unexpected stop error: %q", status.StopError)
	}
	assertIdleStatusMarkers(t, status.Markers)
}

func assertIdleStatusMarkers(t *testing.T, markers []uiIdleMarker) {
	t.Helper()

	if len(markers) != 2 || markers[0].Name != configpkg.ActivityKindSSH || markers[0].Reason != "recent activity" || markers[0].SecondsRemaining != 42 {
		t.Fatalf("unexpected markers: %+v", markers)
	}
}

func TestIdleStatusToUIProjectsMarkerClients(t *testing.T) {
	// Locks the bridge between the per-IP marker data resolved by
	// erun-common and the JSON shape the desktop tooltip consumes. The
	// SSH proxy is the only kind that populates Clients today, but the
	// projection is generic so any future per-client surface gets the
	// same treatment without re-plumbing.
	status := idleStatusToUI(activity.EnvironmentIdleStatus{
		Policy: configpkg.EnvironmentIdlePolicy{Timeout: 5 * time.Minute},
		Markers: []activity.EnvironmentIdleMarker{
			{
				Name:             configpkg.ActivityKindSSH,
				Idle:             false,
				Reason:           "recent activity",
				SecondsRemaining: 50,
				Clients: []activity.EnvironmentIdleMarkerClient{
					{Address: "10.0.4.7", Bytes: 1500, SecondsAgo: 2},
					{Address: "127.0.0.1", Bytes: 548, SecondsAgo: 9},
				},
			},
			{Name: configpkg.ActivityKindCLI, Idle: true, Reason: "no activity recorded"},
		},
	})
	if len(status.Markers) != 2 {
		t.Fatalf("expected 2 markers, got %+v", status.Markers)
	}
	ssh := status.Markers[0]
	if len(ssh.Clients) != 2 {
		t.Fatalf("expected SSH marker to carry 2 clients, got %+v", ssh.Clients)
	}
	if ssh.Clients[0].Address != "10.0.4.7" || ssh.Clients[0].Bytes != 1500 || ssh.Clients[0].SecondsAgo != 2 {
		t.Fatalf("unexpected first client: %+v", ssh.Clients[0])
	}
	if ssh.Clients[1].Address != "127.0.0.1" || ssh.Clients[1].Bytes != 548 || ssh.Clients[1].SecondsAgo != 9 {
		t.Fatalf("unexpected second client: %+v", ssh.Clients[1])
	}
	if status.Markers[1].Clients != nil {
		t.Fatalf("CLI marker should have no clients, got %+v", status.Markers[1].Clients)
	}
}

// TestIdleStatusToUICarriesLeases pins erun#1221: idle status already resolves
// the leases holding an environment, but the UI projection dropped them before
// this fix — the AI tab occupancy notice and any future consumer (#1245,
// #1241) both read this field, so a silent regression here breaks them too.
func TestIdleStatusToUICarriesLeases(t *testing.T) {
	now := time.Now()
	status := idleStatusToUI(activity.EnvironmentIdleStatus{
		Policy: configpkg.EnvironmentIdlePolicy{Timeout: 5 * time.Minute},
		Leases: []activity.EnvironmentActivityLease{
			{ID: "job-fix-1201", Name: "job-fix-1201", PID: 4242, StartedAt: now.Add(-90 * time.Second)},
		},
	})
	if len(status.Leases) != 1 {
		t.Fatalf("expected 1 lease carried through, got %+v", status.Leases)
	}
	lease := status.Leases[0]
	if lease.Name != "job-fix-1201" {
		t.Fatalf("unexpected lease name: %q", lease.Name)
	}
	if lease.SecondsHeld < 89 || lease.SecondsHeld > 91 {
		t.Fatalf("expected ~90 seconds held, got %d", lease.SecondsHeld)
	}
}

func TestMaybeStopIdleClearsStaleIdleStopWhenContextIsRunningAgain(t *testing.T) {
	// idleStops latches on the first successful stop so the desktop
	// does not re-fire a stop while one is in flight. When the context
	// gets restarted externally (CLI preflight, manual titlebar Play,
	// `erun context start`), the desktop's flag turns stale and a
	// second auto-stop can never fire. maybeStopIdleCloudEnvironment
	// reconciles by clearing the flag whenever it observes a running
	// context. This test exercises the reconcile via the public
	// LoadIdleStatus path so the merge + recompute chain stays honest.
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &configpkg.ERunConfig{
			CloudContexts: []configpkg.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]configpkg.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              configpkg.EnvironmentTypeRuntime,
				ManagedCloud:      true,
			},
		},
	}
	app := NewApp(erunUIDeps{
		store:               store,
		canConnectLocalPort: func(int) bool { return false },
		canReachMCPEndpoint: func(int) bool { return false },
		loadIdleStatus: func(context.Context, string, string) (activity.EnvironmentIdleStatus, error) {
			return activity.EnvironmentIdleStatus{}, fmt.Errorf("mcp unreachable")
		},
		stopCloudContext: func(context.Context, string) (configpkg.CloudContextStatus, error) {
			t.Fatal("stop should not fire while context is running")
			return configpkg.CloudContextStatus{}, nil
		},
	})
	defer app.shutdown(context.Background())
	app.setCloudContextStatusInCache("managed-cloud", cloud.CloudContextStatusRunning)

	key := selectionKey(uiSelection{Tenant: "erun", Environment: "remote"})
	app.mu.Lock()
	app.idleStops[key] = struct{}{}
	app.mu.Unlock()

	if _, err := app.LoadIdleStatus(uiSelection{Tenant: "erun", Environment: "remote"}); err != nil {
		t.Fatalf("LoadIdleStatus returned an error: %v", err)
	}

	app.mu.Lock()
	_, present := app.idleStops[key]
	app.mu.Unlock()
	if present {
		t.Fatal("expected stale idle-stop flag to be cleared while context is running")
	}
}

func TestIdleStatusToUIClearsStopErrorWhenContextIsRunning(t *testing.T) {
	// idle-stop.log lives on the pod's home PVC and survives pod and host
	// restarts. The runtime entrypoint truncates it on monitor start, but a
	// freshly-restarted env can still surface a poll-window racing the
	// truncate. The desktop guards against the stale error by suppressing
	// StopError whenever the linked cloud context is observably running —
	// a "stop failed" badge next to a healthy env is always wrong.
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &configpkg.ERunConfig{
			CloudContexts: []configpkg.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]configpkg.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              configpkg.EnvironmentTypeRuntime,
				ManagedCloud:      true,
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})
	defer app.shutdown(context.Background())
	app.setCloudContextStatusInCache("managed-cloud", cloud.CloudContextStatusRunning)

	result, err := openpkg.ResolveOpen(store, shared.OpenParams{Tenant: "erun", Environment: "remote"})
	if err != nil {
		t.Fatalf("ResolveOpen failed: %v", err)
	}
	ui := app.idleStatusToUI(result, activity.EnvironmentIdleStatus{
		ManagedCloud: true,
		Policy:       configpkg.EnvironmentIdlePolicy{Timeout: 5 * time.Minute},
		StopError:    "An error occurred (RequestExpired) when calling the StopInstances operation: Request has expired.",
	}, true)

	if ui.CloudContextStatus != cloud.CloudContextStatusRunning {
		t.Fatalf("expected CloudContextStatus=%q, got %q", cloud.CloudContextStatusRunning, ui.CloudContextStatus)
	}
	if ui.StopError != "" {
		t.Fatalf("expected StopError to be cleared when context is running, got %q", ui.StopError)
	}
}

func TestIdleStatusToUIKeepsStopErrorWhenContextIsStopped(t *testing.T) {
	// Counterpart to the running-context test: when the linked cloud
	// context is not running, the stop error is still the desktop's only
	// surface for telling the user "the last auto-stop failed", so the
	// projection must pass it through unchanged.
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &configpkg.ERunConfig{
			CloudContexts: []configpkg.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]configpkg.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              configpkg.EnvironmentTypeRuntime,
				ManagedCloud:      true,
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})
	defer app.shutdown(context.Background())
	app.setCloudContextStatusInCache("managed-cloud", cloud.CloudContextStatusStopped)

	result, err := openpkg.ResolveOpen(store, shared.OpenParams{Tenant: "erun", Environment: "remote"})
	if err != nil {
		t.Fatalf("ResolveOpen failed: %v", err)
	}
	ui := app.idleStatusToUI(result, activity.EnvironmentIdleStatus{
		ManagedCloud: true,
		Policy:       configpkg.EnvironmentIdlePolicy{Timeout: 5 * time.Minute},
		StopError:    "An error occurred (RequestExpired) when calling the StopInstances operation: Request has expired.",
	}, true)

	if ui.CloudContextStatus != cloud.CloudContextStatusStopped {
		t.Fatalf("expected CloudContextStatus=%q, got %q", cloud.CloudContextStatusStopped, ui.CloudContextStatus)
	}
	if ui.StopError == "" {
		t.Fatal("expected StopError to be preserved when context is stopped")
	}
}
