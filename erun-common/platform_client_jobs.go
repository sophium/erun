package eruncommon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// platform_client_jobs.go extends PlatformClient with the jobs surface: the
// platform's record of work in flight, claimed before the work starts rather
// than reported only once it finishes the way builds and gate runs are. That
// is the half that lets two agents see each other and stop duplicating work.

// PlatformJob mirrors model.Job's JSON shape.
type PlatformJob struct {
	JobID    string `json:"jobId"`
	TenantID string `json:"tenantId,omitempty"`
	// EnvironmentID is empty for host-side orchestrator work that never
	// enters an environment -- a real case, not a gap.
	EnvironmentID string `json:"environmentId,omitempty"`
	JobType       string `json:"jobType"`
	// IssueRef is the issue this work belongs to, in owner/repo#number form.
	IssueRef string `json:"issueRef,omitempty"`
	// Summary is prose by contract: the API refuses one that is only a shell
	// command, and the pod's own job record keeps the command line.
	Summary   string `json:"summary"`
	Status    string `json:"status"`
	ActorKind string `json:"actorKind"`
	ActorID   string `json:"actorId"`
	// Scope is what this job claims, for the dedup query. Empty means it
	// claims nothing and can never collide.
	Scope string `json:"scope,omitempty"`
	// LocalJobID mirrors the in-pod job record's own id when there is one.
	LocalJobID string     `json:"localJobId,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	EndedAt    *time.Time `json:"endedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// PlatformClaimJobParams is the claim/record input. Status defaults to
// RUNNING server-side when empty; a caller recording work that has already
// finished may set a terminal status directly and the server stamps the
// ended_at it implies, so the caller never has to pair the two itself.
type PlatformClaimJobParams struct {
	EnvironmentID string `json:"environmentId,omitempty"`
	JobType       string `json:"jobType"`
	IssueRef      string `json:"issueRef,omitempty"`
	Summary       string `json:"summary"`
	Status        string `json:"status,omitempty"`
	ActorKind     string `json:"actorKind"`
	ActorID       string `json:"actorId"`
	Scope         string `json:"scope,omitempty"`
	LocalJobID    string `json:"localJobId,omitempty"`
}

// ClaimJob records a job starting. With Scope set it is a claim on that
// scope: a 409 whose body names the holder means another open job already
// holds it, and the caller should pick something else rather than duplicate
// the work. See PlatformJobScopeHeldDetails.
func (c *PlatformClient) ClaimJob(ctx context.Context, params PlatformClaimJobParams) (PlatformJob, error) {
	var job PlatformJob
	err := c.do(ctx, http.MethodPost, "/v1/jobs", params, true, &job)
	return job, err
}

// PlatformUpdateJobParams is the progress/completion input. Every field is
// optional; an empty one leaves what the job already has. A job that has
// already finished accepts none of them -- its outcome is the record
// coordination and reporting both read.
type PlatformUpdateJobParams struct {
	Status     string `json:"status,omitempty"`
	Summary    string `json:"summary,omitempty"`
	LocalJobID string `json:"localJobId,omitempty"`
}

// UpdateJob moves an existing job forward: a status change, a refreshed
// summary, or the local job id it mirrors.
func (c *PlatformClient) UpdateJob(ctx context.Context, jobID string, params PlatformUpdateJobParams) (PlatformJob, error) {
	var job PlatformJob
	err := c.do(ctx, http.MethodPatch, "/v1/jobs/"+url.PathEscape(jobID), params, true, &job)
	return job, err
}

// GetJob reads one job by id.
func (c *PlatformClient) GetJob(ctx context.Context, jobID string) (PlatformJob, error) {
	var job PlatformJob
	err := c.do(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(jobID), nil, true, &job)
	return job, err
}

// PlatformJobFilter narrows a GET /v1/jobs read. Every field is optional and
// AND-ed together.
type PlatformJobFilter struct {
	Status        string
	EnvironmentID string
	IssueRef      string
	Scope         string
	ActorID       string
}

func (f PlatformJobFilter) queryString() string {
	values := url.Values{}
	for _, field := range []struct{ name, value string }{
		{"status", f.Status},
		{"environmentId", f.EnvironmentID},
		{"issueRef", f.IssueRef},
		{"scope", f.Scope},
		{"actorId", f.ActorID},
	} {
		if strings.TrimSpace(field.value) != "" {
			values.Set(field.name, field.value)
		}
	}
	return values.Encode()
}

// ListJobs reads the caller's own tenant's queue: the live work first, then
// the most recently started history.
func (c *PlatformClient) ListJobs(ctx context.Context, filter PlatformJobFilter) ([]PlatformJob, error) {
	path := "/v1/jobs"
	if query := filter.queryString(); query != "" {
		path += "?" + query
	}
	var jobs []PlatformJob
	err := c.do(ctx, http.MethodGet, path, nil, true, &jobs)
	return jobs, err
}

// ListEnvironmentJobs reads one environment's queue -- the view for a caller
// that knows only its own environment id.
func (c *PlatformClient) ListEnvironmentJobs(ctx context.Context, environmentID string) ([]PlatformJob, error) {
	var jobs []PlatformJob
	err := c.do(ctx, http.MethodGet, "/v1/environments/"+url.PathEscape(environmentID)+"/jobs", nil, true, &jobs)
	return jobs, err
}

// platformJobErrorEnvelope mirrors the {code, message, details} body
// erun-backend-api's route error helper writes, so a caller can reach the
// structured half of a refusal rather than parsing the formatted message.
type platformJobErrorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details struct {
		Scope     string `json:"scope"`
		JobID     string `json:"jobId"`
		ActorID   string `json:"actorId"`
		Summary   string `json:"summary"`
		StartedAt string `json:"startedAt"`
	} `json:"details"`
}

// PlatformJobScopeHeld is the holder a refused claim names: who has the
// scope, what they are doing in prose, and since when. It is the whole
// payload of the coordination primitive -- a caller told only "conflict" has
// learned nothing it can act on.
type PlatformJobScopeHeld struct {
	Scope     string
	JobID     string
	ActorID   string
	Summary   string
	StartedAt string
}

// PlatformJobScopeHeldDetails reports the holder behind a JOB_SCOPE_HELD
// refusal, or ok=false when err is not one. A caller must treat !ok the same
// as any other conflict: the refusal is still a refusal, it just could not be
// attributed -- an older platform, or a body that failed to parse.
func PlatformJobScopeHeldDetails(err error) (PlatformJobScopeHeld, bool) {
	var statusErr *PlatformStatusError
	if !errors.As(err, &statusErr) || statusErr.Status != http.StatusConflict {
		return PlatformJobScopeHeld{}, false
	}
	var envelope platformJobErrorEnvelope
	if jsonErr := json.Unmarshal(statusErr.Body, &envelope); jsonErr != nil {
		return PlatformJobScopeHeld{}, false
	}
	if envelope.Code != "JOB_SCOPE_HELD" {
		return PlatformJobScopeHeld{}, false
	}
	return PlatformJobScopeHeld{
		Scope:     envelope.Details.Scope,
		JobID:     envelope.Details.JobID,
		ActorID:   envelope.Details.ActorID,
		Summary:   envelope.Details.Summary,
		StartedAt: envelope.Details.StartedAt,
	}, true
}
