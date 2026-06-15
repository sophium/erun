package erunmcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type DeployInput struct {
	Component  string   `json:"component,omitempty" jsonschema:"component name for the devops k8s deploy COMPONENT command"`
	Components []string `json:"components,omitempty" jsonschema:"opt-in components to include alongside the runtime chart (erun-backend-postgres, erun-backend-db, erun-backend-api); ignored when component is set"`
	Version    string   `json:"version,omitempty" jsonschema:"optional already-published version to install by reference instead of building from the working tree; fails if that version's image is absent"`
	Force      bool     `json:"force,omitempty" jsonschema:"when true, bypass the fingerprint cache and re-run helm upgrade even when no source change is detected"`
	Publish    bool     `json:"publish,omitempty" jsonschema:"when true, package and push each resolved chart to the environment's container registry as an OCI Helm artifact before helm upgrade"`
	Preview    bool     `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity  int      `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func deployTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, DeployInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input DeployInput) (*mcp.CallToolResult, CommandOutput, error) {
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
				Components:      input.Components,
				Force:           input.Force,
				Publish:         input.Publish,
				// An explicit version is an install target, not a build label:
				// address the already-published version rather than rebuilding
				// the working tree under it (#556).
				InstallExistingVersion: strings.TrimSpace(input.Version) != "",
			}

			component := strings.TrimSpace(input.Component)
			if component != "" {
				execution, err := eruncommon.ResolveDeploySpec(runCtx, runtime.Store, findProjectRoot, resolveBuildContext, resolveDeployContext, nil, target, component, strings.TrimSpace(input.Version))
				if err != nil {
					return err
				}
				if err := eruncommon.RunDeploySpec(runCtx, execution, runtime.BuildDockerImage, runtimePushFunc(runtime), runtime.DeployHelmChart); err != nil {
					return err
				}
				return eruncommon.PersistRuntimeVersionFromDeploySpecs(runCtx, []eruncommon.DeploySpec{execution}, runtime.Store.SaveEnvConfig, eruncommon.ResolveDeployedHelmReleaseVersion)
			}

			executions, err := eruncommon.ResolveCurrentDeploySpecs(runCtx, runtime.Store, findProjectRoot, resolveBuildContext, resolveDeployContext, nil, target)
			if err != nil {
				return err
			}
			if err := eruncommon.RunDeploySpecs(runCtx, executions, runtime.BuildDockerImage, runtimePushFunc(runtime), runtime.DeployHelmChart); err != nil {
				return err
			}
			return eruncommon.PersistRuntimeVersionFromDeploySpecs(runCtx, executions, runtime.Store.SaveEnvConfig, eruncommon.ResolveDeployedHelmReleaseVersion)
		})
		return nil, output, err
	}
}
