package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentIdleConfigDefaults(t *testing.T) {
	policy, err := (EnvironmentIdleConfig{}).Resolve()
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if policy.Timeout != 5*time.Minute {
		t.Fatalf("unexpected timeout: %v", policy.Timeout)
	}
	if policy.WorkingHours != "08:00-20:00" {
		t.Fatalf("unexpected working hours: %q", policy.WorkingHours)
	}
}

func TestResolveEnvironmentIdleStatusRequiresAllMarkers(t *testing.T) {
	now := time.Date(2026, 4, 28, 17, 0, 0, 0, time.Local)
	status, err := ResolveEnvironmentIdleStatus(EnvironmentIdleConfig{}, map[string]EnvironmentActivitySnapshot{
		ActivityKindSSH:   {LastActivity: now.Add(-10 * time.Minute), Bytes: 0},
		ActivityKindMCP:   {LastActivity: now.Add(-10 * time.Minute)},
		ActivityKindCLI:   {LastActivity: now.Add(-10 * time.Minute)},
		ActivityKindCodex: {LastActivity: now.Add(-10 * time.Minute)},
	}, now)
	if err != nil {
		t.Fatalf("ResolveEnvironmentIdleStatus failed: %v", err)
	}
	if !status.StopEligible {
		t.Fatalf("expected stop eligible status: %+v", status.Markers)
	}

	status, err = ResolveEnvironmentIdleStatus(EnvironmentIdleConfig{}, map[string]EnvironmentActivitySnapshot{
		ActivityKindSSH:   {LastActivity: now.Add(-10 * time.Minute), Bytes: 0},
		ActivityKindMCP:   {LastActivity: now.Add(-10 * time.Minute)},
		ActivityKindCLI:   {LastActivity: now.Add(-1 * time.Minute)},
		ActivityKindCodex: {LastActivity: now.Add(-10 * time.Minute)},
	}, now)
	if err != nil {
		t.Fatalf("ResolveEnvironmentIdleStatus failed: %v", err)
	}
	if status.StopEligible {
		t.Fatalf("expected active CLI marker to block stop: %+v", status.Markers)
	}
	if status.StopBlockedReason != "cli: recent activity" {
		t.Fatalf("unexpected stop blocked reason: %q", status.StopBlockedReason)
	}
}

func TestResolveEnvironmentIdleStatusStopsByIdleDuringWorkingHours(t *testing.T) {
	now := time.Date(2026, 4, 28, 17, 21, 0, 0, time.Local)
	status, err := ResolveEnvironmentIdleStatus(EnvironmentIdleConfig{
		Timeout:      "10s",
		WorkingHours: "08:00-20:00",
	}, map[string]EnvironmentActivitySnapshot{
		ActivityKindSSH:   {LastActivity: now.Add(-10 * time.Minute), Bytes: 0},
		ActivityKindMCP:   {LastActivity: now.Add(-10 * time.Minute)},
		ActivityKindCLI:   {LastActivity: now.Add(-10 * time.Minute)},
		ActivityKindCodex: {LastActivity: now.Add(-10 * time.Minute)},
	}, now)
	if err != nil {
		t.Fatalf("ResolveEnvironmentIdleStatus failed: %v", err)
	}
	if !status.StopEligible {
		t.Fatalf("expected idle environment to stop during working hours: %+v", status.Markers)
	}
	if status.StopBlockedReason != "" {
		t.Fatalf("unexpected stop blocked reason: %q", status.StopBlockedReason)
	}
}

func TestResolveEnvironmentIdleStatusStopsOutsideWorkingHoursRegardlessOfActivity(t *testing.T) {
	now := time.Date(2026, 4, 28, 21, 0, 0, 0, time.Local)
	status, err := ResolveEnvironmentIdleStatus(EnvironmentIdleConfig{
		Timeout:      "10s",
		WorkingHours: "08:00-20:00",
	}, map[string]EnvironmentActivitySnapshot{
		ActivityKindSSH:   {LastActivity: now, Bytes: 100},
		ActivityKindMCP:   {LastActivity: now},
		ActivityKindCLI:   {LastActivity: now},
		ActivityKindCodex: {LastActivity: now},
	}, now)
	if err != nil {
		t.Fatalf("ResolveEnvironmentIdleStatus failed: %v", err)
	}
	if !status.OutsideWorkingHours {
		t.Fatalf("expected outside working hours")
	}
	if !status.StopEligible {
		t.Fatalf("expected outside working hours to force stop eligibility: %+v", status.Markers)
	}
	if status.SecondsUntilStop != 0 {
		t.Fatalf("expected immediate stop outside working hours, got %d", status.SecondsUntilStop)
	}
}

