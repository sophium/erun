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
