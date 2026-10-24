package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type stubPipelineBuilder struct {
	issues []service.PipelineIssue
	err    error
}

func (s *stubPipelineBuilder) Build(context.Context) ([]service.PipelineIssue, error) {
	return s.issues, s.err
}

func getPipeline(t *testing.T, builder PipelineBuilder) *httptest.ResponseRecorder {
	t.Helper()
	routes := PipelineRoutes{pipeline: builder}
	req := httptest.NewRequest(http.MethodGet, "/v1/pipeline", nil)
	rec := httptest.NewRecorder()
	routes.getPipeline(rec, req)
	return rec
}

func TestGetPipelineWritesTheUnionItWasHanded(t *testing.T) {
	rec := getPipeline(t, &stubPipelineBuilder{issues: []service.PipelineIssue{{
		IssueKey: "sophium/erun#2683",
		Items: []service.PipelineItem{{
			IssueKey: "sophium/erun#2683",
			Rung:     "PLANNED",
			Job:      &model.Job{JobID: "job-1", Status: model.JobStatusPlanned, IssueRef: "sophium/erun#2683"},
		}},
	}}})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var issues []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &issues); err != nil {
		t.Fatalf("body %q is not a JSON array of issues: %v", rec.Body.String(), err)
	}
	if len(issues) != 1 || issues[0]["issueKey"] != "sophium/erun#2683" {
		t.Fatalf("body = %q, want the union the service built", rec.Body.String())
	}
}

func TestGetPipelineOfNothingMarshalsAsAnEmptyArray(t *testing.T) {
	rec := getPipeline(t, &stubPipelineBuilder{})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "[]\n" {
		t.Fatalf("body = %q, want an empty array", got)
	}
}

func TestGetPipelineReportsARepositoryFailure(t *testing.T) {
	rec := getPipeline(t, &stubPipelineBuilder{err: repository.ErrNotFound})

	if rec.Code == http.StatusOK {
		t.Fatalf("status = %d, want a failure rather than an empty view", rec.Code)
	}
}
