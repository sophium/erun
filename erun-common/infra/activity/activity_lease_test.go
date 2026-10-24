package activity

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrg/xdg"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

// Injected clocks and liveness: a real dead pid is racy and recyclable.

func isolateActivityCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}

func alwaysAlive(int) bool { return true }
func neverAlive(int) bool  { return false }

func expectReleaseOutcome(t *testing.T, context string, outcome model.EnvironmentActivityLeaseReleaseOutcome, err error, want model.EnvironmentActivityLeaseReleaseOutcome) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", context, err)
	}
	if outcome != want {
		t.Errorf("%s: expected outcome %v, got %v", context, want, outcome)
	}
}

// Resolving the activity directory must not create it (dry-run purity).
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

	outcome, err := ReleaseEnvironmentActivityLease("team", "dev", "gradle-build")
	expectReleaseOutcome(t, "release", outcome, err, model.EnvironmentActivityLeaseReleased)
	held, err = loadEnvironmentActivityLeases("team", "dev", now.Add(time.Minute), alwaysAlive)
	if err != nil {
		t.Fatalf("load after release: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("expected no leases after release, got %+v", held)
	}
}

func TestFormatLeaseHoldersCollapsesSameHolderAcrossLeases(t *testing.T) {
	sameHolder := model.EnvironmentActivityLeaseHolder{Orchestrator: "erun-devops", Tenant: "erun"}
	leases := []model.EnvironmentActivityLease{
		{Name: "release erun 1.0.248", Holder: sameHolder},
		{Name: "release 1.0.248", Holder: sameHolder},
	}
	got := FormatLeaseHolders(leases)
	want := `orchestrator erun-devops, tenant erun (leases "release erun 1.0.248", "release 1.0.248")`
	if got != want {
		t.Errorf("FormatLeaseHolders() = %q, want %q", got, want)
	}
}

func TestFormatLeaseHoldersKeepsDistinctHoldersSeparate(t *testing.T) {
	leases := []model.EnvironmentActivityLease{
		{Name: "gradle-build", Holder: model.EnvironmentActivityLeaseHolder{Orchestrator: "eng-42"}},
		{Name: "agent-run"},
	}
	got := FormatLeaseHolders(leases)
	want := `orchestrator eng-42 (lease "gradle-build"); an unnamed holder (lease "agent-run")`
	if got != want {
		t.Errorf("FormatLeaseHolders() = %q, want %q", got, want)
	}
}

func TestActivityLeaseReleaseIsIdempotent(t *testing.T) {
	// Releasing a never-taken lease succeeds but reports NotHeld.
	isolateActivityCache(t)
	outcome, err := ReleaseEnvironmentActivityLease("team", "dev", "gradle-build")
	if err != nil {
		t.Errorf("releasing an absent lease must succeed, got %v", err)
	}
	if outcome != model.EnvironmentActivityLeaseNotHeld {
		t.Errorf("expected releasing an absent lease to report NotHeld, got %v", outcome)
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
	ceiling := start.Add(EnvironmentActivityLeaseMaxLifetime)
	var renewed model.EnvironmentActivityLease
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

	held, err := loadEnvironmentActivityLeases("team", "dev", now.Add(time.Minute), alwaysAlive)
	if err != nil {
		t.Fatalf("load with live holder: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("expected a live holder to keep the lease, got %+v", held)
	}

	held, err = loadEnvironmentActivityLeases("team", "dev", now.Add(time.Minute), neverAlive)
	if err != nil {
		t.Fatalf("load with dead holder: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("expected an orphaned lease reclaimed, got %+v", held)
	}
}

func TestExclusiveLeaseRefusesASecondHolderInTheSameScope(t *testing.T) {
	isolateActivityCache(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	first, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "job-fix-1201", ID: "job-fix-1201",
		Exclusive: true, Holder: model.EnvironmentActivityLeaseHolder{Orchestrator: "petios"}, Now: now,
	})
	if err != nil {
		t.Fatalf("first take: %v", err)
	}
	if first.Scope != "worktree" {
		t.Errorf("expected the default scope to be worktree, got %q", first.Scope)
	}

	_, err = TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "job-fix-1245", ID: "job-fix-1245",
		Exclusive: true, Holder: model.EnvironmentActivityLeaseHolder{Orchestrator: "erun"}, Now: now.Add(time.Second),
	})
	var conflict *EnvironmentActivityLeaseConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a conflict error, got %v", err)
	}
	if conflict.Holder.ID != "job-fix-1201" || conflict.Holder.Holder.Orchestrator != "petios" {
		t.Fatalf("refusal must name the actual holder, got %+v", conflict.Holder)
	}
}

