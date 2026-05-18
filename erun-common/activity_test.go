package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrg/xdg"
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

func TestResolveEnvironmentIdleStatusUsesConfiguredTimezone(t *testing.T) {
	// 06:00 UTC is before the 08:00 working-hours start when interpreted as UTC,
	// but it is 15:00 on the same day in Asia/Tokyo (UTC+9) — inside working hours.
	now := time.Date(2026, 4, 28, 6, 0, 0, 0, time.UTC)

	statusUTC, err := ResolveEnvironmentIdleStatus(EnvironmentIdleConfig{
		Timeout:      "5m",
		WorkingHours: "08:00-20:00",
	}, nil, now)
	if err != nil {
		t.Fatalf("UTC resolve failed: %v", err)
	}
	if !statusUTC.OutsideWorkingHours {
		t.Fatalf("expected 06:00 (now's native timezone, here UTC) to be outside 08:00-20:00 when no timezone override is configured")
	}

	statusJST, err := ResolveEnvironmentIdleStatus(EnvironmentIdleConfig{
		Timeout:      "5m",
		WorkingHours: "08:00-20:00",
		Timezone:     "Asia/Tokyo",
	}, nil, now)
	if err != nil {
		t.Fatalf("Asia/Tokyo resolve failed: %v", err)
	}
	if statusJST.OutsideWorkingHours {
		t.Fatalf("expected 06:00 UTC (= 15:00 JST) to be inside 08:00-20:00 Asia/Tokyo, got OutsideWorkingHours=true")
	}
}

func TestSSHMarkerIdlesOnLastActivityRegardlessOfAccumulatedBytes(t *testing.T) {
	// Regression: Bytes accumulates monotonically in the activity snapshot.
	// The marker should still go idle once LastActivity exceeds the timeout,
	// even though Bytes is large from earlier traffic.
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.Local)
	status, err := ResolveEnvironmentIdleStatus(EnvironmentIdleConfig{
		Timeout:      "5m",
		WorkingHours: "08:00-20:00",
	}, map[string]EnvironmentActivitySnapshot{
		ActivityKindSSH:   {LastActivity: now.Add(-10 * time.Minute), Bytes: 1_000_000},
		ActivityKindMCP:   {LastActivity: now.Add(-10 * time.Minute)},
		ActivityKindCLI:   {LastActivity: now.Add(-10 * time.Minute)},
		ActivityKindCodex: {LastActivity: now.Add(-10 * time.Minute)},
	}, now)
	if err != nil {
		t.Fatalf("ResolveEnvironmentIdleStatus failed: %v", err)
	}
	if !status.StopEligible {
		t.Fatalf("expected SSH marker to idle on LastActivity timeout regardless of accumulated bytes, markers=%+v", status.Markers)
	}
}

func TestEnvironmentIdleConfigRejectsInvalidTimezone(t *testing.T) {
	_, err := EnvironmentIdleConfig{Timezone: "Not/A_Place"}.Resolve()
	if err == nil {
		t.Fatalf("expected invalid timezone error")
	}
}

func TestResolveStoredEnvironmentIdleStatusDetectsManagedCloudWhenRepoIsLocal(t *testing.T) {
	now := time.Date(2026, 4, 28, 21, 0, 0, 0, time.Local)
	store := idleStatusTestStore{
		envs: map[string]EnvConfig{
			"tenant/cloud": {
				Name:              "cloud",
				KubernetesContext: "in-cluster",
				ManagedCloud:      true,
			},
		},
	}

	status, err := ResolveStoredEnvironmentIdleStatus(store, "tenant", "cloud", now)
	if err != nil {
		t.Fatalf("ResolveStoredEnvironmentIdleStatus failed: %v", err)
	}
	if !status.ManagedCloud {
		t.Fatalf("expected env with ManagedCloud=true to be detected as managed cloud even when Remote=false (chart-deployed pods set Remote=false)")
	}
	if !status.StopEligible {
		t.Fatalf("expected outside-working-hours managed cloud env to be stop eligible: %s", status.StopBlockedReason)
	}
}

