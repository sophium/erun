package eruncommon

import (
	"context"
	"strings"
	"time"
)

// job_report.go lets an agent or orchestrator record the work it is doing to
// the erun platform, so the queue shows what is in flight before it finishes
// and a second actor can see it rather than duplicating it. Every other
// platform command in this module (review_commands.go, gate_run_commands.go)
// treats a missing or broken alias as a hard, propagated error, because a
// human asked for that specific call. This is the opposite contract on
// purpose, and it is ReportBuildOutcome's (build_report.go) exactly: job
// recording runs on the critical path of real work, most environments have no
// platform alias configured at all, and bookkeeping must never fail -- or
// even change the output of -- the work it describes.

// jobReportTimeout bounds each network call this file makes, so an
// unreachable platform cannot hang the work being recorded. It does not bound
// the token mint a request triggers first when no cached access token exists,
// the gap shared by every platform command in this module.
const jobReportTimeout = 20 * time.Second

// ReportJobStartParams carries what an actor knows about the work it is
// about to start. Summary is prose by contract -- the platform refuses one
// that is only a shell command -- and Scope is what the job claims, so a
// second actor asking for the same scope is told who already holds it.
type ReportJobStartParams struct {
	// Environment is the local environment name this work runs in, empty for
	// host-side orchestrator work that never enters one.
	Environment string
	JobType     string
	IssueRef    string
	Summary     string
	ActorKind   string
	ActorID     string
	Scope       string
	LocalJobID  string
}

// ReportJobStart records a job starting, best-effort. It returns the recorded
// job's id when the platform answered and "" otherwise; a caller uses the id
// only to report the outcome later, and must treat "" as "nothing to report
// against", never as an error.
//
// Like ReportBuildOutcome it degrades completely silently with no alias
// configured (the overwhelming majority of invocations) and records every
// other reason as a trace. It never warns, never errors, and never blocks.
func ReportJobStart(ctx Context, store CloudReadStore, deps CloudDependencies, params ReportJobStartParams) string {
	if !hasAnyErunPlatformAlias(store) {
		return ""
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, "", deps)
	if err != nil {
		ctx.Trace("job record to erun platform skipped: " + err.Error())
		return ""
	}
	environmentID, err := resolveJobReportEnvironment(client, params.Environment)
	if err != nil {
		ctx.Trace("job record to erun platform skipped: " + err.Error())
		return ""
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/jobs", jobReportTraceDetails(params)...)
	if ctx.DryRun {
		return ""
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), jobReportTimeout)
	defer cancel()
	created, err := client.ClaimJob(timeoutCtx, PlatformClaimJobParams{
		EnvironmentID: environmentID,
		JobType:       params.JobType,
		IssueRef:      params.IssueRef,
		Summary:       params.Summary,
		ActorKind:     params.ActorKind,
		ActorID:       params.ActorID,
		Scope:         params.Scope,
		LocalJobID:    params.LocalJobID,
	})
	if err != nil {
		// A held scope is the one refusal with a remedy a reader can act on,
		// so it is reported as what it is rather than as another opaque
		// failure -- still a trace, never a warning, since the work itself is
		// unaffected either way.
		if held, ok := PlatformJobScopeHeldDetails(err); ok {
			ctx.Trace("job record to erun platform skipped: scope " + held.Scope +
				" is held by " + held.ActorID + " (started " + held.StartedAt + "): " + held.Summary)
			return ""
		}
		ctx.Trace("job record to erun platform skipped: " + err.Error())
		return ""
	}
	ctx.Trace("job recorded to erun platform: " + created.JobID)
	return created.JobID
}

// ReportJobOutcomeParams carries how a job ended. A terminal status is
// required: there is no way to report "still running" here, since a caller
// that has one is the same caller that started it.
type ReportJobOutcomeParams struct {
	JobID string
	// Status is one of SUCCEEDED, FAILED, ABANDONED, or SUPERSEDED.
	Status string
	// Summary optionally refreshes the job's prose to describe how it ended.
	Summary string
}

// ReportJobOutcome closes a job, best-effort, on the same contract as
// ReportJobStart: silent without an alias, a trace for every other reason,
// never a propagated error. An empty JobID means the start was never
// recorded, so there is nothing to close and nothing is said about it -- the
// caller that had no id already saw why.
func ReportJobOutcome(ctx Context, store CloudReadStore, deps CloudDependencies, params ReportJobOutcomeParams) {
	if strings.TrimSpace(params.JobID) == "" {
		return
	}
	if !hasAnyErunPlatformAlias(store) {
		return
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, "", deps)
	if err != nil {
		ctx.Trace("job outcome report to erun platform skipped: " + err.Error())
		return
	}
	tracePlatformCall(ctx, provider, "PATCH", "/v1/jobs/"+params.JobID,
		"status="+params.Status)
	if ctx.DryRun {
		return
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), jobReportTimeout)
	defer cancel()
	updated, err := client.UpdateJob(timeoutCtx, params.JobID, PlatformUpdateJobParams{
		Status:  params.Status,
		Summary: params.Summary,
	})
	if err != nil {
		ctx.Trace("job outcome report to erun platform skipped: " + err.Error())
		return
	}
	ctx.Trace("job outcome reported to erun platform: " + updated.JobID + " " + updated.Status)
}

// resolveJobReportEnvironment resolves the platform's environment id for a
// local environment name, both network calls bounded by jobReportTimeout. An
// empty name means host-side work: there is no environment to resolve and no
// error, which is a real case rather than a gap.
func resolveJobReportEnvironment(client *PlatformClient, environment string) (string, error) {
	if strings.TrimSpace(environment) == "" {
		return "", nil
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), jobReportTimeout)
	defer cancel()
	return resolvePlatformEnvironmentID(timeoutCtx, client, environment)
}

func jobReportTraceDetails(params ReportJobStartParams) []string {
	details := []string{"jobType=" + params.JobType, "actorId=" + params.ActorID}
	if params.Environment != "" {
		details = append(details, "environment="+params.Environment)
	}
	if params.IssueRef != "" {
		details = append(details, "issueRef="+params.IssueRef)
	}
	if params.Scope != "" {
		details = append(details, "scope="+params.Scope)
	}
	return details
}
