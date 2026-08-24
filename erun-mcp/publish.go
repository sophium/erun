package erunmcp

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

var errMissingPublishVersion = errors.New("publish requires a version: it mirrors an already-built version's images from the FROM to the TO registry and never builds — set the version input")

type PublishInput struct {
	Version   string `json:"version" jsonschema:"required published version to mirror from the FROM registry to each TO registry (produced by build then push); publish never builds or deploys"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned copy commands without executing them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Wait      *bool  `json:"wait,omitempty" jsonschema:"when true (the default this release), run synchronously and return the full result inline, exactly as before this input existed. Set false to start the work as a background job and get back {jobId, state: running} immediately instead -- poll exec_job_status/exec_job_await/exec_job_output for the outcome. This default flips to false in a future release, with true kept callable for one more release as the compatibility switch"`
}

func publishTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PublishInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PublishInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		if strings.TrimSpace(input.Version) == "" {
			return nil, JobEnvelopeOutput{}, errMissingPublishVersion
		}
		execute := simpleJobExecute(runtime, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			findProjectRoot := func() (string, string, error) {
				return runtimeFindProjectRoot(runtime.Context, workDir)
			}
			resolveBuildContext := func() (eruncommon.DockerBuildContext, error) {
				return eruncommon.DockerBuildContextAtDir(workDir)
			}
			resolveDeployContext := func() (eruncommon.KubernetesDeployContext, error) {
				return eruncommon.KubernetesDeployContextAtDir(workDir), nil
			}
			target := eruncommon.DeployTarget{
				Tenant:          strings.TrimSpace(runtime.Context.Tenant),
				Environment:     strings.TrimSpace(runtime.Context.Environment),
				RepoPath:        workDir,
				VersionOverride: strings.TrimSpace(input.Version),
			}
			return eruncommon.RunPublish(runCtx, runtime.Store, findProjectRoot, resolveBuildContext, resolveDeployContext, nil, target)
		})
		envelope, err := runJobEnvelope(runtime, "publish", input.Wait, input.Preview, execute)
		return nil, envelope, err
	}
}
