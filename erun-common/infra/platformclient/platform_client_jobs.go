package platformclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PlatformJob mirrors model.Job's JSON shape.
type PlatformJob struct {
	JobID    string `json:"jobId"`
	TenantID string `json:"tenantId,omitempty"`
	// EnvironmentID is empty for host-side orchestrator work.
	EnvironmentID string `json:"environmentId,omitempty"`
	JobType       string `json:"jobType"`
	// IssueRef is owner/repo#number.
	IssueRef string `json:"issueRef,omitempty"`
	// Summary must be prose; the API refuses a bare shell command.
	Summary   string `json:"summary"`
	Status    string `json:"status"`
	ActorKind string `json:"actorKind"`
	ActorID   string `json:"actorId"`
	// Scope is what the job claims; empty never collides.
	Scope      string     `json:"scope,omitempty"`
	LocalJobID string     `json:"localJobId,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	EndedAt    *time.Time `json:"endedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// PlatformClaimJobParams.Status defaults to RUNNING; a terminal status makes the
// server stamp ended_at.
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

// ClaimJob records a job starting. With Scope set, a 409 means another open job
// holds it; see PlatformJobScopeHeldDetails.
func (c *PlatformClient) ClaimJob(ctx context.Context, params PlatformClaimJobParams) (PlatformJob, error) {
	var job PlatformJob
	err := c.do(ctx, http.MethodPost, "/v1/jobs", params, true, &job)
	return job, err
}

// PlatformUpdateJobParams leaves empty fields unchanged; finished jobs accept no updates.
type PlatformUpdateJobParams struct {
	Status     string `json:"status,omitempty"`
	Summary    string `json:"summary,omitempty"`
	LocalJobID string `json:"localJobId,omitempty"`
}

// UpdateJob moves an existing job forward.
func (c *PlatformClient) UpdateJob(ctx context.Context, jobID string, params PlatformUpdateJobParams) (PlatformJob, error) {
	var job PlatformJob
	err := c.do(ctx, http.MethodPatch, "/v1/jobs/"+url.PathEscape(jobID), params, true, &job)
	return job, err
}

func (c *PlatformClient) GetJob(ctx context.Context, jobID string) (PlatformJob, error) {
	var job PlatformJob
	err := c.do(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(jobID), nil, true, &job)
	return job, err
}

// PlatformJobFilter fields are optional and AND-ed.
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

// ListJobs reads the tenant's queue: live work first, then recent history.
func (c *PlatformClient) ListJobs(ctx context.Context, filter PlatformJobFilter) ([]PlatformJob, error) {
	path := "/v1/jobs"
	if query := filter.queryString(); query != "" {
		path += "?" + query
	}
	var jobs []PlatformJob
	err := c.do(ctx, http.MethodGet, path, nil, true, &jobs)
	return jobs, err
}

// ListEnvironmentJobs reads one environment's queue.
func (c *PlatformClient) ListEnvironmentJobs(ctx context.Context, environmentID string) ([]PlatformJob, error) {
	var jobs []PlatformJob
	err := c.do(ctx, http.MethodGet, "/v1/environments/"+url.PathEscape(environmentID)+"/jobs", nil, true, &jobs)
	return jobs, err
}

// platformJobErrorEnvelope mirrors the backend's {code, message, details} error body.
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

// PlatformJobScopeHeld is the holder named by a refused claim.
type PlatformJobScopeHeld struct {
	Scope     string
	JobID     string
	ActorID   string
	Summary   string
	StartedAt string
}

// PlatformJobScopeHeldDetails extracts a JOB_SCOPE_HELD holder. A false result is
// still a conflict, just unattributed.
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
