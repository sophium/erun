package service

import (
	"context"
	"errors"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// ReleaseQueueRepository is the persistence behind the per-tenant serial
// queue's idempotency and requeue behavior. Claim/outcome/expiry live on the
// repository (internal/repository/releases.go) and are exercised directly by
// its own SQL-contract tests; nothing here dispatches an attempt — the
// environment that runs `erun release` reports its own outcome, the same
// shift the merge queue's gate build made (see AGENTS.md "Merge Queue").
type ReleaseQueueRepository interface {
	Create(ctx context.Context, release model.Release) (model.Release, error)
	Get(ctx context.Context, releaseID string) (model.Release, error)
	FindByCommit(ctx context.Context, commitID string) (model.Release, error)
	Requeue(ctx context.Context, releaseID string) (model.Release, error)
}

// ReleaseRequest is one trigger: the merge commit to release, the branch it
// landed on, and the review that earned it.
type ReleaseRequest struct {
	ReviewID     string
	TargetBranch string
	CommitID     string
}

// EnqueueResult reports what the trigger did. AlreadyReleased is the first-class
// "this commit is already released" answer: the row names the version that was
// minted for it, and nothing new was queued.
type EnqueueResult struct {
	Release         model.Release
	Created         bool
	Requeued        bool
	AlreadyReleased bool
}

// ReleaseService owns the release queue's idempotency: enqueueing a trigger
// records exactly one row per (tenant, commit), never a second version for
// a commit already released.
type ReleaseService struct {
	releases ReleaseQueueRepository
}

func NewReleaseService(releases ReleaseQueueRepository) *ReleaseService {
	return &ReleaseService{releases: releases}
}

// Enqueue records a release request, or returns the one this merge commit
// already has. Minting a second version for one merge commit is the worst thing
// this queue could do, so the commit — not the request — is the identity: an
// already-released commit is reported back untouched, an in-flight one is
// reported as-is, and only a failed attempt is queued again.
func (s *ReleaseService) Enqueue(ctx context.Context, request ReleaseRequest) (EnqueueResult, error) {
	commit := strings.TrimSpace(request.CommitID)
	branch := strings.TrimSpace(request.TargetBranch)
	if commit == "" || branch == "" {
		return EnqueueResult{}, repository.ErrInvalidInput
	}

	existing, err := s.releases.FindByCommit(ctx, commit)
	switch {
	case err == nil:
		return s.reuse(ctx, existing)
	case errors.Is(err, repository.ErrNotFound):
	default:
		return EnqueueResult{}, err
	}

	created, err := s.releases.Create(ctx, model.Release{
		ReviewID:     strings.TrimSpace(request.ReviewID),
		TargetBranch: branch,
		CommitID:     commit,
	})
	if err != nil {
		// A concurrent trigger for the same commit loses the insert on the
		// per-commit uniqueness contract; its row is the answer, not the error.
		if raced, findErr := s.releases.FindByCommit(ctx, commit); findErr == nil {
			return s.reuse(ctx, raced)
		}
		return EnqueueResult{}, err
	}
	return EnqueueResult{Release: created, Created: true}, nil
}

// reuse decides what an existing row for this commit means for a fresh trigger.
func (s *ReleaseService) reuse(ctx context.Context, existing model.Release) (EnqueueResult, error) {
	switch existing.Status {
	case model.ReleaseStatusReleased:
		return EnqueueResult{Release: existing, AlreadyReleased: true}, nil
	case model.ReleaseStatusFailed:
		requeued, err := s.releases.Requeue(ctx, existing.ReleaseID)
		if err != nil {
			return EnqueueResult{}, err
		}
		return EnqueueResult{Release: requeued, Requeued: true}, nil
	default:
		return EnqueueResult{Release: existing}, nil
	}
}

// Get reads one release.
func (s *ReleaseService) Get(ctx context.Context, releaseID string) (model.Release, error) {
	return s.releases.Get(ctx, releaseID)
}