func TestResolveStoredEnvironmentIdleStatusIncludesStopError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	logDir := filepath.Join(home, ".erun", "tenant", "cloud")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "idle-stop.log"), []byte("failed to stop instance: access denied\n"), 0o644); err != nil {
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

func TestRecordEnvironmentActivityMergesClientUpdates(t *testing.T) {
	// SSH-proxy callers record one or more per-IP byte deltas per save.
	// RecordEnvironmentActivity must accumulate Bytes per client across
	// saves and stamp LastActivity on whichever clients contributed bytes
	// this round, leaving idle peers alone.
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	xdg.Reload()

	first := time.Date(2026, 5, 18, 19, 0, 0, 0, time.UTC)
	if err := RecordEnvironmentActivity(EnvironmentActivityParams{
		Tenant:      "tenant-a",
		Environment: "dev",
		Kind:        ActivityKindSSH,
		Bytes:       2048,
		ClientUpdates: []EnvironmentActivityClientUpdate{
			{Address: "10.0.4.7", Bytes: 1500},
			{Address: "127.0.0.1", Bytes: 548},
		},
		Now: first,
	}); err != nil {
		t.Fatalf("first RecordEnvironmentActivity failed: %v", err)
	}

	second := first.Add(5 * time.Second)
	if err := RecordEnvironmentActivity(EnvironmentActivityParams{
		Tenant:      "tenant-a",
		Environment: "dev",
		Kind:        ActivityKindSSH,
		Bytes:       512,
		ClientUpdates: []EnvironmentActivityClientUpdate{
			{Address: "10.0.4.7", Bytes: 512},
		},
		Now: second,
	}); err != nil {
		t.Fatalf("second RecordEnvironmentActivity failed: %v", err)
	}

	activity, err := LoadEnvironmentActivity("tenant-a", "dev")
	if err != nil {
		t.Fatalf("LoadEnvironmentActivity failed: %v", err)
	}
	snapshot, ok := activity[ActivityKindSSH]
	if !ok {
		t.Fatalf("expected ssh snapshot, got %+v", activity)
	}
	if snapshot.Bytes != 2560 {
		t.Fatalf("expected total Bytes=2560, got %d", snapshot.Bytes)
	}
	if got := len(snapshot.Clients); got != 2 {
		t.Fatalf("expected 2 client entries, got %d", got)
	}
	primary := snapshot.Clients["10.0.4.7"]
	if primary.Bytes != 2012 {
		t.Fatalf("primary client bytes = %d, want 2012", primary.Bytes)
	}
	if !primary.LastActivity.Equal(second) {
		t.Fatalf("primary client LastActivity = %v, want %v", primary.LastActivity, second)
	}
	secondary := snapshot.Clients["127.0.0.1"]
	if secondary.Bytes != 548 {
		t.Fatalf("secondary client bytes = %d, want 548", secondary.Bytes)
	}
	if !secondary.LastActivity.Equal(first) {
		t.Fatalf("idle client LastActivity must not advance: got %v, want %v", secondary.LastActivity, first)
	}
}

func TestRecordEnvironmentActivityEvictsOldestClientOverCap(t *testing.T) {
	// Long-lived runtimes can see many ephemeral peers (NAT'd source
	// ports, transient port-forwards). The snapshot is bounded by
	// environmentActivityClientCap; the oldest LastActivity is dropped
	// once the cap would be exceeded so ssh.json cannot grow unbounded.
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	xdg.Reload()

	base := time.Date(2026, 5, 18, 19, 0, 0, 0, time.UTC)
	for i := 0; i < environmentActivityClientCap+3; i++ {
		address := fmt.Sprintf("10.0.0.%d", i+1)
		if err := RecordEnvironmentActivity(EnvironmentActivityParams{
			Tenant:        "tenant-a",
			Environment:   "dev",
			Kind:          ActivityKindSSH,
			Bytes:         128,
			ClientUpdates: []EnvironmentActivityClientUpdate{{Address: address, Bytes: 128}},
			Now:           base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("RecordEnvironmentActivity[%d] failed: %v", i, err)
		}
	}

	activity, err := LoadEnvironmentActivity("tenant-a", "dev")
	if err != nil {
		t.Fatalf("LoadEnvironmentActivity failed: %v", err)
	}
	snapshot := activity[ActivityKindSSH]
	if got := len(snapshot.Clients); got != environmentActivityClientCap {
		t.Fatalf("expected snapshot to be capped at %d clients, got %d", environmentActivityClientCap, got)
	}
	// The first three IPs are the oldest and must be evicted; the
	// remaining cap entries are the most recent.
	for i := 0; i < 3; i++ {
		address := fmt.Sprintf("10.0.0.%d", i+1)
		if _, present := snapshot.Clients[address]; present {
			t.Fatalf("expected %s to be evicted, snapshot=%+v", address, snapshot.Clients)
		}
	}
	for i := 3; i < environmentActivityClientCap+3; i++ {
		address := fmt.Sprintf("10.0.0.%d", i+1)
		if _, present := snapshot.Clients[address]; !present {
			t.Fatalf("expected %s to remain, snapshot=%+v", address, snapshot.Clients)
		}
	}
}

func TestActivityIdleMarkerProjectsClientsSortedByRecency(t *testing.T) {
	now := time.Date(2026, 5, 18, 19, 0, 0, 0, time.UTC)
	snapshot := EnvironmentActivitySnapshot{
		LastActivity: now,
		LastSeen:     now,
		Bytes:        4096,
		Clients: map[string]EnvironmentActivityClient{
			"10.0.4.7":  {Bytes: 2500, LastActivity: now.Add(-2 * time.Second)},
			"10.0.4.8":  {Bytes: 600, LastActivity: now.Add(-30 * time.Second)},
			"127.0.0.1": {Bytes: 996, LastActivity: now},
		},
	}
	policy := EnvironmentIdlePolicy{Timeout: 5 * time.Minute}
	marker := activityIdleMarker(ActivityKindSSH, snapshot, policy, now)
	if len(marker.Clients) != 3 {
		t.Fatalf("expected 3 client entries on marker, got %+v", marker.Clients)
	}
	// Sorted by LastActivity descending: localhost (now), 10.0.4.7 (2s
	// ago), 10.0.4.8 (30s ago).
	wantOrder := []string{"127.0.0.1", "10.0.4.7", "10.0.4.8"}
	for i, want := range wantOrder {
		if marker.Clients[i].Address != want {
			t.Fatalf("marker.Clients[%d] = %s, want %s", i, marker.Clients[i].Address, want)
		}
	}
	if marker.Clients[1].SecondsAgo != 2 {
		t.Fatalf("expected SecondsAgo=2 for 10.0.4.7, got %d", marker.Clients[1].SecondsAgo)
	}
	if marker.Clients[2].SecondsAgo != 30 {
		t.Fatalf("expected SecondsAgo=30 for 10.0.4.8, got %d", marker.Clients[2].SecondsAgo)
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
