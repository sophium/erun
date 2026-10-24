package build

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/orchestration/release"
	"github.com/sophium/erun/erun-common/primitive/deploy/kubecheck"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
	"github.com/sophium/erun/erun-mcp/tools/shared/jobenvelope"
	"github.com/sophium/erun/erun-mcp/tools/shared/runcmd"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

var errMissingPublishVersion = errors.New("publish requires a version: it mirrors an already-built version's images from the FROM to the TO registry and never builds — set the version input")

type PublishInput struct {
	Version   string `json:"version" jsonschema:"required published version to mirror from the FROM registry to each TO registry (produced by build then push); publish never builds or deploys"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned copy commands without executing them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	jobenvelope.JobEnvelopeInput
}

func PublishTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PublishInput) (*mcp.CallToolResult, jobenvelope.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PublishInput) (*mcp.CallToolResult, jobenvelope.JobEnvelopeOutput, error) {
		if strings.TrimSpace(input.Version) == "" {
			return nil, jobenvelope.JobEnvelopeOutput{}, errMissingPublishVersion
		}
		execute := runcmd.SimpleJobExecute(runtime, input.Verbosity, func(runCtx core.Context, workDir string) error {
			findProjectRoot := func() (string, string, error) {
				return toolio.RuntimeFindProjectRoot(runtime.Context, workDir)
			}
			resolveBuildContext := func() (model.DockerBuildContext, error) {
				return dockerspec.DockerBuildContextAtDir(workDir)
			}
			resolveDeployContext := func() (model.KubernetesDeployContext, error) {
				return kubecheck.KubernetesDeployContextAtDir(workDir), nil
			}
			target := model.DeployTarget{
				Tenant:          strings.TrimSpace(runtime.Context.Tenant),
				Environment:     strings.TrimSpace(runtime.Context.Environment),
				RepoPath:        workDir,
				VersionOverride: strings.TrimSpace(input.Version),
			}
			return release.RunPublish(runCtx, runtime.Store, findProjectRoot, resolveBuildContext, resolveDeployContext, nil, target)
		})
		envelope, err := jobenvelope.RunJobEnvelope(runtime, "publish", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}
