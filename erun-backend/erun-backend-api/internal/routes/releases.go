package routes

import (
	"context"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type ReleaseRepository interface {
	Get(ctx context.Context, releaseID string) (model.Release, error)
	List(ctx context.Context, filter apirepository.ReleaseFilter) ([]model.Release, error)
}

// ReleaseQueueService enqueues a release trigger idempotently.
type ReleaseQueueService interface {
	Enqueue(ctx context.Context, request service.ReleaseRequest) (service.EnqueueResult, error)
}

type ReleaseRoutes struct {
	releases ReleaseRepository
	queue    ReleaseQueueService
}

// RegisterReleaseRoutes wires the release endpoints and returns the same routes
// value as the review status route's release trigger, so an accepted review and
// an explicit trigger enqueue through one path.
func RegisterReleaseRoutes(register ProtectedRouteRegistrar, releases ReleaseRepository, queue ReleaseQueueService) ReleaseRoutes {
	routes := ReleaseRoutes{releases: releases, queue: queue}
	register(http.MethodGet, "/v1/releases", http.HandlerFunc(routes.listReleases))
	register(http.MethodPost, "/v1/releases", http.HandlerFunc(routes.createRelease))
	register(http.MethodGet, "/v1/releases/{release_id}", http.HandlerFunc(routes.getRelease))
	register(http.MethodGet, "/v1/reviews/{review_id}/releases", http.HandlerFunc(routes.listReviewReleases))
	return routes
}

// createReleaseRequest is the trigger: the merge commit to release, the branch it
// landed on, and optionally the review that earned it.
type createReleaseRequest struct {
	ReviewID     string `json:"reviewId"`
	TargetBranch string `json:"targetBranch"`
	CommitID     string `json:"commitId"`
}

// createRelease enqueues a release for a merge commit. It answers 200 rather
// than 201 for a commit that already has a release, so a caller can tell an
// enqueue apart from "this commit is already released" without minting anything.
func (r ReleaseRoutes) createRelease(w http.ResponseWriter, req *http.Request) {
	var body createReleaseRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := r.queue.Enqueue(req.Context(), service.ReleaseRequest{
		ReviewID:     strings.TrimSpace(body.ReviewID),
		TargetBranch: strings.TrimSpace(body.TargetBranch),
		CommitID:     strings.TrimSpace(body.CommitID),
	})
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if result.Created {
		writeJSON(w, http.StatusCreated, result.Release)
		return
	}
	writeJSON(w, http.StatusOK, result.Release)
}

// TriggerRelease enqueues the release an accepted review earns. It is shared
// with the review status route, which is where the "accepted, therefore
// release" policy lives; running the release itself is the environment's own
// job, not this control plane's — see AGENTS.md "Merge Queue".
func (r ReleaseRoutes) TriggerRelease(ctx context.Context, request service.ReleaseRequest) error {
	_, err := r.queue.Enqueue(ctx, request)
	return err
}

func (r ReleaseRoutes) listReleases(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	releases, err := r.releases.List(req.Context(), apirepository.ReleaseFilter{
		TargetBranch: query.Get("targetBranch"),
		ReviewID:     query.Get("reviewId"),
		Status:       model.ReleaseStatus(query.Get("status")),
	})
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, releases)
}

// listReviewReleases is how a review names why its release failed: the reason
// the release itself gave lives on the release row, not on the review.
func (r ReleaseRoutes) listReviewReleases(w http.ResponseWriter, req *http.Request) {
	releases, err := r.releases.List(req.Context(), apirepository.ReleaseFilter{ReviewID: req.PathValue("review_id")})
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, releases)
}

func (r ReleaseRoutes) getRelease(w http.ResponseWriter, req *http.Request) {
	release, err := r.releases.Get(req.Context(), req.PathValue("release_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, release)
}
