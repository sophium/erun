package activity

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvironmentActivityLeaseReleaseOutcome reports what a release did; every outcome is
// success so a repeated release never fails a finished job.
type EnvironmentActivityLeaseReleaseOutcome int

const (
	// EnvironmentActivityLeaseReleased means this call removed a held claim.
	EnvironmentActivityLeaseReleased EnvironmentActivityLeaseReleaseOutcome = iota
	// EnvironmentActivityLeaseNotHeld means nothing was held: never taken, already released, or reclaimed.
	EnvironmentActivityLeaseNotHeld
	// EnvironmentActivityLeaseHeldElsewhere: the id holds a lease of the other
	// shape (shared vs exclusive), which stays held.
	EnvironmentActivityLeaseHeldElsewhere
)

// ReleaseEnvironmentActivityLease drops a shared lease; it is idempotent.
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
			// An exclusive claim released without --exclusive lands here; say so rather than
			// report a no-match while it stays held.
			if held, heldErr := environmentActivityLeaseHeldAsExclusiveClaim(tenant, environment, resolved); heldErr == nil && held {
				return EnvironmentActivityLeaseHeldElsewhere, nil
			}
			return EnvironmentActivityLeaseNotHeld, nil
		}
		return EnvironmentActivityLeaseNotHeld, err
	}
	return EnvironmentActivityLeaseReleased, nil
}

// Exclusive claims are keyed by scope, so scan the directory for this holder.
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

// EnvironmentActivityLeaseHeldElsewhereNote names the release that would match, or ""
// when the id is not held under the other shape.
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

// ReleaseExclusiveEnvironmentActivityLease removes the claim only when this id holds it,
// so a stale release cannot drop another holder's claim.
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
			// Reverse case: a shared lease released with --exclusive.
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
