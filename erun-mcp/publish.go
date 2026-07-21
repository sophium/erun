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
}

func publishTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PublishInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PublishInput) (*mcp.CallToolResult, CommandOutput, error) {
		if strings.TrimSpace(input.Version) == "" {
			return nil, CommandOutput{}, errMissingPublishVersion
		}
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
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
		return nil, output, err
	}
}
