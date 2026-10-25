package jobs

import (
	"context"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/platformapi"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// These are explicit platform commands, unlike job_report.go's best-effort
// recording: a missing alias or unreachable plane is a hard error.

// The job status vocabulary; PLANNED and RUNNING are open, the rest close the job.
const (
	// JobStatusPlanned is never swept to ABANDONED: there is no actor yet to go quiet.
	JobStatusPlanned    = "PLANNED"
	JobStatusRunning    = "RUNNING"
	JobStatusSucceeded  = "SUCCEEDED"
	JobStatusFailed     = "FAILED"
	JobStatusAbandoned  = "ABANDONED"
	JobStatusSuperseded = "SUPERSEDED"
)

// The job type vocabulary is closed so the queue groups without a free-text bucket.
const (
	JobTypeFix         = "fix"
	JobTypeReview      = "review"
	JobTypeGate        = "gate"
	JobTypeRelease     = "release"
	JobTypeDeploy      = "deploy"
	JobTypeInvestigate = "investigate"
	JobTypePlan        = "plan"
	JobTypeTriage      = "triage"
	JobTypeMaintenance = "maintenance"
)

var jobTypes = []string{
	JobTypeFix, JobTypeReview, JobTypeGate, JobTypeRelease, JobTypeDeploy,
	JobTypeInvestigate, JobTypePlan, JobTypeTriage, JobTypeMaintenance,
}

// NormalizeJobType resolves a job type case-insensitively and refuses unknown
// values, which the dashboard cannot render.
func NormalizeJobType(jobType string) (string, error) {
	trimmed := strings.TrimSpace(jobType)
	if trimmed == "" {
		return "", fmt.Errorf("job type is required: expected one of %s", strings.Join(jobTypes, ", "))
	}
	normalized := strings.ToLower(trimmed)
	for _, candidate := range jobTypes {
		if normalized == candidate {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("unsupported job type %q: expected one of %s", trimmed, strings.Join(jobTypes, ", "))
}

// NormalizeJobStatus resolves a status; empty means no filter, valid only for reads.
func NormalizeJobStatus(status string) (string, error) {
	trimmed := strings.TrimSpace(status)
	switch normalized := strings.ToUpper(trimmed); normalized {
	case "":
		return "", nil
	case JobStatusPlanned, JobStatusRunning, JobStatusSucceeded, JobStatusFailed, JobStatusAbandoned, JobStatusSuperseded:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported job status %q: expected one of %s, %s, %s, %s, %s, %s",
			trimmed, JobStatusPlanned, JobStatusRunning, JobStatusSucceeded, JobStatusFailed, JobStatusAbandoned, JobStatusSuperseded)
	}
}

// JobScopeHeldError reports the open job that already holds the requested scope.
type JobScopeHeldError struct {
	Held platformclient.PlatformJobScopeHeld
}

func (e *JobScopeHeldError) Error() string {
	if strings.TrimSpace(e.Held.Summary) == "" {
		return fmt.Sprintf("scope %q is already claimed by %s", e.Held.Scope, e.Held.ActorID)
	}
	return fmt.Sprintf("scope %q is already claimed by %s (started %s): %s",
		e.Held.Scope, e.Held.ActorID, e.Held.StartedAt, e.Held.Summary)
}

func (e *JobScopeHeldError) Unwrap() error { return platformclient.ErrPlatformConflict }

// RunJobList lists the caller's tenant's jobs, live queue first.
func RunJobList(ctx core.Context, store config.CloudReadStore, alias string, params model.JobListParams, deps cloud.CloudDependencies) ([]model.PlatformJob, error) {
	status, err := NormalizeJobStatus(params.Status)
	if err != nil {
		return nil, err
	}
	params.Status = status
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	filter := platformclient.PlatformJobFilter(params)
	platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/jobs", jobFilterTraceDetails(filter)...)
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListJobs(context.Background(), filter)
}

// RunJobClaim records a job starting; a 409 on Scope becomes a *JobScopeHeldError.
func RunJobClaim(ctx core.Context, store config.CloudReadStore, alias string, params model.JobClaimParams, deps cloud.CloudDependencies) (model.PlatformJob, error) {
	params, err := normalizeJobClaim(params)
	if err != nil {
		return model.PlatformJob{}, err
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformJob{}, err
	}
	environmentID, err := resolveJobReportEnvironment(client, params.Environment)
	if err != nil {
		return model.PlatformJob{}, err
	}
	platformapi.TracePlatformCall(ctx, provider, "POST", "/v1/jobs", jobClaimTraceDetails(params)...)
	if ctx.DryRun {
		return model.PlatformJob{}, nil
	}
	created, err := client.ClaimJob(context.Background(), platformclient.PlatformClaimJobParams{
		EnvironmentID: environmentID,
		JobType:       params.JobType,
		Status:        params.Status,
		IssueRef:      params.IssueRef,
		Summary:       params.Summary,
		ActorKind:     params.ActorKind,
		ActorID:       params.ActorID,
		Scope:         params.Scope,
		LocalJobID:    params.LocalJobID,
	})
	if err != nil {
		if held, ok := platformclient.PlatformJobScopeHeldDetails(err); ok {
			return model.PlatformJob{}, &JobScopeHeldError{Held: held}
		}
		return model.PlatformJob{}, err
	}
	return created, nil
}

func normalizeJobClaim(params model.JobClaimParams) (model.JobClaimParams, error) {
	jobType, err := NormalizeJobType(params.JobType)
	if err != nil {
		return model.JobClaimParams{}, err
	}
	actorKind, err := normalizeActorKind(params.ActorKind)
	if err != nil {
		return model.JobClaimParams{}, err
	}
	if strings.TrimSpace(params.Summary) == "" {
		return model.JobClaimParams{}, fmt.Errorf("summary is required: say what the work is, in prose, not the command that performs it")
	}
	if strings.TrimSpace(params.ActorID) == "" {
		return model.JobClaimParams{}, fmt.Errorf("actor id is required: it is who a refused claimant is told to ask")
	}
	status, err := NormalizeJobStatus(params.Status)
	if err != nil {
		return model.JobClaimParams{}, err
	}
	params.JobType = jobType
	params.Status = status
	params.ActorKind = actorKind
	params.Summary = strings.TrimSpace(params.Summary)
	params.ActorID = strings.TrimSpace(params.ActorID)
	params.IssueRef = strings.TrimSpace(params.IssueRef)
	params.Scope = strings.TrimSpace(params.Scope)
	params.LocalJobID = strings.TrimSpace(params.LocalJobID)
	params.Environment = strings.TrimSpace(params.Environment)
	return params, nil
}

func jobClaimTraceDetails(params model.JobClaimParams) []string {
	details := []string{"jobType=" + params.JobType, "actorId=" + params.ActorID}
	if params.Status != "" {
		details = append(details, "status="+params.Status)
	}
	for _, field := range []struct{ name, value string }{
		{"environment", params.Environment},
		{"issueRef", params.IssueRef},
		{"scope", params.Scope},
	} {
		if field.value != "" {
			details = append(details, field.name+"="+field.value)
		}
	}
	return details
}

func RunJobUpdate(ctx core.Context, store config.CloudReadStore, alias string, params model.JobUpdateParams, deps cloud.CloudDependencies) (model.PlatformJob, error) {
	if strings.TrimSpace(params.JobID) == "" {
		return model.PlatformJob{}, fmt.Errorf("job id is required")
	}
	status, err := NormalizeJobStatus(params.Status)
	if err != nil {
		return model.PlatformJob{}, err
	}
	// RUNNING means a planned job has begun; the platform owns the transition
	// table and refuses it otherwise.
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformJob{}, err
	}
	platformapi.TracePlatformCall(ctx, provider, "PATCH", "/v1/jobs/"+params.JobID, jobUpdateTraceDetails(params)...)
	if ctx.DryRun {
		return model.PlatformJob{}, nil
	}
	return client.UpdateJob(context.Background(), params.JobID, platformclient.PlatformUpdateJobParams{
		Status:     status,
		Summary:    strings.TrimSpace(params.Summary),
		LocalJobID: strings.TrimSpace(params.LocalJobID),
	})
}

func RunJobShow(ctx core.Context, store config.CloudReadStore, alias, jobID string, deps cloud.CloudDependencies) (model.PlatformJob, error) {
	if strings.TrimSpace(jobID) == "" {
		return model.PlatformJob{}, fmt.Errorf("job id is required")
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformJob{}, err
	}
	platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/jobs/"+jobID)
	if ctx.DryRun {
		return model.PlatformJob{}, nil
	}
	return client.GetJob(context.Background(), jobID)
}

// normalizeActorKind's vocabulary is closed because the queue renders it.
func normalizeActorKind(actorKind string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(actorKind)); normalized {
	case "agent", "orchestrator", "human":
		return normalized, nil
	case "":
		return "", fmt.Errorf("actor kind is required: expected agent, orchestrator or human")
	default:
		return "", fmt.Errorf("unsupported actor kind %q: expected agent, orchestrator or human", actorKind)
	}
}

func jobFilterTraceDetails(filter platformclient.PlatformJobFilter) []string {
	var details []string
	for _, field := range []struct{ name, value string }{
		{"status", filter.Status},
		{"environmentId", filter.EnvironmentID},
		{"issueRef", filter.IssueRef},
		{"scope", filter.Scope},
		{"actorId", filter.ActorID},
	} {
		if strings.TrimSpace(field.value) != "" {
			details = append(details, field.name+"="+field.value)
		}
	}
	return details
}

func jobUpdateTraceDetails(params model.JobUpdateParams) []string {
	var details []string
	if strings.TrimSpace(params.Status) != "" {
		details = append(details, "status="+params.Status)
	}
	if strings.TrimSpace(params.Summary) != "" {
		details = append(details, "summary="+params.Summary)
	}
	return details
}
