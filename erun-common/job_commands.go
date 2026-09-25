package eruncommon

import (
	"context"
	"fmt"
	"strings"
)

// job_commands.go is the shared planning/execution layer `erun jobs
// list`/`show` and `erun jobs start`/`finish` (CLI) and their MCP tools both
// drive, mirroring gate_run_commands.go: resolve the erun platform alias,
// build a PlatformClient, trace the resolved HTTP call so --dry-run (CLI) and
// a preview path (MCP) never touch the network, then perform it for real.
//
// These are ordinary platform commands, not the best-effort recording path in
// job_report.go: a human or an agent asked for this specific call, so a
// missing alias or an unreachable plane is a hard, propagated error instead
// of a trace.

// The job status vocabulary the platform stores. PLANNED and RUNNING are the
// open states; every other value closes the job.
const (
	// JobStatusPlanned is work recorded before it starts: a triage or plan
	// item parked against its issue, moved to RUNNING when coding begins. It
	// is not swept to ABANDONED, because there is no actor yet to go quiet.
	JobStatusPlanned    = "PLANNED"
	JobStatusRunning    = "RUNNING"
	JobStatusSucceeded  = "SUCCEEDED"
	JobStatusFailed     = "FAILED"
	JobStatusAbandoned  = "ABANDONED"
	JobStatusSuperseded = "SUPERSEDED"
)

// The job type vocabulary -- a closed set so the queue can group without a
// free-text bucket that never converges.
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

// jobTypes is the closed vocabulary in the order the API documents it.
var jobTypes = []string{
	JobTypeFix, JobTypeReview, JobTypeGate, JobTypeRelease, JobTypeDeploy,
	JobTypeInvestigate, JobTypePlan, JobTypeTriage, JobTypeMaintenance,
}

// NormalizeJobType resolves a caller-supplied job type to the spelling the
// platform stores, accepting any casing. An unknown value is refused here
// rather than stored: the dashboard groups by this field, so a value outside
// the vocabulary is a row it cannot render.
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

// NormalizeJobStatus resolves a caller-supplied status to the spelling the
// platform stores. The empty value means "no status filter", which is only
// meaningful for a read -- a write states the status it wants.
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

// JobScopeHeldError reports that another open job already holds the scope this
// claim asked for. It carries the holder so a caller can print who has it,
// what they are doing in prose, and since when -- the payload that makes the
// refusal actionable rather than a bare conflict.
type JobScopeHeldError struct {
	Held PlatformJobScopeHeld
}

func (e *JobScopeHeldError) Error() string {
	if strings.TrimSpace(e.Held.Summary) == "" {
		return fmt.Sprintf("scope %q is already claimed by %s", e.Held.Scope, e.Held.ActorID)
	}
	return fmt.Sprintf("scope %q is already claimed by %s (started %s): %s",
		e.Held.Scope, e.Held.ActorID, e.Held.StartedAt, e.Held.Summary)
}

func (e *JobScopeHeldError) Unwrap() error { return ErrPlatformConflict }

// JobListParams narrows `erun jobs list`. Every field is optional.
type JobListParams struct {
	Status        string
	EnvironmentID string
	IssueRef      string
	Scope         string
	ActorID       string
}

// RunJobList lists the caller's tenant's jobs, the live queue first. It is
// the operator's view of what is being worked on right now and what recently
// finished.
func RunJobList(ctx Context, store CloudReadStore, alias string, params JobListParams, deps CloudDependencies) ([]PlatformJob, error) {
	status, err := NormalizeJobStatus(params.Status)
	if err != nil {
		return nil, err
	}
	params.Status = status
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	filter := PlatformJobFilter(params)
	tracePlatformCall(ctx, provider, "GET", "/v1/jobs", jobFilterTraceDetails(filter)...)
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListJobs(context.Background(), filter)
}

// JobClaimParams is the `erun jobs start` input. Environment is a local
// environment name, resolved to the platform's own id; empty is host-side
// work that never enters an environment, which is a real case rather than a
// gap.
type JobClaimParams struct {
	Environment string
	JobType     string
	IssueRef    string
	Summary     string
	// Status is optional. Empty is RUNNING, which is what every caller before
	// PLANNED existed meant; PLANNED records work that has not begun.
	Status     string
	ActorKind  string
	ActorID    string
	Scope      string
	LocalJobID string
}

