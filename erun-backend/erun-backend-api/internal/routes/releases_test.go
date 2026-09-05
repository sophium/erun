package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type stubReleaseQueue struct {
	requests []service.ReleaseRequest
	result   service.EnqueueResult
	err      error
}

func (q *stubReleaseQueue) Enqueue(_ context.Context, request service.ReleaseRequest) (service.EnqueueResult, error) {
	q.requests = append(q.requests, request)
	return q.result, q.err
}

type stubReleaseRepository struct {
	release model.Release
	filters []apirepository.ReleaseFilter
	list    []model.Release
	err     error
}

func (r *stubReleaseRepository) Get(context.Context, string) (model.Release, error) {
	return r.release, r.err
}

func (r *stubReleaseRepository) List(_ context.Context, filter apirepository.ReleaseFilter) ([]model.Release, error) {
	r.filters = append(r.filters, filter)
	return r.list, r.err
}

func releaseRoutes(queue *stubReleaseQueue, releases *stubReleaseRepository) ReleaseRoutes {
	return ReleaseRoutes{releases: releases, queue: queue}
}

func postRelease(t *testing.T, routes ReleaseRoutes, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/releases", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	routes.createRelease(rec, req)
	return rec
}

func TestCreateReleaseQueuesTheCommit(t *testing.T) {
	queue := &stubReleaseQueue{result: service.EnqueueResult{
		Created: true,
		Release: model.Release{ReleaseID: "rel-1", Status: model.ReleaseStatusQueued, CommitID: "commit-a"},
	}}
	rec := postRelease(t, releaseRoutes(queue, &stubReleaseRepository{}),
		`{"reviewId":"review-1","targetBranch":"main","commitId":"commit-a"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if len(queue.requests) != 1 {
		t.Fatalf("enqueued %d requests, want one", len(queue.requests))
	}
	want := service.ReleaseRequest{ReviewID: "review-1", TargetBranch: "main", CommitID: "commit-a"}
	if queue.requests[0] != want {
		t.Fatalf("request = %+v, want %+v", queue.requests[0], want)
	}
}

// TestCreateReleaseAnswers200ForAnAlreadyReleasedCommit lets a caller tell an
// enqueue apart from "this commit is already released" without minting anything.
func TestCreateReleaseAnswers200ForAnAlreadyReleasedCommit(t *testing.T) {
	queue := &stubReleaseQueue{result: service.EnqueueResult{
		AlreadyReleased: true,
		Release:         model.Release{ReleaseID: "rel-1", Status: model.ReleaseStatusReleased, Version: "1.0.150"},
	}}
	rec := postRelease(t, releaseRoutes(queue, &stubReleaseRepository{}),
		`{"targetBranch":"main","commitId":"commit-a"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a commit that already released", rec.Code)
	}
	var got model.Release
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if got.Version != "1.0.150" {
		t.Fatalf("version = %q, want the version already minted for this commit", got.Version)
	}
}

func TestCreateReleaseRejectsAnInvalidRequest(t *testing.T) {
	queue := &stubReleaseQueue{err: apirepository.ErrInvalidInput}
	rec := postRelease(t, releaseRoutes(queue, &stubReleaseRepository{}), `{"commitId":"commit-a"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestListReviewReleasesScopesToTheReview is how a review names why its release
// failed: the reason the release gave lives on the release row.
func TestListReviewReleasesScopesToTheReview(t *testing.T) {
	releases := &stubReleaseRepository{list: []model.Release{{
		ReleaseID:     "rel-1",
		Status:        model.ReleaseStatusFailed,
		FailureReason: "the registry rejected the push",
	}}}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews/review-1/releases", nil)
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()
	releaseRoutes(&stubReleaseQueue{}, releases).listReviewReleases(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(releases.filters) != 1 || releases.filters[0].ReviewID != "review-1" {
		t.Fatalf("filters = %+v, want the listing scoped to the review", releases.filters)
	}
	if !strings.Contains(rec.Body.String(), "the registry rejected the push") {
		t.Fatalf("body = %s, want the failure reason the release recorded", rec.Body.String())
	}
}

func TestListReleasesPassesItsFilters(t *testing.T) {
	releases := &stubReleaseRepository{}
	req := httptest.NewRequest(http.MethodGet, "/v1/releases?targetBranch=main&status=queued&reviewId=review-1", nil)
	rec := httptest.NewRecorder()
	releaseRoutes(&stubReleaseQueue{}, releases).listReleases(rec, req)

	want := apirepository.ReleaseFilter{TargetBranch: "main", ReviewID: "review-1", Status: model.ReleaseStatusQueued}
	if len(releases.filters) != 1 || releases.filters[0] != want {
		t.Fatalf("filter = %+v, want %+v", releases.filters, want)
	}
}
