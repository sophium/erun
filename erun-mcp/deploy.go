package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type DeployInput struct {
	Component string `json:"component,omitempty" jsonschema:"component name for the devops k8s deploy COMPONENT command"`
	Version   string `json:"version,omitempty" jsonschema:"optional explicit version override for the deployed chart"`
	Snapshot  *bool  `json:"snapshot,omitempty" jsonschema:"optional local snapshot override; when false, skips local snapshot builds in the local environment"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func deployTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, DeployInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input DeployInput) (*mcp.CallToolResult, CommandOutput, error) {
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			component := strings.TrimSpace(input.Component)
			if component == "" {
				return fmt.Errorf("component is required")
			}

			findProjectRoot := func() (string, string, error) {
				return runtimeFindProjectRoot(runtime.Context, workDir)
			}
			resolveBuildContext := func() (eruncommon.DockerBuildContext, error) {
				return eruncommon.DockerBuildContextAtDir(workDir)
			}
			resolveDeployContext := func() (eruncommon.KubernetesDeployContext, error) {
				return eruncommon.KubernetesDeployContextAtDir(workDir), nil
			}

			execution, err := eruncommon.ResolveDeploySpec(runtime.Store, findProjectRoot, resolveBuildContext, resolveDeployContext, nil, eruncommon.DeployTarget{
				Tenant:          strings.TrimSpace(runtime.Context.Tenant),
				Environment:     strings.TrimSpace(runtime.Context.Environment),
				RepoPath:        workDir,
				VersionOverride: strings.TrimSpace(input.Version),
				Snapshot:        input.Snapshot,
			}, component, strings.TrimSpace(input.Version))
			if err != nil {
				return err
			}
			return eruncommon.RunDeploySpec(
				runCtx,
				execution,
				runtime.BuildDockerImage,
				runtimePushFunc(runtime),
				runtime.DeployHelmChart,
			)
		})
		return nil, output, err
	}
}
