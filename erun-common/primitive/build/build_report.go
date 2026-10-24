package build

import (
	"context"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// Build reporting is best-effort: most environments have no platform alias, and a build must
// never fail or change its output because reporting is unavailable.

// buildReportTimeout bounds the reporting network calls; it does not bound a token refresh.
const buildReportTimeout = 20 * time.Second

// ReportBuildOutcomeParams carries one build's outcome. The commit is resolved only after an
// alias is confirmed, since rev-parse traces and no-alias builds must not change output.
type ReportBuildOutcomeParams struct {
	// ProjectRoot is where to run `git rev-parse HEAD`; empty skips commit resolution.
	ProjectRoot string
	// Environment is the resolved local env name; empty skips reporting.
	Environment   string
	Version       string
	Successful    bool
	FailureDetail string
	Profile       *model.BuildProfileSummary
}

// ReportBuildOutcome self-reports the build outcome, best-effort, never failing the build.
// No alias is silent with no network call; any other failure is a traced skip.
func ReportBuildOutcome(ctx core.Context, store config.CloudReadStore, deps cloud.CloudDependencies, params ReportBuildOutcomeParams) {
	if !cloud.HasAnyErunPlatformAlias(store) {
		return
	}
	if strings.TrimSpace(params.Environment) == "" {
		ctx.Trace("build report to erun platform skipped: no environment resolved to report against")
		return
	}
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, "", deps)
	if err != nil {
		ctx.Trace("build report to erun platform skipped: " + err.Error())
		return
	}
	commitID, err := resolveBuildReportCommitID(ctx, params.ProjectRoot)
	if err != nil {
		ctx.Trace("build report to erun platform skipped: " + err.Error())
		return
	}
	cloud.TracePlatformCall(ctx, provider, "POST", "/v1/builds", buildReportTraceDetails(params, commitID)...)
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

// resolveBuildReportCommitID returns "" when there is no repository; only a real git failure errors.
func resolveBuildReportCommitID(ctx core.Context, projectRoot string) (string, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return "", nil
	}
	commitID, _, err := shared.GitResolvedRef(ctx, projectRoot, "HEAD")
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

func createUnattachedBuildReport(client *platformclient.PlatformClient, params ReportBuildOutcomeParams, commitID string) (model.PlatformBuild, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), buildReportTimeout)
	defer cancel()

	environmentID, err := cloud.ResolvePlatformEnvironmentID(timeoutCtx, client, params.Environment)
	if err != nil {
		return model.PlatformBuild{}, err
	}
	return client.CreateUnattachedBuild(timeoutCtx, platformclient.PlatformCreateUnattachedBuildParams{
		EnvironmentID: environmentID,
		CommitID:      commitID,
		Version:       params.Version,
		Successful:    params.Successful,
		FailureDetail: params.FailureDetail,
		Profile:       params.Profile,
	})
}

func successfulDetail(successful bool) string {
	if successful {
		return "true"
	}
	return "false"
}
