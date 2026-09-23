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

// InvalidJobInputError refuses a job write whose field does not satisfy the
// jobs table's own contract, named up front instead of surfacing as a
// generic 400 once the database's CHECK constraint fires.
type InvalidJobInputError struct {
	Field  string
	Reason string
}

func (e *InvalidJobInputError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func (e *InvalidJobInputError) Unwrap() error { return repository.ErrInvalidInput }

// JobScopeHeldError refuses a claim on a scope another open job already
// holds. It is the coordination primitive's whole payload: the refusal names
// who holds the scope, what they are doing in prose, and when they started,
// so a refused actor can decide for itself whether to wait, ask, or pick up
// something else — rather than being told only "conflict".
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

// JobAlreadyFinishedError refuses a write against a job that already
// finished: an outcome is immutable once reached, the same discipline that
// keeps a gate run's verdict and a review's recorded builds append-only.
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
	// Get and Update take the owning tenant explicitly; see
	// repository.JobRepository.Get for why an id alone is not a scope.
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

// validJobTypes is the closed job_type vocabulary. An unknown value is
// refused rather than stored, so a dashboard grouping by type has a finite
// set to render and never grows a free-text bucket.
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

// terminalJobStatuses are the states a job can end in; RUNNING is only ever
// the status a claim assigns.
var terminalJobStatuses = map[model.JobStatus]bool{
	model.JobStatusSucceeded:  true,
	model.JobStatusFailed:     true,
	model.JobStatusAbandoned:  true,
	model.JobStatusSuperseded: true,
}

// jobCommandWords are the shell commands a summary must never be only. A row
// reading "git -C /home/erun/git/erun rebase --onto main~1" describes nothing
// a reader can act on: it is the work's mechanics, not its intent, and the
// pod's own job record already keeps the command line as technical detail.
var jobCommandWords = map[string]bool{
	"erun": true, "git": true, "make": true, "sh": true, "bash": true,
	"docker": true, "kubectl": true, "go": true, "npm": true, "yarn": true,
}

// jobProseWords are the English function words a description of work
// naturally uses and a bare command line does not. Their presence is what
// separates "erun build is failing on main" — prose that merely opens with a
// command's name — from "erun build --release", which is the command itself.
//
// This is a heuristic, deliberately: the rule being enforced is "describe
// the work, do not paste the command", and no parser can decide that for
// certain. It is tuned to refuse a pasted command line and to accept a
// description that happens to start with a command's name, since wrongly
// refusing a real description would block a legitimate claim.
var jobProseWords = map[string]bool{
	"a": true, "an": true, "and": true, "as": true, "at": true, "because": true,
	"before": true, "but": true, "by": true, "for": true, "from": true, "in": true,
	"into": true, "is": true, "it": true, "its": true, "not": true, "of": true,
	"on": true, "or": true, "so": true, "that": true, "the": true, "then": true,
	"this": true, "to": true, "until": true, "when": true, "while": true, "with": true,
}

// Claim records a job starting. With a scope set it is a claim on that
// scope: if an open job already holds it in this tenant, the claim is
// refused with the holder named — see JobScopeHeldError — so a second actor
// asking for the same work is told who has it and what they are doing
// instead of silently duplicating it.
//
// This is deliberately advisory coordination over server-side state, not a
// distributed lock. Two claims landing at the same instant can both be
// recorded, and that is the accepted trade: making the scope exclusive in
// the database would wedge a scope permanently the moment an actor
// disappeared without closing its job, which is the orphaned-running-record
// failure this design exists to avoid. The queue makes the overlap visible
// rather than pretending it cannot happen.
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
	// A caller recording work that has already finished may claim straight
	// into a terminal status; the ended_at that status requires is stamped
	// here so the caller never has to pair the two itself.
	if terminalJobStatuses[job.Status] && job.EndedAt == nil {
		ended := now
		job.EndedAt = &ended
	}
	return s.jobs.Create(ctx, job)
}

// Update moves an existing job forward: a status change, a refreshed
// summary, or the local job id it mirrors. A finished job accepts none of
// these — see JobAlreadyFinishedError — since its outcome is the record that
// coordination and reporting both read.
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
		if !terminalJobStatuses[status] {
			return model.Job{}, &InvalidJobInputError{Field: "status", Reason: "must be SUCCEEDED, FAILED, ABANDONED, or SUPERSEDED when changing a running job"}
		}
		ended := time.Now().UTC()
		updated.Status = status
		updated.EndedAt = &ended
	}
	if strings.TrimSpace(summary) != "" {
		updated.Summary = strings.TrimSpace(summary)
	}
	if strings.TrimSpace(localJobID) != "" {
		updated.LocalJobID = strings.TrimSpace(localJobID)
	}
	// The refreshed summary is validated exactly as a fresh claim's is: a
	// progress refresh is as capable of pasting a command line in as a claim
	// is, and the contract belongs to the column, not to the write that first
	// populated it.
	if err := validateJobSummary(updated.Summary); err != nil {
		return model.Job{}, err
	}
	return s.jobs.Update(ctx, tenantID, updated)
}

// DefaultJobAbandonTTL is how long a RUNNING job may go without an update
// before a sweep treats its actor as gone. It is generous on purpose: an
// agent working a real issue updates its job only at meaningful boundaries
// (see the refresh path in Update), so a short TTL would abandon jobs that
// are plainly still being worked.
const DefaultJobAbandonTTL = 30 * time.Minute

// SweepAbandoned closes every RUNNING job nobody has updated within ttl, so
// an actor that disappears without closing its job cannot wedge a scope
// forever — the failure a permanently orphaned running record produces on
// the local side, where nothing ever clears it.
//
// This is an explicit sweep rather than implicit reaping: abandonment is a
// recorded transition performed by a caller that means to perform it, never
// something a read infers about a row it happens to look at. A claim never
// steals a scope on the grounds that its holder looks stale; only this does,
// and it leaves the row saying what happened.
func (s *JobService) SweepAbandoned(ctx context.Context, ttl time.Duration) ([]model.Job, error) {
	if ttl <= 0 {
		ttl = DefaultJobAbandonTTL
	}
	return s.jobs.AbandonStale(ctx, time.Now().UTC().Add(-ttl))
}

// validateJobWrite mirrors the jobs table's own CHECK constraints and its
// closed vocabularies so a violation is named up front rather than surfaced
// as a generic 400 from the database.
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
	if job.Status != model.JobStatusRunning && !terminalJobStatuses[job.Status] {
		return &InvalidJobInputError{Field: "status", Reason: "must be RUNNING, SUCCEEDED, FAILED, ABANDONED, or SUPERSEDED"}
	}
	return validateJobSummary(job.Summary)
}

// validateJobSummary enforces the summary contract: non-empty, and a
// description of the work rather than the command that performs it.
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

// isOnlyAShellCommand reports whether summary is a shell invocation with no
// prose around it. See jobProseWords for why this is a heuristic rather than
// a parse.
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