// RunJobClaim records a job starting. With Scope set it is a claim on that
// scope; a 409 becomes a *JobScopeHeldError naming the holder, so a caller
// can pick up something else instead of duplicating the work.
func RunJobClaim(ctx Context, store CloudReadStore, alias string, params JobClaimParams, deps CloudDependencies) (PlatformJob, error) {
	params, err := normalizeJobClaim(params)
	if err != nil {
		return PlatformJob{}, err
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformJob{}, err
	}
	environmentID, err := resolveJobReportEnvironment(client, params.Environment)
	if err != nil {
		return PlatformJob{}, err
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/jobs", jobClaimTraceDetails(params)...)
	if ctx.DryRun {
		return PlatformJob{}, nil
	}
	created, err := client.ClaimJob(context.Background(), PlatformClaimJobParams{
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
		if held, ok := PlatformJobScopeHeldDetails(err); ok {
			return PlatformJob{}, &JobScopeHeldError{Held: held}
		}
		return PlatformJob{}, err
	}
	return created, nil
}

// normalizeJobClaim validates a claim up front and returns it with every
// field trimmed and its vocabularies resolved, so the request the platform
// receives is the one this layer already checked.
func normalizeJobClaim(params JobClaimParams) (JobClaimParams, error) {
	jobType, err := NormalizeJobType(params.JobType)
	if err != nil {
		return JobClaimParams{}, err
	}
	actorKind, err := normalizeActorKind(params.ActorKind)
	if err != nil {
		return JobClaimParams{}, err
	}
	if strings.TrimSpace(params.Summary) == "" {
		return JobClaimParams{}, fmt.Errorf("summary is required: say what the work is, in prose, not the command that performs it")
	}
	if strings.TrimSpace(params.ActorID) == "" {
		return JobClaimParams{}, fmt.Errorf("actor id is required: it is who a refused claimant is told to ask")
	}
	status, err := NormalizeJobStatus(params.Status)
	if err != nil {
		return JobClaimParams{}, err
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

func jobClaimTraceDetails(params JobClaimParams) []string {
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

// JobUpdateParams moves an existing job forward. An empty field leaves what
// the job already has.
type JobUpdateParams struct {
	JobID      string
	Status     string
	Summary    string
	LocalJobID string
}

// RunJobUpdate reports a job's progress or its outcome.
func RunJobUpdate(ctx Context, store CloudReadStore, alias string, params JobUpdateParams, deps CloudDependencies) (PlatformJob, error) {
	if strings.TrimSpace(params.JobID) == "" {
		return PlatformJob{}, fmt.Errorf("job id is required")
	}
	status, err := NormalizeJobStatus(params.Status)
	if err != nil {
		return PlatformJob{}, err
	}
	// RUNNING is accepted here and means exactly one thing: the planned job
	// this names has begun. The platform refuses it for any job that is
	// already running (a no-op) or already finished, so the transition table
	// stays in one place rather than being restated as a local guess about a
	// status this layer cannot read.
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformJob{}, err
	}
	tracePlatformCall(ctx, provider, "PATCH", "/v1/jobs/"+params.JobID, jobUpdateTraceDetails(params)...)
	if ctx.DryRun {
		return PlatformJob{}, nil
	}
	return client.UpdateJob(context.Background(), params.JobID, PlatformUpdateJobParams{
		Status:     status,
		Summary:    strings.TrimSpace(params.Summary),
		LocalJobID: strings.TrimSpace(params.LocalJobID),
	})
}

// RunJobShow fetches one job by id.
func RunJobShow(ctx Context, store CloudReadStore, alias, jobID string, deps CloudDependencies) (PlatformJob, error) {
	if strings.TrimSpace(jobID) == "" {
		return PlatformJob{}, fmt.Errorf("job id is required")
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformJob{}, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/jobs/"+jobID)
	if ctx.DryRun {
		return PlatformJob{}, nil
	}
	return client.GetJob(context.Background(), jobID)
}

// normalizeActorKind resolves who is holding a job. The vocabulary is closed
// for the same reason job_type is: the queue renders it.
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

func jobFilterTraceDetails(filter PlatformJobFilter) []string {
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

func jobUpdateTraceDetails(params JobUpdateParams) []string {
	var details []string
	if strings.TrimSpace(params.Status) != "" {
		details = append(details, "status="+params.Status)
	}
	if strings.TrimSpace(params.Summary) != "" {
		details = append(details, "summary="+params.Summary)
	}
	return details
}
