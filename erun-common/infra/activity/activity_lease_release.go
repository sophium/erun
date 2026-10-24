package activity

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvironmentActivityLeaseReleaseOutcome distinguishes what a release actually
// did, so a caller — and the report it prints — can tell "a held claim was
// removed" from "there was nothing there to remove" instead of both reading as
// the same bare success. Both are still success from the release call's own
// point of view: a wrapper's exit trap must never fail a job that already
// finished cleanly just because it releases twice.
type EnvironmentActivityLeaseReleaseOutcome int

const (
	// EnvironmentActivityLeaseReleased means a held claim existed under the
	// given id (and, for an exclusive claim, scope) and this call removed it.
	EnvironmentActivityLeaseReleased EnvironmentActivityLeaseReleaseOutcome = iota
	// EnvironmentActivityLeaseNotHeld means nothing was held under the given
	// id/scope: never taken, already released, or already expired and
	// reclaimed.
	EnvironmentActivityLeaseNotHeld
	// EnvironmentActivityLeaseHeldElsewhere means this id is holding a lease
	// on this environment, but under the other shape than the one this call
	// asked to release -- a shared lease released with --exclusive, or an
	// exclusive claim released without it. Nothing was removed and this is
	// still not an error, but the release was aimed at the wrong store: the
	// claim the caller meant to drop is untouched and stays held for its full
	// TTL. EnvironmentActivityLeaseHeldElsewhereNote names the release that
	// would match.
	EnvironmentActivityLeaseHeldElsewhere
)

// ReleaseEnvironmentActivityLease drops a shared (non-exclusive) lease.
// Idempotent: releasing a lease that already expired or was never taken is
// success, so a wrapper's exit trap never fails a job that already finished
// cleanly — but the returned outcome tells a caller whether anything was
// actually there to remove, rather than reporting the same success either way.
func ReleaseEnvironmentActivityLease(tenant, environment, id string) (EnvironmentActivityLeaseReleaseOutcome, error) {
	resolved, err := ResolveEnvironmentActivityLeaseID(id, id)
	if err != nil {
		return EnvironmentActivityLeaseNotHeld, err
	}
	dir, err := environmentActivityLeaseDir(tenant, environment)
	if err != nil {
		return EnvironmentActivityLeaseNotHeld, err
	}
	if err := os.Remove(filepath.Join(dir, resolved+".json")); err != nil {
		if os.IsNotExist(err) {
			// Nothing was held as a shared lease. Before calling that simply
			// "not held", check the other store: a caller who took an
			// exclusive claim and released it without --exclusive lands here,
			// and reporting a bare no-match would leave them believing the
			// claim was dropped while it stays held for its full TTL.
			if held, heldErr := environmentActivityLeaseHeldAsExclusiveClaim(tenant, environment, resolved); heldErr == nil && held {
				return EnvironmentActivityLeaseHeldElsewhere, nil
			}
			return EnvironmentActivityLeaseNotHeld, nil
		}
		return EnvironmentActivityLeaseNotHeld, err
	}
	return EnvironmentActivityLeaseReleased, nil
}

// environmentActivityLeaseHeldAsExclusiveClaim reports whether id is the
// recorded holder of an exclusive claim on any scope of this environment.
// Exclusive claims are keyed by scope rather than by holder id (see
// exclusiveEnvironmentActivityLeaseDir), so this reads that directory instead
// of probing a single path -- the scope is what the caller would have to name,
// and it is not recoverable from the id alone.
func environmentActivityLeaseHeldAsExclusiveClaim(tenant, environment, id string) (bool, error) {
	dir, err := exclusiveEnvironmentActivityLeaseDir(tenant, environment)
	if err != nil {
		return false, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		claim, err := loadEnvironmentActivityLease(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		if claim.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// EnvironmentActivityLeaseHeldElsewhereNote explains a release that matched
// nothing because the id is held under the other shape, and names the release
// that would match it. releasedExclusive is the shape the caller asked for, so
// the note describes the opposite one. It returns "" when the id is not held
// elsewhere after all, so a caller only ever prints a note it can stand
// behind rather than a guess about where the claim went.
func EnvironmentActivityLeaseHeldElsewhereNote(tenant, environment, id string, releasedExclusive bool) (string, error) {
	resolved, err := ResolveEnvironmentActivityLeaseID(id, id)
	if err != nil {
		return "", err
	}
	if releasedExclusive {
		dir, err := environmentActivityLeaseDir(tenant, environment)
		if err != nil {
			return "", err
		}
		if !environmentActivityLeaseFileExists(dir, resolved) {
			return "", nil
		}
		return "held as a shared lease; release it without --exclusive", nil
	}
	held, err := environmentActivityLeaseHeldAsExclusiveClaim(tenant, environment, resolved)
	if err != nil || !held {
		return "", err
	}
	return "held as an exclusive claim; release it with --exclusive and the --scope it was taken with", nil
}

func environmentActivityLeaseHeldAsShared(tenant, environment, id string) bool {
	dir, err := environmentActivityLeaseDir(tenant, environment)
	if err != nil {
		return false
	}
	return environmentActivityLeaseFileExists(dir, id)
}

func environmentActivityLeaseFileExists(dir, id string) bool {
	_, err := os.Stat(filepath.Join(dir, id+".json"))
	return err == nil
}

// ReleaseExclusiveEnvironmentActivityLease drops an exclusive claim on a
// scope. It only removes the file when the recorded holder is this same id —
// releasing by scope name alone, without proving you are the holder, could
// otherwise drop a different holder's exclusivity out from under them (a
// stale release call racing a new legitimate claim). An already-vacated scope
// is a success outcome, matching the shared release's idempotence, but a
// scope held by a *different* id is refused outright and names the actual
// holder (EnvironmentActivityLeaseConflictError) instead of quietly reporting
// success for a claim it never touched — the same shape a conflicting take
// already refuses with.
func ReleaseExclusiveEnvironmentActivityLease(tenant, environment, scope, id string) (EnvironmentActivityLeaseReleaseOutcome, error) {
	scope = NormalizeExclusiveEnvironmentActivityLeaseScope(scope)
	resolvedID, err := ResolveEnvironmentActivityLeaseID(id, id)
	if err != nil {
		return EnvironmentActivityLeaseNotHeld, err
	}
	path, err := exclusiveEnvironmentActivityLeasePath(tenant, environment, scope)
	if err != nil {
		return EnvironmentActivityLeaseNotHeld, err
	}
	existing, err := loadEnvironmentActivityLease(path)
	if err != nil {
		if os.IsNotExist(err) {
			// The scope holds nothing. This is the reverse of the shared
			// release's case: if this id is holding an ordinary shared lease,
			// the caller asked to drop it with --exclusive, and a bare
			// no-match would read as "already gone" while the lease stays
			// held.
			if environmentActivityLeaseHeldAsShared(tenant, environment, resolvedID) {
				return EnvironmentActivityLeaseHeldElsewhere, nil
			}
			return EnvironmentActivityLeaseNotHeld, nil
		}
		return EnvironmentActivityLeaseNotHeld, err
	}
	if existing.ID != resolvedID {
		return EnvironmentActivityLeaseNotHeld, &EnvironmentActivityLeaseConflictError{Scope: scope, Holder: existing}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return EnvironmentActivityLeaseNotHeld, err
	}
	return EnvironmentActivityLeaseReleased, nil
}
