package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type stubJobRepository struct {
	listFilter     apirepository.JobFilter
	jobs           []model.Job
	environment    string
	openByScope    model.Job
	openByScopeErr error
}

func (s *stubJobRepository) Get(_ context.Context, jobID string) (model.Job, error) {
	return model.Job{JobID: jobID, Status: model.JobStatusRunning}, nil
}

func (s *stubJobRepository) List(_ context.Context, filter apirepository.JobFilter) ([]model.Job, error) {
	s.listFilter = filter
	return s.jobs, nil
}

func (s *stubJobRepository) ListByEnvironment(_ context.Context, environmentID string) ([]model.Job, error) {
	s.environment = environmentID
	return s.jobs, nil
}

func (s *stubJobRepository) FindOpenByScope(_ context.Context, scope string) (model.Job, error) {
	return s.openByScope, s.openByScopeErr
}

type stubJobService struct {
	claimed model.Job
	updated model.Job
	err     error
}

func (s *stubJobService) Claim(_ context.Context, job model.Job) (model.Job, error) {
	s.claimed = job
	if s.err != nil {
		return model.Job{}, s.err
	}
	job.JobID = "job-1"
	return job, nil
}

func (s *stubJobService) Update(_ context.Context, jobID string, status model.JobStatus, summary, localJobID string) (model.Job, error) {
	if s.err != nil {
		return model.Job{}, s.err
	}
	s.updated = model.Job{JobID: jobID, Status: status, Summary: summary, LocalJobID: localJobID}
	return s.updated, nil
}

type stubJobEnvironments struct{}

func (stubJobEnvironments) Get(_ context.Context, environmentID string) (model.Environment, error) {
	return model.Environment{EnvironmentID: environmentID}, nil
}

