package idlestatus

import (
	"testing"
	"time"

	"github.com/adrg/xdg"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
	"github.com/sophium/erun/erun-common/infra/activity/records"
)

// Injected clocks and liveness: a real dead pid is racy and recyclable.

func isolateActivityCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}

func TestReadOnlyCallersAreNeverBlockedByAHeldExclusiveLease(t *testing.T) {
	isolateActivityCache(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	if _, err := activitylease.TakeEnvironmentActivityLease(activitylease.TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "job-fix-1201", ID: "job-fix-1201",
		Exclusive: true, Now: now,
	}); err != nil {
		t.Fatalf("take: %v", err)
	}

	// Reads see the exclusive claim without being refused by it.
	held, err := activitylease.LoadEnvironmentActivityLeases("erun", "ux", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("read-only list must not be blocked, got %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("expected the exclusive claim visible to a read-only caller, got %+v", held)
	}

	status, err := ResolveEnvironmentIdleStatus(configpkg.EnvironmentIdleConfig{WorkingHours: "00:00-23:59"}, nil, held, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("resolve idle status: %v", err)
	}
	if status.StopEligible {
		t.Fatal("a held exclusive lease must still defer idle-stop like any other lease")
	}
}

func TestEnvironmentOperatorPresenceReason(t *testing.T) {
	idleMarker := func(name string) model.EnvironmentIdleMarker {
		return model.EnvironmentIdleMarker{Name: name, Idle: true}
	}
	busyMarker := func(name string) model.EnvironmentIdleMarker {
		return model.EnvironmentIdleMarker{Name: name, Idle: false}
	}

	if _, present := EnvironmentOperatorPresenceReason(model.EnvironmentIdleStatus{}); present {
		t.Error("no markers at all must not report an operator present")
	}

	sshBusy := model.EnvironmentIdleStatus{Markers: []model.EnvironmentIdleMarker{busyMarker(configpkg.ActivityKindSSH)}}
	reason, present := EnvironmentOperatorPresenceReason(sshBusy)
	if !present || reason == "" {
		t.Fatalf("a busy ssh marker must report an operator present, got present=%v reason=%q", present, reason)
	}

	// "process" and "codex" also fire for orchestrator jobs; only ssh means an operator.
	for _, kind := range []string{records.ActivityKindProcess, configpkg.ActivityKindCodex, configpkg.ActivityKindMCP, configpkg.ActivityKindCLI, configpkg.ActivityKindAPI} {
		status := model.EnvironmentIdleStatus{Markers: []model.EnvironmentIdleMarker{busyMarker(kind), idleMarker(configpkg.ActivityKindSSH)}}
		if _, present := EnvironmentOperatorPresenceReason(status); present {
			t.Errorf("marker %q must not by itself report an operator present", kind)
		}
	}
}
