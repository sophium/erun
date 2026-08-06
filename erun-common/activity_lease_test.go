package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrg/xdg"
)

// These cover what the CLI subprocess cannot reach deterministically: expiry and
// the hard lifetime ceiling need an injected clock, and orphan reconciliation
// needs an injected liveness answer (a real dead pid is racy and recyclable).
// The lease commands' own surface is locked by the integration scenarios.

// isolateActivityCache points the activity tree at a temp dir for one test.
func isolateActivityCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}

func alwaysAlive(int) bool { return true }
func neverAlive(int) bool  { return false }

func TestActivityLeaseHoldsUntilReleased(t *testing.T) {
	isolateActivityCache(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	lease, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "team", Environment: "dev", Name: "gradle-build", TTL: 10 * time.Minute, Now: now,
	})
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if lease.ID != "gradle-build" {
		t.Errorf("expected the id to default to the name, got %q", lease.ID)
	}
	if !lease.ExpiresAt.Equal(now.Add(10 * time.Minute)) {
		t.Errorf("expected the ttl to set the expiry, got %s", lease.ExpiresAt)
	}

	held, err := loadEnvironmentActivityLeases("team", "dev", now.Add(time.Minute), alwaysAlive)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(held) != 1 || held[0].Name != "gradle-build" {
		t.Fatalf("expected the lease to be held, got %+v", held)
	}

	if err := ReleaseEnvironmentActivityLease("team", "dev", "gradle-build"); err != nil {
		t.Fatalf("release: %v", err)
	}
	held, err = loadEnvironmentActivityLeases("team", "dev", now.Add(time.Minute), alwaysAlive)
	if err != nil {
		t.Fatalf("load after release: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("expected no leases after release, got %+v", held)
	}
}

func TestActivityLeaseReleaseIsIdempotent(t *testing.T) {
	// A wrapper's exit trap must not fail a job that already finished, so
	// releasing a lease that was never taken is success.
	isolateActivityCache(t)
	if err := ReleaseEnvironmentActivityLease("team", "dev", "gradle-build"); err != nil {
		t.Errorf("releasing an absent lease must succeed, got %v", err)
	}
}

func TestActivityLeaseExpiresAndIsReclaimed(t *testing.T) {
	isolateActivityCache(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "team", Environment: "dev", Name: "agent-run", TTL: 5 * time.Minute, Now: now,
	}); err != nil {
		t.Fatalf("take: %v", err)
	}

	held, err := loadEnvironmentActivityLeases("team", "dev", now.Add(5*time.Minute), alwaysAlive)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("expected the lease to have expired, got %+v", held)
	}
	// Reclaimed on disk too, so an expired claim cannot resurface.
	dir, err := environmentActivityLeaseDir("team", "dev")
	if err != nil {
		t.Fatalf("lease dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent-run.json")); !os.IsNotExist(err) {
		t.Errorf("expected the expired lease file removed, stat err %v", err)
	}
}

func TestActivityLeaseRenewalCannotOutrunTheLifetimeCeiling(t *testing.T) {
	isolateActivityCache(t)
	start := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "team", Environment: "dev", Name: "runaway", TTL: time.Hour, Now: start,
	}); err != nil {
		t.Fatalf("take: %v", err)
	}
	// A wrapper looping on a hung job renews before its ttl lapses, forever; the
	// ceiling is what stops that pinning the environment awake.
	ceiling := start.Add(EnvironmentActivityLeaseMaxLifetime)
	var renewed EnvironmentActivityLease
	for at := start.Add(30 * time.Minute); at.Before(ceiling); at = at.Add(30 * time.Minute) {
		lease, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
			Tenant: "team", Environment: "dev", Name: "runaway", TTL: time.Hour, Now: at,
		})
		if err != nil {
			t.Fatalf("renew at %s: %v", at, err)
		}
		renewed = lease
	}
	if renewed.ExpiresAt.After(ceiling) {
		t.Errorf("renewal pushed the expiry past the ceiling: %s > %s", renewed.ExpiresAt, ceiling)
	}
	if !renewed.StartedAt.Equal(start) {
		t.Errorf("renewal must keep the original start, got %s", renewed.StartedAt)
	}

	held, err := loadEnvironmentActivityLeases("team", "dev", ceiling, alwaysAlive)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("expected the lease dropped at the ceiling, got %+v", held)
	}
}

func TestActivityLeaseOrphanedByADeadHolderIsReclaimed(t *testing.T) {
	isolateActivityCache(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "team", Environment: "dev", Name: "detached-run", PID: 4242,
		TTL: time.Hour, Now: now,
	}); err != nil {
		t.Fatalf("take: %v", err)
	}

	// Holder alive: the lease holds for its whole TTL, which is the point.
	held, err := loadEnvironmentActivityLeases("team", "dev", now.Add(time.Minute), alwaysAlive)
	if err != nil {
		t.Fatalf("load with live holder: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("expected a live holder to keep the lease, got %+v", held)
	}

	// Holder gone: reclaimed immediately rather than waiting out the TTL.
	held, err = loadEnvironmentActivityLeases("team", "dev", now.Add(time.Minute), neverAlive)
	if err != nil {
		t.Fatalf("load with dead holder: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("expected an orphaned lease reclaimed, got %+v", held)
	}
}

func TestLeasedEnvironmentIsNotStopEligible(t *testing.T) {
	// This is AC6 of the stop work: idle-stop must not stop an environment that
	// is holding a live lease, and must say which lease is deferring it.
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	config := EnvironmentIdleConfig{Timeout: "5m", WorkingHours: "00:00-23:59"}

	idle, err := ResolveEnvironmentIdleStatus(config, nil, nil, now)
	if err != nil {
		t.Fatalf("resolve without leases: %v", err)
	}
	if !idle.StopEligible {
		t.Fatalf("an environment with no activity and no lease must be stop eligible, blocked by %q", idle.StopBlockedReason)
	}

	leased, err := ResolveEnvironmentIdleStatus(config, nil, []EnvironmentActivityLease{{
		ID: "agent-run", Name: "agent-run", StartedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}}, now)
	if err != nil {
		t.Fatalf("resolve with a lease: %v", err)
	}
	if leased.StopEligible {
		t.Fatal("a held lease must block idle-stop")
	}
	if !strings.Contains(leased.StopBlockedReason, "agent-run") {
		t.Errorf("the blocked reason must name the lease, got %q", leased.StopBlockedReason)
	}
	if len(leased.Leases) != 1 {
		t.Errorf("the status must carry the leases so a client can render them, got %+v", leased.Leases)
	}
}
