package eruncommon

import (
	"context"
	"strings"
	"time"
)

// build_report.go lets an ordinary `erun build` self-report its own outcome
// to the erun platform, so a build no longer needs a review to exist
// anywhere (erun#1954). Every existing platform command in this module
// (review_commands.go, gate_run_commands.go) treats a missing/broken alias
// or an unreachable platform as a hard, propagated error, because a human
// asked for that specific call. This is the opposite contract on purpose:
// `erun build` runs constantly, in every environment, most of which have no
// platform alias configured at all, and a build must never fail -- or even
// change its own output -- because reporting is unavailable.

// buildReportTimeout bounds the network calls this file makes (resolving the
// platform environment id, then reporting the build) so an unreachable
// platform cannot hang a build indefinitely. It does not bound the token
// mint a request triggers first when no cached access token exists (that
// gap is shared by every existing platform command in this module, none of
// which impose a deadline on CloudProviderBearerToken's own refresh call;
// fixing it is a separate change to cloud.go, not specific to build
// reporting).
const buildReportTimeout = 20 * time.Second

// ReportBuildOutcomeParams carries what one `erun build` run knows about
// itself when it finishes. The commit is deliberately not a field here:
// resolving it runs a `git rev-parse` that traces unconditionally
// (gitResolvedRef), so ReportBuildOutcome only resolves it once it has
// already confirmed a platform alias exists to report to -- resolving it
// eagerly, before that check, would add a trace line to every build that
// has no platform alias configured at all, breaking the "no alias means zero
// behavior change" contract this file exists to hold.
type ReportBuildOutcomeParams struct {
	// ProjectRoot is where build ran `git rev-parse HEAD` from; empty skips
	// commit resolution (e.g. no git repository), same as ResolveDockerBuildProjectRoot's
	// own "no repo" case.
	ProjectRoot string
	// Environment is the local env name build already resolved (see
	// ResolveDockerBuildEnvConfig) -- empty when build could not resolve one,
	// in which case there is nothing to report against and this is skipped
	// the same as having no alias.
	Environment   string
	Version       string
	Successful    bool
	FailureDetail string
}

// ReportBuildOutcome self-reports build's outcome to the erun platform,
// best-effort. It returns nothing and never fails the caller's build:
//   - No alias configured at all (the overwhelming majority of invocations)
//     degrades completely silently -- no trace line, no network call, zero
//     behavior change from before this feature existed.
//   - Any other reason reporting cannot complete (ambiguous alias, missing
//     environment, an environment not registered on the platform, the
//     platform unreachable or slow) is a recorded skip: traced, never
//     silent, but never propagated as a build failure either.
//
// Traces the intended call before checking ctx.DryRun, the same contract
// tracePlatformCall's other callers already use, so --dry-run shows the
// attempt without making it.
func ReportBuildOutcome(ctx Context, store CloudReadStore, deps CloudDependencies, params ReportBuildOutcomeParams) {
	if !hasAnyErunPlatformAlias(store) {
		return
	}
	if strings.TrimSpace(params.Environment) == "" {
		ctx.Trace("build report to erun platform skipped: no environment resolved to report against")
		return
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, "", deps)
	if err != nil {
		ctx.Trace("build report to erun platform skipped: " + err.Error())
		return
	}
	commitID, err := resolveBuildReportCommitID(ctx, params.ProjectRoot)
	if err != nil {
		ctx.Trace("build report to erun platform skipped: " + err.Error())
		return
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/builds", buildReportTraceDetails(params, commitID)...)
	if ctx.DryRun {
		return
	}
	if commitID == "" {
		ctx.Trace("build report to erun platform skipped: could not resolve the commit erun build ran against")
		return
	}
	created, err := createUnattachedBuildReport(client, params, commitID)
	if err != nil {
		ctx.Trace("build report to erun platform skipped: " + err.Error())
		return
	}
	ctx.Trace("build reported to erun platform: " + created.BuildID)
}

// resolveBuildReportCommitID reads the full commit hash `erun build` ran
// against. An empty ProjectRoot (no git repository) or an empty result from
// `git rev-parse` (same case, seen through gitResolvedRef's own ok flag) both
// report no commit rather than an error -- only a real git failure is an
// error here.
func resolveBuildReportCommitID(ctx Context, projectRoot string) (string, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return "", nil
	}
	commitID, _, err := gitResolvedRef(ctx, projectRoot, "HEAD")
	return commitID, err
}

func buildReportTraceDetails(params ReportBuildOutcomeParams, commitID string) []string {
	details := []string{"environment=" + params.Environment, "successful=" + successfulDetail(params.Successful)}
	if commitID != "" {
		details = append(details, "commitId="+commitID)
	}
	if params.Version != "" {
		details = append(details, "version="+params.Version)
	}
	return details
}

// createUnattachedBuildReport resolves the platform's environment id and
// reports the build against it, both network calls bounded by
// buildReportTimeout.
func createUnattachedBuildReport(client *PlatformClient, params ReportBuildOutcomeParams, commitID string) (PlatformBuild, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), buildReportTimeout)
	defer cancel()

	environmentID, err := resolvePlatformEnvironmentID(timeoutCtx, client, params.Environment)
	if err != nil {
		return PlatformBuild{}, err
	}
	return client.CreateUnattachedBuild(timeoutCtx, PlatformCreateUnattachedBuildParams{
		EnvironmentID: environmentID,
		CommitID:      commitID,
		Version:       params.Version,
		Successful:    params.Successful,
		FailureDetail: params.FailureDetail,
	})
}

func successfulDetail(successful bool) string {
	if successful {
		return "true"
	}
	return "false"
}
