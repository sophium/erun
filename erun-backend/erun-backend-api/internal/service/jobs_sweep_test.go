package service

import (
	"context"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// TestJobServiceSweepAbandonsAStaleRunningJob: an actor that disappears
// without closing its job is the orphaned running record nothing clears.
// The sweep is what clears it -- and it records the transition rather than
// deleting the row, so the queue still shows what was claimed and dropped.
func TestJobServiceSweepAbandonsAStaleRunningJob(t *testing.T) {
	repo := newFakeJobRepo()
	svc := NewJobService(repo)
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "sophium/erun#2109", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	repo.backdate(t, created.JobID, DefaultJobAbandonTTL+time.Minute)

	abandoned, err := svc.SweepAbandoned(ctx, DefaultJobAbandonTTL)
	if err != nil {
		t.Fatalf("SweepAbandoned() error = %v", err)
	}
	if len(abandoned) != 1 || abandoned[0].JobID != created.JobID {
		t.Fatalf("abandoned = %+v, want exactly %s", abandoned, created.JobID)
	}
	if abandoned[0].Status != model.JobStatusAbandoned {
		t.Errorf("status = %q, want ABANDONED", abandoned[0].Status)
	}
	if abandoned[0].EndedAt == nil {
		t.Error("endedAt = nil; closing a job must record when it stopped")
	}
}

// TestJobServiceSweepReleasesTheAbandonedScope is the sweep's whole purpose:
// the scope a vanished actor held must become claimable again. Without this
// the first abandoned job wedges its issue forever, which is worse than the
// duplicate work the claim primitive exists to prevent.
func TestJobServiceSweepReleasesTheAbandonedScope(t *testing.T) {
	repo := newFakeJobRepo()
	svc := NewJobService(repo)
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "sophium/erun#2109", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	repo.backdate(t, created.JobID, DefaultJobAbandonTTL+time.Minute)

	if _, err := svc.SweepAbandoned(ctx, DefaultJobAbandonTTL); err != nil {
		t.Fatalf("SweepAbandoned() error = %v", err)
	}

	if _, err := svc.Claim(ctx, claimFor("erun/code5", "sophium/erun#2109", "picking the issue up after the sweep")); err != nil {
		t.Fatalf("Claim() after the sweep error = %v, want the scope released", err)
	}
}

// TestJobServiceSweepLeavesAFreshJobAlone: the sweep is bounded by a TTL, not
// by a guess that a quiet job is a dead one. An agent mid-task that has not
// needed to update yet must keep its scope.
func TestJobServiceSweepLeavesAFreshJobAlone(t *testing.T) {
	repo := newFakeJobRepo()
	svc := NewJobService(repo)
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "sophium/erun#2109", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	repo.backdate(t, created.JobID, time.Minute)

	abandoned, err := svc.SweepAbandoned(ctx, DefaultJobAbandonTTL)
	if err != nil {
		t.Fatalf("SweepAbandoned() error = %v", err)
	}
	if len(abandoned) != 0 {
		t.Fatalf("abandoned = %+v, want none; the job is inside its TTL", abandoned)
	}

	job, err := svc.jobs.Get(ctx, created.TenantID, created.JobID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if job.Status != model.JobStatusRunning {
		t.Errorf("status = %q, want RUNNING", job.Status)
	}
}

// TestJobServiceSweepLeavesAFinishedJobAlone: only RUNNING jobs are swept. A
// job that already reached an outcome keeps it -- a sweep is not a way to
// rewrite history.
func TestJobServiceSweepLeavesAFinishedJobAlone(t *testing.T) {
	repo := newFakeJobRepo()
	svc := NewJobService(repo)
	ctx := context.Background()

	created, err := svc.Claim(ctx, claimFor("erun/code4", "", "fix the jobs claim race"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := svc.Update(ctx, created.TenantID, created.JobID, model.JobStatusFailed, "", ""); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	repo.backdate(t, created.JobID, 30*DefaultJobAbandonTTL)

	abandoned, err := svc.SweepAbandoned(ctx, DefaultJobAbandonTTL)
	if err != nil {
		t.Fatalf("SweepAbandoned() error = %v", err)
	}
	if len(abandoned) != 0 {
		t.Fatalf("abandoned = %+v, want none; the job already finished", abandoned)
	}

	job, err := svc.jobs.Get(ctx, created.TenantID, created.JobID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if job.Status != model.JobStatusFailed {
		t.Errorf("status = %q, want FAILED to be preserved", job.Status)
	}
}

// TestJobServiceSweepOfNothingIsAnEmptyList: a quiet platform is the ordinary
// case, not an error, and the answer must still be a definite empty list
// rather than a nil a caller has to guard.
func TestJobServiceSweepOfNothingIsAnEmptyList(t *testing.T) {
	svc := NewJobService(newFakeJobRepo())

	abandoned, err := svc.SweepAbandoned(context.Background(), DefaultJobAbandonTTL)
	if err != nil {
		t.Fatalf("SweepAbandoned() error = %v", err)
	}
	if abandoned == nil {
		t.Fatal("abandoned = nil, want an empty slice")
	}
	if len(abandoned) != 0 {
		t.Fatalf("abandoned = %+v, want none", abandoned)
	}
}

// TestJobServiceSweepLeavesAPlannedJobAlone: ABANDONED is what the sweep does
// to a job whose actor stopped updating it, and a parked plan has no actor yet
// to stop. Sweeping one would turn the pipeline's first rung into "dropped"
// inside the TTL, on a schedule, with nothing an operator did to cause it --
// and would do it to every plan in the backlog at once.
//
// The job is backdated far past the TTL so the only thing keeping it out of
// the sweep's result is the exemption itself, not the clock.
func TestJobServiceSweepLeavesAPlannedJobAlone(t *testing.T) {
	repo := newFakeJobRepo()
	svc := NewJobService(repo)
	ctx := context.Background()

	created, err := svc.Claim(ctx, plannedFor("erun/code4", "sophium/erun#2683", "plan the pipeline view"))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	repo.backdate(t, created.JobID, 30*DefaultJobAbandonTTL)

	abandoned, err := svc.SweepAbandoned(ctx, DefaultJobAbandonTTL)
	if err != nil {
		t.Fatalf("SweepAbandoned() error = %v", err)
	}
	if len(abandoned) != 0 {
		t.Fatalf("abandoned = %+v, want none; a PLANNED job is parked, not stale", abandoned)
	}

	job, err := svc.jobs.Get(ctx, created.TenantID, created.JobID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if job.Status != model.JobStatusPlanned {
		t.Errorf("status = %q, want PLANNED to be preserved", job.Status)
	}
	if job.EndedAt != nil {
		t.Errorf("endedAt = %v, want none; a planned job has not stopped", job.EndedAt)
	}
}
