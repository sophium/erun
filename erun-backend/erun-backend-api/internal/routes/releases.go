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

// RegisterReleaseRoutes wires the release endpoints; the returned routes also serve the review status trigger.
func RegisterReleaseRoutes(register ProtectedRouteRegistrar, releases ReleaseRepository, queue ReleaseQueueService) ReleaseRoutes {
	routes := ReleaseRoutes{releases: releases, queue: queue}
	register(http.MethodGet, "/v1/releases", http.HandlerFunc(routes.listReleases))
	register(http.MethodPost, "/v1/releases", http.HandlerFunc(routes.createRelease))
	register(http.MethodGet, "/v1/releases/{release_id}", http.HandlerFunc(routes.getRelease))
	register(http.MethodGet, "/v1/reviews/{review_id}/releases", http.HandlerFunc(routes.listReviewReleases))
	return routes
}

type createReleaseRequest struct {
	ReviewID     string `json:"reviewId"`
	TargetBranch string `json:"targetBranch"`
	CommitID     string `json:"commitId"`
}

// createRelease answers 200, not 201, for a commit that already has a release.
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
		writeRepositoryError(w, req, err)
		return
	}
	if result.Created {
		writeJSON(w, http.StatusCreated, result.Release)
		return
	}
	writeJSON(w, http.StatusOK, result.Release)
}

// TriggerRelease enqueues the release an accepted review earns.
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
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, releases)
}

func (r ReleaseRoutes) listReviewReleases(w http.ResponseWriter, req *http.Request) {
	releases, err := r.releases.List(req.Context(), apirepository.ReleaseFilter{ReviewID: req.PathValue("review_id")})
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, releases)
}

func (r ReleaseRoutes) getRelease(w http.ResponseWriter, req *http.Request) {
	release, err := r.releases.Get(req.Context(), req.PathValue("release_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, release)
}
