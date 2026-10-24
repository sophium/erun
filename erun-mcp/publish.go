package erunmcp

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/orchestration"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

var errMissingPublishVersion = errors.New("publish requires a version: it mirrors an already-built version's images from the FROM to the TO registry and never builds — set the version input")

type PublishInput struct {
	Version   string `json:"version" jsonschema:"required published version to mirror from the FROM registry to each TO registry (produced by build then push); publish never builds or deploys"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned copy commands without executing them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	JobEnvelopeInput
}

func publishTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PublishInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PublishInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		if strings.TrimSpace(input.Version) == "" {
			return nil, JobEnvelopeOutput{}, errMissingPublishVersion
		}
		execute := simpleJobExecute(runtime, input.Verbosity, func(runCtx core.Context, workDir string) error {
			findProjectRoot := func() (string, string, error) {
				return runtimeFindProjectRoot(runtime.Context, workDir)
			}
			resolveBuildContext := func() (shared.DockerBuildContext, error) {
				return shared.DockerBuildContextAtDir(workDir)
			}
			resolveDeployContext := func() (shared.KubernetesDeployContext, error) {
				return deploy.KubernetesDeployContextAtDir(workDir), nil
			}
			target := deploy.DeployTarget{
				Tenant:          strings.TrimSpace(runtime.Context.Tenant),
				Environment:     strings.TrimSpace(runtime.Context.Environment),
				RepoPath:        workDir,
				VersionOverride: strings.TrimSpace(input.Version),
			}
			return orchestration.RunPublish(runCtx, runtime.Store, findProjectRoot, resolveBuildContext, resolveDeployContext, nil, target)
		})
		envelope, err := runJobEnvelope(runtime, "publish", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}
