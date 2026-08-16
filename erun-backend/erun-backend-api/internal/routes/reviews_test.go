package routes

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type stubReviewService struct {
	review model.Review
	err    error
}

func (s stubReviewService) PrepareCreate(review model.Review) model.Review { return review }

func (s stubReviewService) AdvanceMergeQueue(context.Context, string) (model.Review, error) {
	return s.review, s.err
}

func (s stubReviewService) UpdateStatus(context.Context, string, model.ReviewStatus, string) (model.Review, error) {
	return s.review, s.err
}

type stubBuilds struct {
	build model.Build
	err   error
}

func (b stubBuilds) Get(context.Context, string) (model.Build, error) { return b.build, b.err }

func (b stubBuilds) List(context.Context, apirepository.BuildFilter) ([]model.Build, error) {
	return nil, nil
}

type recordingTrigger struct {
	requests []service.ReleaseRequest
	err      error
}

func (t *recordingTrigger) TriggerRelease(_ context.Context, request service.ReleaseRequest) error {
	t.requests = append(t.requests, request)
	return t.err
}

func patchReviewStatus(t *testing.T, routes ReviewRoutes, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/v1/reviews/review-1/status", bytes.NewBufferString(body))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()
	routes.updateReviewStatus(rec, req)
	return rec
}

func mergedReview() model.Review {
	return model.Review{
		ReviewID:          "review-1",
		TargetBranch:      "main",
		Status:            model.ReviewStatusMerged,
		LastMergedBuildID: "build-1",
	}
}

// TestMergingAReviewTriggersItsRelease is the trigger half of the pipeline: an
// accepted review is what earns a version, and the commit released is the one the
// review actually merged on.
func TestMergingAReviewTriggersItsRelease(t *testing.T) {
	trigger := &recordingTrigger{}
	routes := ReviewRoutes{
		service: stubReviewService{review: mergedReview()},
		builds:  stubBuilds{build: model.Build{BuildID: "build-1", CommitID: "commit-a"}},
		trigger: trigger,
	}
	rec := patchReviewStatus(t, routes, `{"status":"MERGED","buildId":"build-1"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(trigger.requests) != 1 {
		t.Fatalf("triggered %d releases, want one", len(trigger.requests))
	}
	want := service.ReleaseRequest{ReviewID: "review-1", TargetBranch: "main", CommitID: "commit-a"}
	if trigger.requests[0] != want {
		t.Fatalf("release request = %+v, want %+v", trigger.requests[0], want)
	}
}

// TestANonMergedTransitionTriggersNothing: the queue releases what has already
// been accepted; every other transition is not an acceptance.
func TestANonMergedTransitionTriggersNothing(t *testing.T) {
	for _, status := range []model.ReviewStatus{model.ReviewStatusReady, model.ReviewStatusFailed, model.ReviewStatusClosed} {
		trigger := &recordingTrigger{}
		review := mergedReview()
		review.Status = status
		routes := ReviewRoutes{
			service: stubReviewService{review: review},
			builds:  stubBuilds{build: model.Build{BuildID: "build-1", CommitID: "commit-a"}},
			trigger: trigger,
		}
		if rec := patchReviewStatus(t, routes, `{"status":"`+string(status)+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("status %s: HTTP %d, want 200", status, rec.Code)
		}
		if len(trigger.requests) != 0 {
			t.Fatalf("status %s triggered a release: %+v", status, trigger.requests)
		}
	}
}

// TestMergingWithoutAReleaseQueueStillMerges keeps the pre-queue behaviour: a
// control plane with no release queue records reviews exactly as before.
func TestMergingWithoutAReleaseQueueStillMerges(t *testing.T) {
	routes := ReviewRoutes{service: stubReviewService{review: mergedReview()}}
	if rec := patchReviewStatus(t, routes, `{"status":"MERGED","buildId":"build-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestAFailedTriggerSaysTheReviewAlreadyMerged: the transition is persisted by
// the time the trigger runs, so the error must not imply it was rolled back, and
// it must name the recovery.
func TestAFailedTriggerSaysTheReviewAlreadyMerged(t *testing.T) {
	routes := ReviewRoutes{
		service: stubReviewService{review: mergedReview()},
		builds:  stubBuilds{build: model.Build{BuildID: "build-1", CommitID: "commit-a"}},
		trigger: &recordingTrigger{err: errors.New("the queue is unreachable")},
	}
	rec := patchReviewStatus(t, routes, `{"status":"MERGED","buildId":"build-1"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"the review is merged", "POST /v1/releases"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want it to carry %q", body, want)
		}
	}
}
