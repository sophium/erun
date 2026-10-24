package service

import (
	"context"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

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

// Backdated past the TTL so only the PLANNED exemption keeps it out.
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
