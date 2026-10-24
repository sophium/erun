package routes

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

// JobRepository is the persistence access job routes need.
type JobRepository interface {
	// Get takes the tenant because erun_operations' RLS would otherwise return any
	// tenant's job to an OPERATIONS caller.
	Get(ctx context.Context, tenantID, jobID string) (model.Job, error)
	List(ctx context.Context, filter apirepository.JobFilter) ([]model.Job, error)
	ListByEnvironment(ctx context.Context, environmentID string) ([]model.Job, error)
	FindOpenByScope(ctx context.Context, scope string) (model.Job, error)
}

// JobService claims and updates jobs.
type JobService interface {
	Claim(ctx context.Context, job model.Job) (model.Job, error)
	Update(ctx context.Context, tenantID, jobID string, status model.JobStatus, summary, localJobID string) (model.Job, error)
}

type JobRoutes struct {
	jobs         JobRepository
	environments EnvironmentGetter
	service      JobService
}

// RegisterJobRoutes wires the jobs API, recording work before it starts so actors
// can see each other's in-flight work.
func RegisterJobRoutes(register ProtectedRouteRegistrar, jobs JobRepository, environments EnvironmentGetter, jobService JobService) {
	routes := JobRoutes{jobs: jobs, environments: environments, service: jobService}
	register(http.MethodGet, "/v1/jobs", http.HandlerFunc(routes.listJobs))
	register(http.MethodPost, "/v1/jobs", http.HandlerFunc(routes.claimJob))
	register(http.MethodGet, "/v1/jobs/{job_id}", http.HandlerFunc(routes.getJob))
	register(http.MethodPatch, "/v1/jobs/{job_id}", http.HandlerFunc(routes.updateJob))
	register(http.MethodGet, "/v1/environments/{environment_id}/jobs", http.HandlerFunc(routes.listEnvironmentJobs))
}

func (r JobRoutes) listJobs(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	filter := apirepository.JobFilter{
		Status:        model.JobStatus(strings.ToUpper(strings.TrimSpace(query.Get("status")))),
		EnvironmentID: strings.TrimSpace(query.Get("environmentId")),
		IssueRef:      strings.TrimSpace(query.Get("issueRef")),
		Scope:         strings.TrimSpace(query.Get("scope")),
		ActorID:       strings.TrimSpace(query.Get("actorId")),
	}
	jobs, err := r.jobs.List(req.Context(), filter)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (r JobRoutes) getJob(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	job, err := r.jobs.Get(req.Context(), securityContext.TenantID, req.PathValue("job_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// claimJobRequest has no timestamps: the server stamps them so a client clock
// cannot age a job.
type claimJobRequest struct {
	EnvironmentID string          `json:"environmentId,omitempty"`
	JobType       model.JobType   `json:"jobType"`
	IssueRef      string          `json:"issueRef,omitempty"`
	Summary       string          `json:"summary"`
	Status        model.JobStatus `json:"status,omitempty"`
	ActorKind     model.ActorKind `json:"actorKind"`
	ActorID       string          `json:"actorId"`
	Scope         string          `json:"scope,omitempty"`
	LocalJobID    string          `json:"localJobId,omitempty"`
}

func (r JobRoutes) claimJob(w http.ResponseWriter, req *http.Request) {
	var body claimJobRequest
	if err := decodeJSON(req, &body); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	job, err := r.service.Claim(req.Context(), model.Job{
		EnvironmentID: strings.TrimSpace(body.EnvironmentID),
		JobType:       model.JobType(strings.ToLower(strings.TrimSpace(string(body.JobType)))),
		IssueRef:      strings.TrimSpace(body.IssueRef),
		Summary:       body.Summary,
		Status:        model.JobStatus(strings.ToUpper(strings.TrimSpace(string(body.Status)))),
		ActorKind:     model.ActorKind(strings.ToLower(strings.TrimSpace(string(body.ActorKind)))),
		ActorID:       strings.TrimSpace(body.ActorID),
		Scope:         strings.TrimSpace(body.Scope),
		LocalJobID:    strings.TrimSpace(body.LocalJobID),
	})
	if err != nil {
		writeJobError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

type updateJobRequest struct {
	Status     model.JobStatus `json:"status,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	LocalJobID string          `json:"localJobId,omitempty"`
}

func (r JobRoutes) updateJob(w http.ResponseWriter, req *http.Request) {
	var body updateJobRequest
	if err := decodeJSON(req, &body); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	job, err := r.service.Update(
		req.Context(),
		securityContext.TenantID,
		req.PathValue("job_id"),
		model.JobStatus(strings.ToUpper(strings.TrimSpace(string(body.Status)))),
		body.Summary,
		body.LocalJobID,
	)
	if err != nil {
		writeJobError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// listEnvironmentJobs confirms the environment first so a foreign id reads as
// not-found, not an empty queue.
func (r JobRoutes) listEnvironmentJobs(w http.ResponseWriter, req *http.Request) {
	environmentID := req.PathValue("environment_id")
	if _, err := r.environments.Get(req.Context(), environmentID); err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	jobs, err := r.jobs.ListByEnvironment(req.Context(), environmentID)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func writeJobError(w http.ResponseWriter, req *http.Request, err error) {
	var invalidInput *service.InvalidJobInputError
	if errors.As(err, &invalidInput) {
		writeErrorDetails(w, http.StatusBadRequest, "INVALID_BODY", invalidInput.Error(), map[string]any{"field": invalidInput.Field})
		return
	}
	var scopeHeld *service.JobScopeHeldError
	if errors.As(err, &scopeHeld) {
		// Naming the holder lets the caller choose to wait rather than duplicate the work.
		writeErrorDetails(w, http.StatusConflict, "JOB_SCOPE_HELD", scopeHeld.Error(), map[string]any{
			"scope":     scopeHeld.Scope,
			"jobId":     scopeHeld.Holder.JobID,
			"actorId":   scopeHeld.Holder.ActorID,
			"summary":   scopeHeld.Holder.Summary,
			"startedAt": scopeHeld.Holder.StartedAt.UTC().Format(time.RFC3339),
		})
		return
	}
	var alreadyFinished *service.JobAlreadyFinishedError
	if errors.As(err, &alreadyFinished) {
		writeErrorCode(w, http.StatusConflict, "JOB_ALREADY_FINISHED", alreadyFinished.Error())
		return
	}
	writeRepositoryError(w, req, err)
}