func TestExclusiveLeaseAllowsDifferentScopesToCoexist(t *testing.T) {
	// Two clones in one pod: exclusivity is scoped, not environment-wide.
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
	// Only an O_EXCL create passes this; create-or-overwrite cannot.
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
	// A remote holder has no PID to probe, so an expired TTL is the only reclaim path.
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

	// A non-holder releasing by scope must be refused with the holder named.
	_, err := ReleaseExclusiveEnvironmentActivityLease("erun", "ux", "worktree", "somebody-else")
	expectHolderNamedInConflict(t, err, "job-fix-1201")
	requireHeldCount(t, "erun", "ux", now, 1, "expected the real holder's claim to survive a mismatched release")

	outcome, err := ReleaseExclusiveEnvironmentActivityLease("erun", "ux", "worktree", "job-fix-1201")
	expectReleaseOutcome(t, "release", outcome, err, model.EnvironmentActivityLeaseReleased)
	requireHeldCount(t, "erun", "ux", now, 0, "expected the scope free after its own holder released it")

	outcome, err = ReleaseExclusiveEnvironmentActivityLease("erun", "ux", "worktree", "job-fix-1201")
	expectReleaseOutcome(t, "release of a vacated scope", outcome, err, model.EnvironmentActivityLeaseNotHeld)
}

func expectHolderNamedInConflict(t *testing.T, err error, wantHolderID string) {
	t.Helper()
	var conflict *EnvironmentActivityLeaseConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a mismatched release to fail naming the holder, got %v", err)
	}
	if conflict.Holder.ID != wantHolderID {
		t.Errorf("expected the conflict to name the real holder, got %+v", conflict.Holder)
	}
}

func requireHeldCount(t *testing.T, tenant, environment string, now time.Time, want int, context string) {
	t.Helper()
	held, err := loadEnvironmentActivityLeases(tenant, environment, now.Add(time.Minute), alwaysAlive)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(held) != want {
		t.Fatalf("%s, got %+v", context, held)
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

	// Reads see the exclusive claim without being refused by it.
	held, err := LoadEnvironmentActivityLeases("erun", "ux", now.Add(time.Minute))
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
	for _, kind := range []string{ActivityKindProcess, configpkg.ActivityKindCodex, configpkg.ActivityKindMCP, configpkg.ActivityKindCLI, configpkg.ActivityKindAPI} {
		status := model.EnvironmentIdleStatus{Markers: []model.EnvironmentIdleMarker{busyMarker(kind), idleMarker(configpkg.ActivityKindSSH)}}
		if _, present := EnvironmentOperatorPresenceReason(status); present {
			t.Errorf("marker %q must not by itself report an operator present", kind)
		}
	}
}

func TestLeaseReleaseNamesTheStoreHoldingIt(t *testing.T) {
	isolateActivityCache(t)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

	// An exclusive claim released without --exclusive stays held.
	requireTakeLease(t, "job-fix-1301", now, true)
	outcome, err := ReleaseEnvironmentActivityLease("erun", "ux", "job-fix-1301")
	expectReleaseOutcome(t, "shared release of an exclusive claim", outcome, err, model.EnvironmentActivityLeaseHeldElsewhere)
	requireHeldCount(t, "erun", "ux", now, 1, "expected the exclusive claim to survive a release aimed at the shared store")
	requireNoteNames(t, "job-fix-1301", false, "exclusive", "--exclusive")

	if _, err := ReleaseExclusiveEnvironmentActivityLease("erun", "ux", "worktree", "job-fix-1301"); err != nil {
		t.Fatalf("release exclusive: %v", err)
	}
	requireHeldCount(t, "erun", "ux", now, 0, "expected the claim gone once released in its own shape")

	requireTakeLease(t, "job-fix-1302", now, false)
	outcome, err = ReleaseExclusiveEnvironmentActivityLease("erun", "ux", "worktree", "job-fix-1302")
	expectReleaseOutcome(t, "exclusive release of a shared lease", outcome, err, model.EnvironmentActivityLeaseHeldElsewhere)
	requireHeldCount(t, "erun", "ux", now, 1, "expected the shared lease to survive a release aimed at the exclusive store")
	requireNoteNames(t, "job-fix-1302", true, "shared", "without --exclusive")

	outcome, err = ReleaseEnvironmentActivityLease("erun", "ux", "job-fix-1303")
	expectReleaseOutcome(t, "release of an unheld id", outcome, err, model.EnvironmentActivityLeaseNotHeld)
	requireNoNote(t, "job-fix-1303", false)
}

func requireTakeLease(t *testing.T, id string, now time.Time, exclusive bool) {
	t.Helper()
	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: id, ID: id, Exclusive: exclusive, Now: now,
	}); err != nil {
		t.Fatalf("take %s (exclusive=%v): %v", id, exclusive, err)
	}
}

func requireNoteNames(t *testing.T, id string, releasedExclusive bool, wants ...string) {
	t.Helper()
	note, err := EnvironmentActivityLeaseHeldElsewhereNote("erun", "ux", id, releasedExclusive)
	if err != nil {
		t.Fatalf("note for %s: %v", id, err)
	}
	for _, want := range wants {
		if !strings.Contains(note, want) {
			t.Errorf("expected the note for %s to name %q, got %q", id, want, note)
		}
	}
}

func requireNoNote(t *testing.T, id string, releasedExclusive bool) {
	t.Helper()
	note, err := EnvironmentActivityLeaseHeldElsewhereNote("erun", "ux", id, releasedExclusive)
	if err != nil {
		t.Fatalf("note for %s: %v", id, err)
	}
	if note != "" {
		t.Errorf("expected no note for an id held nowhere, got %q", note)
	}
}
