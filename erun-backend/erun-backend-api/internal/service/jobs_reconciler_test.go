package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type fakeJobSweeper struct {
	ttl       time.Duration
	abandoned []model.Job
	err       error
}

func (f *fakeJobSweeper) SweepAbandoned(_ context.Context, ttl time.Duration) ([]model.Job, error) {
	f.ttl = ttl
	return f.abandoned, f.err
}

// TestJobAbandonReconcilerReportsHowManyItClosed: the count is what the
// scheduled fire returns to DBOS, so a sweep that closed jobs must say so
// rather than reporting a bare success.
func TestJobAbandonReconcilerReportsHowManyItClosed(t *testing.T) {
	sweeper := &fakeJobSweeper{abandoned: []model.Job{{JobID: "job-1"}, {JobID: "job-2"}}}
	r := &JobAbandonReconciler{jobs: sweeper, ttl: DefaultJobAbandonTTL}

	closed, err := r.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile() error = %v", err)
	}
	if closed != 2 {
		t.Fatalf("closed = %d, want 2", closed)
	}
}

// TestJobAbandonReconcilerUsesTheConfiguredTTL: the TTL is the reconciler's
// own policy value, passed through to the sweep rather than re-derived there.
func TestJobAbandonReconcilerUsesTheConfiguredTTL(t *testing.T) {
	sweeper := &fakeJobSweeper{}
	r := &JobAbandonReconciler{jobs: sweeper, ttl: 7 * time.Minute}

	if _, err := r.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile() error = %v", err)
	}
	if sweeper.ttl != 7*time.Minute {
		t.Fatalf("ttl = %v, want %v", sweeper.ttl, 7*time.Minute)
	}
}

// TestJobAbandonReconcilerPropagatesASweepFailure: a sweep that could not
// reach the database must surface as a failed tick, not as a quiet zero. A
// scheduler that cannot tell the two apart stops retrying work that never
// happened.
func TestJobAbandonReconcilerPropagatesASweepFailure(t *testing.T) {
	sweeper := &fakeJobSweeper{err: errors.New("connection reset")}
	r := &JobAbandonReconciler{jobs: sweeper, ttl: DefaultJobAbandonTTL}

	closed, err := r.reconcile(context.Background())

	if err == nil {
		t.Fatal("reconcile() error = nil, want the sweep failure propagated")
	}
	if closed != 0 {
		t.Fatalf("closed = %d, want 0 on a failed sweep", closed)
	}
}

// TestJobAbandonReconcilerOfAQuietPlatformIsNotAFailure: the ordinary result
// on a platform where nothing has been abandoned is zero closed and no error.
func TestJobAbandonReconcilerOfAQuietPlatformIsNotAFailure(t *testing.T) {
	r := &JobAbandonReconciler{jobs: &fakeJobSweeper{}, ttl: DefaultJobAbandonTTL}

	closed, err := r.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile() error = %v", err)
	}
	if closed != 0 {
		t.Fatalf("closed = %d, want 0", closed)
	}
}
