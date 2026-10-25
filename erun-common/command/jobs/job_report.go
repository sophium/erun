package jobs

import (
	"context"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/platformapi"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// Job reporting is best-effort, unlike other platform commands: it runs on the
// critical path of real work and must never fail or change that work's output.

// jobReportTimeout bounds each network call (not the token mint before it).
const jobReportTimeout = 20 * time.Second

// ReportJobStartParams describes work about to start. Summary must be prose,
// and Scope is what the job claims.
type ReportJobStartParams struct {
	// Environment is empty for host-side work.
	Environment string
	JobType     string
	IssueRef    string
	Summary     string
	ActorKind   string
	ActorID     string
	Scope       string
	LocalJobID  string
}

// ReportJobStart records a job starting, best-effort, returning its platform id
// or "" (nothing to report against, not an error). Failures are traces only.
func ReportJobStart(ctx core.Context, store config.CloudReadStore, deps cloud.CloudDependencies, params ReportJobStartParams) string {
	if !platformapi.HasAnyErunPlatformAlias(store) {
		return ""
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, "", deps)
	if err != nil {
		ctx.Trace("job record to erun platform skipped: " + err.Error())
		return ""
	}
	environmentID, err := resolveJobReportEnvironment(client, params.Environment)
	if err != nil {
		ctx.Trace("job record to erun platform skipped: " + err.Error())
		return ""
	}
	platformapi.TracePlatformCall(ctx, provider, "POST", "/v1/jobs", jobReportTraceDetails(params)...)
	if ctx.DryRun {
		return ""
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), jobReportTimeout)
	defer cancel()
	created, err := client.ClaimJob(timeoutCtx, platformclient.PlatformClaimJobParams{
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
		// A held scope gets a reason a reader can act on.
		if held, ok := platformclient.PlatformJobScopeHeldDetails(err); ok {
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

// ReportJobOutcomeParams carries how a job ended; Status must be terminal.
type ReportJobOutcomeParams struct {
	JobID string
	// Status is one of SUCCEEDED, FAILED, ABANDONED, or SUPERSEDED.
	Status  string
	Summary string
}

// ReportJobOutcome closes a job on ReportJobStart's best-effort contract; an
// empty JobID is a no-op.
func ReportJobOutcome(ctx core.Context, store config.CloudReadStore, deps cloud.CloudDependencies, params ReportJobOutcomeParams) {
	if strings.TrimSpace(params.JobID) == "" {
		return
	}
	if !platformapi.HasAnyErunPlatformAlias(store) {
		return
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, "", deps)
	if err != nil {
		ctx.Trace("job outcome report to erun platform skipped: " + err.Error())
		return
	}
	platformapi.TracePlatformCall(ctx, provider, "PATCH", "/v1/jobs/"+params.JobID,
		"status="+params.Status)
	if ctx.DryRun {
		return
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), jobReportTimeout)
	defer cancel()
	updated, err := client.UpdateJob(timeoutCtx, params.JobID, platformclient.PlatformUpdateJobParams{
		Status:  params.Status,
		Summary: params.Summary,
	})
	if err != nil {
		ctx.Trace("job outcome report to erun platform skipped: " + err.Error())
		return
	}
	ctx.Trace("job outcome reported to erun platform: " + updated.JobID + " " + updated.Status)
}

// resolveJobReportEnvironment returns "" with no error for host-side work.
func resolveJobReportEnvironment(client *platformclient.PlatformClient, environment string) (string, error) {
	if strings.TrimSpace(environment) == "" {
		return "", nil
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), jobReportTimeout)
	defer cancel()
	return platformapi.ResolvePlatformEnvironmentID(timeoutCtx, client, environment)
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
