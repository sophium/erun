package activity

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
)

// The concurrent reader runs the moment the exclusive claim first exists.
func TestAConcurrentReaderNeverSeesAHalfWrittenExclusiveClaim(t *testing.T) {
	isolateActivityCache(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

	var readerSaw []model.EnvironmentActivityLease
	previous := exclusiveLeaseClaimVisible
	exclusiveLeaseClaimVisible = func(path string) {
		held, err := collectHeldEnvironmentActivityLeases(filepath.Dir(path), now, alwaysAlive)
		if err != nil {
			t.Errorf("concurrent read: %v", err)
		}
		readerSaw = held
	}
	t.Cleanup(func() { exclusiveLeaseClaimVisible = previous })

	if _, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "job-first", ID: "job-first",
		Exclusive: true, Now: now,
	}); err != nil {
		t.Fatalf("first take: %v", err)
	}
	exclusiveLeaseClaimVisible = previous

	if len(readerSaw) != 1 || readerSaw[0].ID != "job-first" {
		t.Fatalf("a reader at the moment of publication saw %+v, want the complete claim", readerSaw)
	}
	_, err := TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant: "erun", Environment: "ux", Name: "job-second", ID: "job-second",
		Exclusive: true, Now: now.Add(time.Second),
	})
	var conflict *EnvironmentActivityLeaseConflictError
	if !errors.As(err, &conflict) || conflict.Holder.ID != "job-first" {
		t.Fatalf("second take = %v, want a conflict naming job-first: the first claim must survive the read", err)
	}
}
