package build

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

var errMissingPublishVersion = errors.New("publish requires a version: it mirrors an already-built version's images from the FROM to the TO registry and never builds — set the version input")

type PublishInput struct {
	Version   string `json:"version" jsonschema:"required published version to mirror from the FROM registry to each TO registry (produced by build then push); publish never builds or deploys"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned copy commands without executing them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	shared.JobEnvelopeInput
}

func PublishTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PublishInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PublishInput) (*mcp.CallToolResult, shared.JobEnvelopeOutput, error) {
		if strings.TrimSpace(input.Version) == "" {
			return nil, shared.JobEnvelopeOutput{}, errMissingPublishVersion
		}
		execute := shared.SimpleJobExecute(runtime, input.Verbosity, func(runCtx core.Context, workDir string) error {
			findProjectRoot := func() (string, string, error) {
				return shared.RuntimeFindProjectRoot(runtime.Context, workDir)
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
		envelope, err := shared.RunJobEnvelope(runtime, "publish", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}
