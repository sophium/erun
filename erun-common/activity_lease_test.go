package eruncommon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestEnvironmentActivityDirDoesNotCreateDirectory pins the same dry-run
// purity contract erun#1907 fixed for the config tree: resolving the
// activity directory is a pure read (job_supervisor.go's environmentJobDir
// and RunLocalEnvironmentWhip both resolve it ahead of their own dry-run
// checks) and must not create ~/.cache/erun/activity/<tenant>/<environment>/
// as a side effect.
func TestEnvironmentActivityDirDoesNotCreateDirectory(t *testing.T) {
	isolateActivityCache(t)
	dir, err := EnvironmentActivityDir("acme", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("resolving the activity directory must not create it, got err=%v", statErr)
	}
}

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

// erun#1245: two orchestrators, or two agent jobs, driving the same worktree
// with no shared visibility. A plain lease is presence, not exclusion, so the
// tests below cover the exclusive mode that actually refuses a second holder.

func TestExclusiveLeaseRefusesASecondHolderInTheSameScope(t *testing.T) {
	isolateActivityCache(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	first, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "job-fix-1201", ID: "job-fix-1201",
		Exclusive: true, Holder: EnvironmentActivityLeaseHolder{Orchestrator: "petios"}, Now: now,
	})
	if err != nil {
		t.Fatalf("first take: %v", err)
	}
	if first.Scope != "worktree" {
		t.Errorf("expected the default scope to be worktree, got %q", first.Scope)
	}

	_, err = TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "job-fix-1245", ID: "job-fix-1245",
		Exclusive: true, Holder: EnvironmentActivityLeaseHolder{Orchestrator: "erun"}, Now: now.Add(time.Second),
	})
	var conflict *EnvironmentActivityLeaseConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a conflict error, got %v", err)
	}
	if conflict.Holder.ID != "job-fix-1201" || conflict.Holder.Holder.Orchestrator != "petios" {
		// The Validation section of #1245 is explicit: assert on the identity
		// carried in the error, not merely that the call failed.
		t.Fatalf("refusal must name the actual holder, got %+v", conflict.Holder)
	}
}

func TestExclusiveLeaseAllowsDifferentScopesToCoexist(t *testing.T) {
	// Two clones of the same repo in one pod: legitimate parallelism, not a
	// collision. Exclusivity must be scoped, never environment-wide.
	isolateActivityCache(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "pod", Name: "clone-a-job", ID: "clone-a-job",
		Scope: "/home/erun/work/clone-a", Exclusive: true, Now: now,
	}); err != nil {
		t.Fatalf("take scope a: %v", err)
	}
	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "pod", Name: "clone-b-job", ID: "clone-b-job",
		Scope: "/home/erun/work/clone-b", Exclusive: true, Now: now,
	}); err != nil {
		t.Fatalf("expected a different scope to succeed without conflict, got %v", err)
	}

	held, err := loadEnvironmentActivityLeases("erun", "pod", now.Add(time.Minute), alwaysAlive)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(held) != 2 {
		t.Fatalf("expected both scoped claims held, got %+v", held)
	}
}

func TestExclusiveLeaseRenewalBySameHolderSucceeds(t *testing.T) {
	isolateActivityCache(t)
	start := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "job-fix-1201", ID: "job-fix-1201",
		Exclusive: true, TTL: 5 * time.Minute, Now: start,
	}); err != nil {
		t.Fatalf("take: %v", err)
	}

	renewal, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "job-fix-1201", ID: "job-fix-1201",
		Exclusive: true, TTL: 5 * time.Minute, Now: start.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("expected the same holder's renewal to succeed, got %v", err)
	}
	if !renewal.StartedAt.Equal(start) {
		t.Errorf("renewal must keep the original start, got %s", renewal.StartedAt)
	}
	if !renewal.ExpiresAt.Equal(start.Add(time.Minute).Add(5 * time.Minute)) {
		t.Errorf("renewal must extend the expiry from now, got %s", renewal.ExpiresAt)
	}
}

func TestExclusiveLeaseTakeRaceExactlyOneWins(t *testing.T) {
	// The test #1245 calls out explicitly: two concurrent exclusive takes in
	// the same scope, exactly one succeeds. Plain os.WriteFile (create-or-
	// overwrite) cannot pass this; only the O_EXCL create can.
	isolateActivityCache(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	const contenders = 8
	var wg sync.WaitGroup
	successes := make([]bool, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "job-" + string(rune('a'+i))
			_, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
				Tenant: "erun", Environment: "race", Name: id, ID: id,
				Exclusive: true, Now: now,
			})
			successes[i] = err == nil
		}(i)
	}
	wg.Wait()

	won := 0
	for _, ok := range successes {
		if ok {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("expected exactly one of %d concurrent claimants to win, got %d", contenders, won)
	}

	held, err := loadEnvironmentActivityLeases("erun", "race", now.Add(time.Minute), alwaysAlive)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("expected exactly one holder on disk, got %+v", held)
	}
}

func TestExclusiveLeaseReclaimsALapsedRemoteHolder(t *testing.T) {
	// No PID to probe for a remote holder (an orchestrator driving over MCP
	// from another host), so an expired TTL is the only reclaim path.
	isolateActivityCache(t)
	start := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "stale-job", ID: "stale-job",
		Exclusive: true, TTL: time.Minute, Now: start,
	}); err != nil {
		t.Fatalf("take: %v", err)
	}

	after := start.Add(5 * time.Minute)
	next, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "fresh-job", ID: "fresh-job",
		Exclusive: true, TTL: time.Minute, Now: after,
	})
	if err != nil {
		t.Fatalf("expected the lapsed holder to be reclaimed, got %v", err)
	}
	if next.ID != "fresh-job" {
		t.Errorf("expected the new holder to win the scope, got %+v", next)
	}
}

