package service

import (
	"context"
	"errors"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// ReleaseQueueRepository persists the per-tenant serial release queue. Nothing
// here dispatches an attempt; the environment running `erun release` reports its
// own outcome.
type ReleaseQueueRepository interface {
	Create(ctx context.Context, release model.Release) (model.Release, error)
	Get(ctx context.Context, releaseID string) (model.Release, error)
	FindByCommit(ctx context.Context, commitID string) (model.Release, error)
	Requeue(ctx context.Context, releaseID string) (model.Release, error)
}

type ReleaseRequest struct {
	ReviewID     string
	TargetBranch string
	CommitID     string
}

// AlreadyReleased means the commit already has a minted version and nothing was queued.
type EnqueueResult struct {
	Release         model.Release
	Created         bool
	Requeued        bool
	AlreadyReleased bool
}

// ReleaseService keeps exactly one release row per (tenant, commit).
type ReleaseService struct {
	releases ReleaseQueueRepository
}

func NewReleaseService(releases ReleaseQueueRepository) *ReleaseService {
	return &ReleaseService{releases: releases}
}

// Enqueue records a release request or returns the one this commit already has.
// The commit is the identity: only a failed attempt is queued again.
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
		// A concurrent trigger loses the per-commit unique insert; the winner's row is the answer.
		if raced, findErr := s.releases.FindByCommit(ctx, commit); findErr == nil {
			return s.reuse(ctx, raced)
		}
		return EnqueueResult{}, err
	}
	return EnqueueResult{Release: created, Created: true}, nil
}

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
	case model.ReleaseStatusQueued, model.ReleaseStatusRunning:
	}
	return EnqueueResult{Release: existing}, nil
}

func (s *ReleaseService) Get(ctx context.Context, releaseID string) (model.Release, error) {
	return s.releases.Get(ctx, releaseID)
}
