package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// InvalidJobInputError names a field violating the jobs table's contract before the CHECK constraint fires.
type InvalidJobInputError struct {
	Field  string
	Reason string
}

func (e *InvalidJobInputError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func (e *InvalidJobInputError) Unwrap() error { return repository.ErrInvalidInput }

// JobScopeHeldError refuses a claim on a held scope, naming the holder, its summary and start time.
type JobScopeHeldError struct {
	Scope  string
	Holder model.Job
}

func (e *JobScopeHeldError) Error() string {
	return fmt.Sprintf(
		"scope %q is held by %s (%s), started %s: %s",
		e.Scope, e.Holder.ActorID, e.Holder.ActorKind, e.Holder.StartedAt.UTC().Format(time.RFC3339), e.Holder.Summary)
}

func (e *JobScopeHeldError) Unwrap() error { return repository.ErrConflict }

// JobAlreadyFinishedError refuses a write against a finished job; outcomes are immutable.
type JobAlreadyFinishedError struct {
	JobID  string
	Status model.JobStatus
}

func (e *JobAlreadyFinishedError) Error() string {
	return fmt.Sprintf("job %s already finished as %s; a finished job cannot be updated", e.JobID, e.Status)
}

func (e *JobAlreadyFinishedError) Unwrap() error { return repository.ErrConflict }

type JobRepository interface {
	Create(ctx context.Context, job model.Job) (model.Job, error)
	// Get and Update take the owning tenant explicitly: an id alone is not a scope.
	Get(ctx context.Context, tenantID, jobID string) (model.Job, error)
	FindOpenByScope(ctx context.Context, scope string) (model.Job, error)
	Update(ctx context.Context, tenantID string, job model.Job) (model.Job, error)
	AbandonStale(ctx context.Context, staleBefore time.Time) ([]model.Job, error)
}

type JobService struct {
	jobs JobRepository
}

func NewJobService(jobs JobRepository) *JobService {
	return &JobService{jobs: jobs}
}

// validJobTypes is closed so a dashboard grouping by type never grows a free-text bucket.
var validJobTypes = map[model.JobType]bool{
	model.JobTypeFix:         true,
	model.JobTypeReview:      true,
	model.JobTypeGate:        true,
	model.JobTypeRelease:     true,
	model.JobTypeDeploy:      true,
	model.JobTypeInvestigate: true,
	model.JobTypePlan:        true,
	model.JobTypeTriage:      true,
	model.JobTypeMaintenance: true,
}

var validActorKinds = map[model.ActorKind]bool{
	model.ActorKindAgent:        true,
	model.ActorKindOrchestrator: true,
	model.ActorKindHuman:        true,
}

var terminalJobStatuses = map[model.JobStatus]bool{
	model.JobStatusSucceeded:  true,
	model.JobStatusFailed:     true,
	model.JobStatusAbandoned:  true,
	model.JobStatusSuperseded: true,
}

var jobStatusTransitions = map[model.JobStatus]map[model.JobStatus]bool{
	model.JobStatusPlanned: {
		model.JobStatusRunning:    true,
		model.JobStatusSucceeded:  true,
		model.JobStatusFailed:     true,
		model.JobStatusAbandoned:  true,
		model.JobStatusSuperseded: true,
	},
	model.JobStatusRunning: {
		model.JobStatusSucceeded:  true,
		model.JobStatusFailed:     true,
		model.JobStatusAbandoned:  true,
		model.JobStatusSuperseded: true,
	},
}

const jobStatusTransitionReason = "must move a PLANNED job to RUNNING or close it, and a RUNNING job to SUCCEEDED, FAILED, ABANDONED, or SUPERSEDED"

// jobCommandWords are shell commands a summary must not consist of alone.
var jobCommandWords = map[string]bool{
	"erun": true, "git": true, "make": true, "sh": true, "bash": true,
	"docker": true, "kubectl": true, "go": true, "npm": true, "yarn": true,
}

// jobProseWords separate prose that starts with a command name from a pasted command line. The heuristic
// favours accepting: wrongly refusing a real description would block a legitimate claim.
var jobProseWords = map[string]bool{
	"a": true, "an": true, "and": true, "as": true, "at": true, "because": true,
	"before": true, "but": true, "by": true, "for": true, "from": true, "in": true,
	"into": true, "is": true, "it": true, "its": true, "not": true, "of": true,
	"on": true, "or": true, "so": true, "that": true, "the": true, "then": true,
	"this": true, "to": true, "until": true, "when": true, "while": true, "with": true,
}

// Claim records a job starting, refusing it if an open job holds the same scope. This is advisory, not a
// lock: an exclusive DB constraint would wedge a scope forever when an actor vanishes. PLANNED claims hold
// the scope but are exempt from SweepAbandoned.
func (s *JobService) Claim(ctx context.Context, job model.Job) (model.Job, error) {
	if job.Status == "" {
		job.Status = model.JobStatusRunning
	}
	if err := validateJobWrite(job); err != nil {
		return model.Job{}, err
	}
	if strings.TrimSpace(job.Scope) != "" {
		holder, err := s.jobs.FindOpenByScope(ctx, job.Scope)
		switch {
		case err == nil:
			return model.Job{}, &JobScopeHeldError{Scope: job.Scope, Holder: holder}
		case !errors.Is(err, repository.ErrNotFound):
			return model.Job{}, err
		}
	}
	now := time.Now().UTC()
	if job.StartedAt.IsZero() {
		job.StartedAt = now
	}
	if terminalJobStatuses[job.Status] && job.EndedAt == nil {
		ended := now
		job.EndedAt = &ended
	}
	return s.jobs.Create(ctx, job)
}

// Update moves an open job forward; a finished job accepts no change.
func (s *JobService) Update(ctx context.Context, tenantID, jobID string, status model.JobStatus, summary, localJobID string) (model.Job, error) {
	existing, err := s.jobs.Get(ctx, tenantID, jobID)
	if err != nil {
		return model.Job{}, err
	}
	if !existing.IsOpen() {
		return model.Job{}, &JobAlreadyFinishedError{JobID: jobID, Status: existing.Status}
	}

	updated := existing
	if status != "" && status != existing.Status {
		if !jobStatusTransitions[existing.Status][status] {
			return model.Job{}, &InvalidJobInputError{Field: "status", Reason: jobStatusTransitionReason}
		}
		updated.Status = status
		if terminalJobStatuses[status] {
			ended := time.Now().UTC()
			updated.EndedAt = &ended
		} else {
			updated.EndedAt = nil
		}
	}
	if strings.TrimSpace(summary) != "" {
		updated.Summary = strings.TrimSpace(summary)
	}
	if strings.TrimSpace(localJobID) != "" {
		updated.LocalJobID = strings.TrimSpace(localJobID)
	}
	if err := validateJobSummary(updated.Summary); err != nil {
		return model.Job{}, err
	}
	return s.jobs.Update(ctx, tenantID, updated)
}

// DefaultJobAbandonTTL is generous because agents update jobs only at meaningful boundaries.
const DefaultJobAbandonTTL = 30 * time.Minute

// SweepAbandoned closes RUNNING jobs not updated within ttl. It is the only path that takes a stale scope,
// and it records the transition. PLANNED is exempt in JobRepository.AbandonStale: a parked plan has no actor.
func (s *JobService) SweepAbandoned(ctx context.Context, ttl time.Duration) ([]model.Job, error) {
	if ttl <= 0 {
		ttl = DefaultJobAbandonTTL
	}
	return s.jobs.AbandonStale(ctx, time.Now().UTC().Add(-ttl))
}

func validateJobWrite(job model.Job) error {
	if !validJobTypes[job.JobType] {
		return &InvalidJobInputError{Field: "jobType", Reason: "must be one of fix, review, gate, release, deploy, investigate, plan, triage, maintenance"}
	}
	if !validActorKinds[job.ActorKind] {
		return &InvalidJobInputError{Field: "actorKind", Reason: "must be one of agent, orchestrator, human"}
	}
	if strings.TrimSpace(job.ActorID) == "" {
		return &InvalidJobInputError{Field: "actorId", Reason: "is required"}
	}
	if job.Status != model.JobStatusRunning && job.Status != model.JobStatusPlanned && !terminalJobStatuses[job.Status] {
		return &InvalidJobInputError{Field: "status", Reason: "must be PLANNED, RUNNING, SUCCEEDED, FAILED, ABANDONED, or SUPERSEDED"}
	}
	return validateJobSummary(job.Summary)
}

func validateJobSummary(summary string) error {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return &InvalidJobInputError{Field: "summary", Reason: "is required"}
	}
	if isOnlyAShellCommand(trimmed) {
		return &InvalidJobInputError{
			Field:  "summary",
			Reason: "must describe the work, not be a shell command; say what it is doing, e.g. \"gate the prospective merge of the jobs branch\"",
		}
	}
	return nil
}

func isOnlyAShellCommand(summary string) bool {
	fields := strings.Fields(strings.ToLower(summary))
	if len(fields) == 0 || !jobCommandWords[fields[0]] {
		return false
	}
	for _, field := range fields[1:] {
		if jobProseWords[strings.Trim(field, ".,;:!?\"'")] {
			return false
		}
	}
	return true
}