func TestResolveEnvironmentIdleStatusDetectsCodexOpenIdle(t *testing.T) {
	now := time.Date(2026, 4, 28, 21, 0, 0, 0, time.Local)
	status, err := ResolveEnvironmentIdleStatus(EnvironmentIdleConfig{}, map[string]EnvironmentActivitySnapshot{
		ActivityKindSSH:   {LastActivity: now.Add(-10 * time.Minute), Bytes: 0},
		ActivityKindMCP:   {LastActivity: now.Add(-10 * time.Minute)},
		ActivityKindCLI:   {LastActivity: now.Add(-10 * time.Minute)},
		ActivityKindCodex: {LastActivity: now.Add(-10 * time.Minute), LastSeen: now.Add(-1 * time.Minute)},
	}, now)
	if err != nil {
		t.Fatalf("ResolveEnvironmentIdleStatus failed: %v", err)
	}
	for _, marker := range status.Markers {
		if marker.Name == ActivityKindCodex && marker.Reason != "codex is open but idle" {
			t.Fatalf("expected codex open-idle marker, got %+v", marker)
		}
	}
	if !status.StopEligible {
		t.Fatalf("expected open-idle codex to allow stop: %+v", status.Markers)
	}
}

func TestResolveStoredEnvironmentIdleStatusStopsOnlyCloudManagedEnvironments(t *testing.T) {
	now := time.Date(2026, 4, 28, 21, 0, 0, 0, time.Local)
	store := idleStatusTestStore{
		global: ERunConfig{
			CloudContexts: []CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		envs: map[string]EnvConfig{
			"tenant/local": {
				Name:              "local",
				KubernetesContext: "cluster-local",
				Remote:            true,
			},
			"tenant/cloud": {
				Name:              "cloud",
				KubernetesContext: "cluster-cloud",
				Remote:            true,
			},
		},
	}

	localStatus, err := ResolveStoredEnvironmentIdleStatus(store, "tenant", "local", now)
	if err != nil {
		t.Fatalf("ResolveStoredEnvironmentIdleStatus local failed: %v", err)
	}
	if localStatus.StopEligible {
		t.Fatalf("expected non-cloud environment to be blocked from stop")
	}
	if localStatus.StopBlockedReason != "environment is not cloud-managed" {
		t.Fatalf("unexpected stop blocked reason: %q", localStatus.StopBlockedReason)
	}

	cloudStatus, err := ResolveStoredEnvironmentIdleStatus(store, "tenant", "cloud", now)
	if err != nil {
		t.Fatalf("ResolveStoredEnvironmentIdleStatus cloud failed: %v", err)
	}
	if !cloudStatus.ManagedCloud {
		t.Fatalf("expected cloud environment to be detected as managed")
	}
	if !cloudStatus.StopEligible {
		t.Fatalf("expected idle cloud environment to be stop eligible: %s", cloudStatus.StopBlockedReason)
	}
}

func TestResolveStoredEnvironmentIdleStatusIncludesStopError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".erun"), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".erun", "idle-stop.log"), []byte("failed to stop instance: access denied\n"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	now := time.Date(2026, 4, 28, 21, 0, 0, 0, time.Local)
	store := idleStatusTestStore{
		global: ERunConfig{
			CloudContexts: []CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		envs: map[string]EnvConfig{
			"tenant/cloud": {
				Name:              "cloud",
				KubernetesContext: "cluster-cloud",
				Remote:            true,
			},
		},
	}

	status, err := ResolveStoredEnvironmentIdleStatus(store, "tenant", "cloud", now)
	if err != nil {
		t.Fatalf("ResolveStoredEnvironmentIdleStatus failed: %v", err)
	}
	if !strings.Contains(status.StopError, "access denied") {
		t.Fatalf("expected stop error to include log contents, got %q", status.StopError)
	}
}

type idleStatusTestStore struct {
	global ERunConfig
	envs   map[string]EnvConfig
}

func (s idleStatusTestStore) LoadERunConfig() (ERunConfig, string, error) {
	return s.global, "", nil
}

func (s idleStatusTestStore) LoadEnvConfig(tenant, environment string) (EnvConfig, string, error) {
	return s.envs[tenant+"/"+environment], "", nil
}