func TestExclusiveLeaseReclaimsADeadLocalHolderOnRead(t *testing.T) {
	// A real dead pid is racy and recyclable (see the file header comment on
	// TestActivityLeaseOrphanedByADeadHolderIsReclaimed), so this drives the
	// same read-path reclaim through the injectable liveness function rather
	// than a real pid.
	isolateActivityCache(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "detached-job", ID: "detached-job",
		PID: 4242, Exclusive: true, TTL: time.Hour, Now: now,
	}); err != nil {
		t.Fatalf("take: %v", err)
	}

	held, err := loadEnvironmentActivityLeases("erun", "ux", now.Add(time.Minute), alwaysAlive)
	if err != nil {
		t.Fatalf("load with live holder: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("expected the live holder's exclusive claim held, got %+v", held)
	}

	held, err = loadEnvironmentActivityLeases("erun", "ux", now.Add(time.Minute), neverAlive)
	if err != nil {
		t.Fatalf("load with dead holder: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("expected the orphaned exclusive claim reclaimed, got %+v", held)
	}
}

func TestExclusiveLeaseReleaseOnlyDropsItsOwnClaim(t *testing.T) {
	isolateActivityCache(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "job-fix-1201", ID: "job-fix-1201",
		Exclusive: true, Now: now,
	}); err != nil {
		t.Fatalf("take: %v", err)
	}

	// A caller that never held the scope releasing by scope name alone must
	// not be able to drop the real holder's claim out from under them.
	if err := ReleaseExclusiveEnvironmentActivityLease("erun", "ux", "worktree", "somebody-else"); err != nil {
		t.Fatalf("mismatched release must be a no-op, not an error: %v", err)
	}
	held, err := loadEnvironmentActivityLeases("erun", "ux", now.Add(time.Minute), alwaysAlive)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("expected the real holder's claim to survive a mismatched release, got %+v", held)
	}

	if err := ReleaseExclusiveEnvironmentActivityLease("erun", "ux", "worktree", "job-fix-1201"); err != nil {
		t.Fatalf("release: %v", err)
	}
	held, err = loadEnvironmentActivityLeases("erun", "ux", now.Add(time.Minute), alwaysAlive)
	if err != nil {
		t.Fatalf("load after release: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("expected the scope free after its own holder released it, got %+v", held)
	}

	// Releasing an already-vacated scope is success, matching the shared
	// lease's idempotence.
	if err := ReleaseExclusiveEnvironmentActivityLease("erun", "ux", "worktree", "job-fix-1201"); err != nil {
		t.Fatalf("expected releasing a vacated scope to succeed, got %v", err)
	}
}

func TestReadOnlyCallersAreNeverBlockedByAHeldExclusiveLease(t *testing.T) {
	isolateActivityCache(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "job-fix-1201", ID: "job-fix-1201",
		Exclusive: true, Now: now,
	}); err != nil {
		t.Fatalf("take: %v", err)
	}

	// observe/diff/usage-shaped reads go through exactly this path (leases,
	// then idle status) and must see the exclusive claim without being
	// refused by it - only a second *exclusive take* can be refused.
	held, err := LoadEnvironmentActivityLeases("erun", "ux", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("read-only list must not be blocked, got %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("expected the exclusive claim visible to a read-only caller, got %+v", held)
	}

	status, err := ResolveEnvironmentIdleStatus(EnvironmentIdleConfig{WorkingHours: "00:00-23:59"}, nil, held, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("resolve idle status: %v", err)
	}
	if status.StopEligible {
		t.Fatal("a held exclusive lease must still defer idle-stop like any other lease")
	}
}

func TestEnvironmentOperatorPresenceReason(t *testing.T) {
	idleMarker := func(name string) EnvironmentIdleMarker { return EnvironmentIdleMarker{Name: name, Idle: true} }
	busyMarker := func(name string) EnvironmentIdleMarker { return EnvironmentIdleMarker{Name: name, Idle: false} }

	if _, present := EnvironmentOperatorPresenceReason(EnvironmentIdleStatus{}); present {
		t.Error("no markers at all must not report an operator present")
	}

	sshBusy := EnvironmentIdleStatus{Markers: []EnvironmentIdleMarker{busyMarker(ActivityKindSSH)}}
	reason, present := EnvironmentOperatorPresenceReason(sshBusy)
	if !present || reason == "" {
		t.Fatalf("a busy ssh marker must report an operator present, got present=%v reason=%q", present, reason)
	}

	// Deliberately narrower than a literal reading of #1245: "process" and
	// "codex" also fire for an orchestrator's own detached job (job_agent.go
	// execs the same wrapped claude/codex binaries the interactive session
	// uses), so gating on them would make an orchestrator refuse itself and
	// would make a second legitimate job in a different clone of the same pod
	// refuse against the first job's resident process. Only ssh is
	// unambiguous. See the comment on environmentOperatorPresenceMarkers.
	for _, kind := range []string{ActivityKindProcess, ActivityKindCodex, ActivityKindMCP, ActivityKindCLI, ActivityKindAPI} {
		status := EnvironmentIdleStatus{Markers: []EnvironmentIdleMarker{busyMarker(kind), idleMarker(ActivityKindSSH)}}
		if _, present := EnvironmentOperatorPresenceReason(status); present {
			t.Errorf("marker %q must not by itself report an operator present", kind)
		}
	}
}
