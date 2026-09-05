package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// fakeReleaseQueue models the queue table's contracts in memory: one row per
// commit, keyed by commit for the idempotency contract. The invariants it
// holds are the ones PostgreSQL enforces, so a service behaviour that depends
// on them is exercised here and the SQL that enforces them is exercised
// against a real database in internal/repository's own release e2e tests.
type fakeReleaseQueue struct {
	rows   []*model.Release
	nextID int
}

func (q *fakeReleaseQueue) Create(_ context.Context, release model.Release) (model.Release, error) {
	for _, row := range q.rows {
		if row.CommitID == release.CommitID {
			return model.Release{}, errors.New("duplicate key value violates releases_tenant_commit_key")
		}
	}
	q.nextID++
	row := release
	row.ReleaseID = string(rune('a'+q.nextID-1)) + "-release"
	row.Status = model.ReleaseStatusQueued
	row.Attempt = 1
	q.rows = append(q.rows, &row)
	return row, nil
}

func (q *fakeReleaseQueue) Get(_ context.Context, releaseID string) (model.Release, error) {
	for _, row := range q.rows {
		if row.ReleaseID == releaseID {
			return *row, nil
		}
	}
	return model.Release{}, repository.ErrNotFound
}

func (q *fakeReleaseQueue) FindByCommit(_ context.Context, commitID string) (model.Release, error) {
	for _, row := range q.rows {
		if row.CommitID == commitID {
			return *row, nil
		}
	}
	return model.Release{}, repository.ErrNotFound
}

func (q *fakeReleaseQueue) Requeue(_ context.Context, releaseID string) (model.Release, error) {
	for _, row := range q.rows {
		if row.ReleaseID == releaseID && row.Status == model.ReleaseStatusFailed {
			row.Status = model.ReleaseStatusQueued
			row.Attempt++
			row.FailureReason = ""
			return *row, nil
		}
	}
	return model.Release{}, repository.ErrNotFound
}

func (q *fakeReleaseQueue) recordOutcome(releaseID string, status model.ReleaseStatus, version, failureReason string) {
	for _, row := range q.rows {
		if row.ReleaseID != releaseID {
			continue
		}
		row.Status = status
		row.Version = version
		row.FailureReason = failureReason
	}
}

func enqueue(t *testing.T, s *ReleaseService, commit string) EnqueueResult {
	t.Helper()
	result, err := s.Enqueue(context.Background(), ReleaseRequest{
		ReviewID:     "review-1",
		TargetBranch: "main",
		CommitID:     commit,
	})
	if err != nil {
		t.Fatalf("enqueue %s: %v", commit, err)
	}
	return result
}

func TestEnqueueQueuesANewCommit(t *testing.T) {
	queue := &fakeReleaseQueue{}
	service := NewReleaseService(queue)

	result := enqueue(t, service, "commit-a")
	if !result.Created {
		t.Fatal("a commit with no release was not queued")
	}
	if result.Release.Status != model.ReleaseStatusQueued {
		t.Fatalf("status = %q, want queued", result.Release.Status)
	}
	if len(queue.rows) != 1 {
		t.Fatalf("rows = %d, want exactly one", len(queue.rows))
	}
}

func TestEnqueueRejectsARequestWithNothingToRelease(t *testing.T) {
	service := NewReleaseService(&fakeReleaseQueue{})
	for _, request := range []ReleaseRequest{
		{TargetBranch: "main"},
		{CommitID: "commit-a"},
	} {
		if _, err := service.Enqueue(context.Background(), request); !errors.Is(err, repository.ErrInvalidInput) {
			t.Fatalf("enqueue(%+v) error = %v, want invalid input", request, err)
		}
	}
}

// TestEnqueueMintsNothingForAnAlreadyReleasedCommit is the worst failure this
// queue could have: two versions for one merge commit. The already-released row
// is the answer, and no second row appears.
func TestEnqueueMintsNothingForAnAlreadyReleasedCommit(t *testing.T) {
	queue := &fakeReleaseQueue{}
	service := NewReleaseService(queue)
	first := enqueue(t, service, "commit-a")
	queue.recordOutcome(first.Release.ReleaseID, model.ReleaseStatusReleased, "1.0.150", "")

	again := enqueue(t, service, "commit-a")
	if !again.AlreadyReleased {
		t.Fatal("re-triggering a released commit did not report it as already released")
	}
	if again.Created || again.Requeued {
		t.Fatalf("re-triggering a released commit queued work: %+v", again)
	}
	if again.Release.Version != "1.0.150" {
		t.Fatalf("version = %q, want the version already minted for this commit", again.Release.Version)
	}
	if len(queue.rows) != 1 {
		t.Fatalf("rows = %d, want one release for one commit", len(queue.rows))
	}
	if queue.rows[0].Attempt != 1 {
		t.Fatalf("attempt = %d, want the released row untouched", queue.rows[0].Attempt)
	}
}

// TestEnqueueRequeuesAFailedCommit: a transient failure must not lock a commit
// out of ever being released, and the retry has to be a new attempt so its
// workflow runs rather than replay.
func TestEnqueueRequeuesAFailedCommit(t *testing.T) {
	queue := &fakeReleaseQueue{}
	service := NewReleaseService(queue)
	first := enqueue(t, service, "commit-a")
	queue.recordOutcome(first.Release.ReleaseID, model.ReleaseStatusFailed, "", "the registry rejected the push")

	again := enqueue(t, service, "commit-a")
	if !again.Requeued {
		t.Fatalf("re-triggering a failed commit did not requeue it: %+v", again)
	}
	if again.Release.Attempt != 2 {
		t.Fatalf("attempt = %d, want a second attempt", again.Release.Attempt)
	}
	if again.Release.Status != model.ReleaseStatusQueued {
		t.Fatalf("status = %q, want queued", again.Release.Status)
	}
	if again.Release.FailureReason != "" {
		t.Fatalf("requeued release still carries the old reason %q", again.Release.FailureReason)
	}
	if len(queue.rows) != 1 {
		t.Fatalf("rows = %d, want the same row requeued, not a second one", len(queue.rows))
	}
}

func TestEnqueueReturnsTheInFlightReleaseForACommit(t *testing.T) {
	queue := &fakeReleaseQueue{}
	service := NewReleaseService(queue)
	first := enqueue(t, service, "commit-a")

	again := enqueue(t, service, "commit-a")
	if again.Created || again.Requeued || again.AlreadyReleased {
		t.Fatalf("re-triggering a queued commit did something: %+v", again)
	}
	if again.Release.ReleaseID != first.Release.ReleaseID {
		t.Fatalf("release id = %q, want the row already queued %q", again.Release.ReleaseID, first.Release.ReleaseID)
	}
}

func TestGetReadsOneRelease(t *testing.T) {
	queue := &fakeReleaseQueue{}
	service := NewReleaseService(queue)
	created := enqueue(t, service, "commit-a").Release

	got, err := service.Get(context.Background(), created.ReleaseID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ReleaseID != created.ReleaseID {
		t.Fatalf("release id = %q, want %q", got.ReleaseID, created.ReleaseID)
	}
}