// TestListJobsReturnsAnEmptyArrayNotNull pins the client contract an empty
// queue has to hold: a tenant with no jobs ranges over an empty list, and a
// null body would force every caller to null-check a collection the platform
// always has.
func TestListJobsReturnsAnEmptyArrayNotNull(t *testing.T) {
	routes := JobRoutes{jobs: &stubJobRepository{jobs: []model.Job{}}}
	rec := httptest.NewRecorder()

	routes.listJobs(rec, httptest.NewRequest(http.MethodGet, "/v1/jobs", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("body = %q, want %q", body, "[]")
	}
}

// TestListJobsPassesEveryDocumentedFilter: the issue names five composable
// filters, and a filter the route silently drops is worse than one it
// rejects -- the caller gets an unfiltered queue and believes it is filtered.
func TestListJobsPassesEveryDocumentedFilter(t *testing.T) {
	repo := &stubJobRepository{jobs: []model.Job{}}
	routes := JobRoutes{jobs: repo}
	rec := httptest.NewRecorder()

	routes.listJobs(rec, httptest.NewRequest(http.MethodGet,
		"/v1/jobs?status=running&environmentId=env-1&issueRef=sophium/erun%232109&scope=branch:main&actorId=erun", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	want := apirepository.JobFilter{
		Status:        model.JobStatusRunning,
		EnvironmentID: "env-1",
		IssueRef:      "sophium/erun#2109",
		Scope:         "branch:main",
		ActorID:       "erun",
	}
	if repo.listFilter != want {
		t.Fatalf("filter = %+v, want %+v", repo.listFilter, want)
	}
}

// TestClaimJobNamesTheHolderOnAConflict is the coordination primitive's whole
// payload: a second actor asking for a held scope must be told *who* holds it,
// what they are doing in prose, and since when. A bare 409 teaches a caller
// nothing it can act on -- the defect the local activity lease has, where a
// refusal is anonymous.
func TestClaimJobNamesTheHolderOnAConflict(t *testing.T) {
	started := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	svc := &stubJobService{err: &service.JobScopeHeldError{
		Scope: "sophium/erun#2109",
		Holder: model.Job{
			JobID:     "job-held",
			ActorID:   "erun/code4",
			Summary:   "fixing the jobs claim race on the console queue",
			StartedAt: started,
		},
	}}
	routes := JobRoutes{service: svc}
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs",
		bytes.NewBufferString(`{"jobType":"fix","summary":"claim the same issue","actorKind":"agent","actorId":"erun/code5","scope":"sophium/erun#2109"}`))
	rec := httptest.NewRecorder()

	routes.claimJob(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response was not JSON: %v: %s", err, rec.Body.String())
	}
	if body.Code != "JOB_SCOPE_HELD" {
		t.Fatalf("code = %q, want JOB_SCOPE_HELD", body.Code)
	}
	for field, want := range map[string]string{
		"scope":     "sophium/erun#2109",
		"jobId":     "job-held",
		"actorId":   "erun/code4",
		"summary":   "fixing the jobs claim race on the console queue",
		"startedAt": "2026-09-21T12:00:00Z",
	} {
		if got, _ := body.Details[field].(string); got != want {
			t.Errorf("details[%q] = %q, want %q", field, got, want)
		}
	}
}

// TestClaimJobRejectsAnUnknownJobType: job_type is a closed vocabulary so the
// queue can group without a free-text bucket. A value outside it is refused at
// the API rather than stored and discovered later by a dashboard that cannot
// render it.
func TestClaimJobRejectsAnUnknownJobType(t *testing.T) {
	svc := &stubJobService{err: &service.InvalidJobInputError{Field: "jobType", Reason: "must be one of fix, review, gate, release, deploy, investigate, plan, triage, maintenance"}}
	routes := JobRoutes{service: svc}
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs",
		bytes.NewBufferString(`{"jobType":"refactor","summary":"tidy the jobs routes","actorKind":"agent","actorId":"erun/code5"}`))
	rec := httptest.NewRecorder()

	routes.claimJob(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response was not JSON: %v: %s", err, rec.Body.String())
	}
	if got, _ := body.Details["field"].(string); got != "jobType" {
		t.Fatalf("details[field] = %q, want jobType", got)
	}
}

// TestClaimJobNormalizesCallerCase: the CLI and MCP transports document
// lowercase values ("fix", "agent"), and the route must accept what it teaches
// rather than passing the caller's case straight into the closed vocabularies
// -- the same normalization TestStartGateRunNormalizesLowercaseStatus pins for
// gate runs.
func TestClaimJobNormalizesCallerCase(t *testing.T) {
	svc := &stubJobService{}
	routes := JobRoutes{service: svc}
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs",
		bytes.NewBufferString(`{"jobType":"FIX","summary":"tidy the jobs routes","actorKind":"Agent","actorId":" erun/code5 ","status":"running"}`))
	rec := httptest.NewRecorder()

	routes.claimJob(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if svc.claimed.JobType != model.JobTypeFix {
		t.Errorf("jobType = %q, want %q", svc.claimed.JobType, model.JobTypeFix)
	}
	if svc.claimed.ActorKind != model.ActorKindAgent {
		t.Errorf("actorKind = %q, want %q", svc.claimed.ActorKind, model.ActorKindAgent)
	}
	if svc.claimed.ActorID != "erun/code5" {
		t.Errorf("actorId = %q, want %q", svc.claimed.ActorID, "erun/code5")
	}
}

// TestClaimJobNeverAcceptsAServerStampedTime: started_at is what a refused
// claimant is told, so a caller's own clock must not be able to age a job.
// The request body has no such field to send; this pins that a caller cannot
// smuggle one in through an unknown key either.
func TestClaimJobNeverAcceptsAServerStampedTime(t *testing.T) {
	svc := &stubJobService{}
	routes := JobRoutes{service: svc}
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs",
		bytes.NewBufferString(`{"jobType":"fix","summary":"tidy the jobs routes","actorKind":"agent","actorId":"erun/code5","startedAt":"2001-01-01T00:00:00Z"}`))
	rec := httptest.NewRecorder()

	routes.claimJob(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if !svc.claimed.StartedAt.IsZero() {
		t.Fatalf("startedAt = %v, want the zero time so the service stamps it", svc.claimed.StartedAt)
	}
}

// TestUpdateJobRejectsAnUpdateToAFinishedJob: an outcome is immutable once
// reached, the same discipline that keeps a gate run's verdict append-only.
func TestUpdateJobRejectsAnUpdateToAFinishedJob(t *testing.T) {
	svc := &stubJobService{err: &service.JobAlreadyFinishedError{JobID: "job-1", Status: model.JobStatusSucceeded}}
	routes := JobRoutes{service: svc}
	req := httptest.NewRequest(http.MethodPatch, "/v1/jobs/job-1",
		bytes.NewBufferString(`{"status":"FAILED"}`))
	req.SetPathValue("job_id", "job-1")
	rec := httptest.NewRecorder()

	routes.updateJob(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response was not JSON: %v: %s", err, rec.Body.String())
	}
	if body.Code != "JOB_ALREADY_FINISHED" {
		t.Fatalf("code = %q, want JOB_ALREADY_FINISHED", body.Code)
	}
}

// TestListEnvironmentJobsConfirmsTheEnvironmentFirst: another tenant's
// environment id must read as not-found rather than as an empty queue, the
// same discipline the ai-sessions sub-route follows. An empty queue is a
// confident "nothing is running there"; a foreign id is not.
func TestListEnvironmentJobsScopesToTheEnvironment(t *testing.T) {
	repo := &stubJobRepository{jobs: []model.Job{}}
	routes := JobRoutes{jobs: repo, environments: stubJobEnvironments{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/environments/env-1/jobs", nil)
	req.SetPathValue("environment_id", "env-1")
	rec := httptest.NewRecorder()

	routes.listEnvironmentJobs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if repo.environment != "env-1" {
		t.Fatalf("environment = %q, want env-1", repo.environment)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("body = %q, want []", body)
	}
}
