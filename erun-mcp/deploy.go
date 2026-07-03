package erunmcp

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

var errMissingDeployVersion = errors.New("deploy requires a version: it installs a published version by reference (produced by `build` then `push`) and never builds — set the version input")

type DeployInput struct {
	Component    string   `json:"component,omitempty" jsonschema:"component name for the devops k8s deploy COMPONENT command"`
	Components   []string `json:"components,omitempty" jsonschema:"opt-in components to include alongside the runtime chart (erun-backend-postgres, erun-backend-db, erun-backend-api, erun-powerdns); ignored when component is set"`
	Version      string   `json:"version" jsonschema:"required published version to install by reference (produced by build then push); deploy installs by reference and never builds"`
	RuntimeImage string   `json:"runtime_image,omitempty" jsonschema:"install the runtime running this image via the published erun-devops chart (imageOverrides.erun-devops), pinned to version, even when the env has a repo-local runtime chart; use it to bootstrap an env on the canonical ERun base image before its own image is built"`
	Force        bool     `json:"force,omitempty" jsonschema:"when true, re-run the helm upgrade even when the deployed release already matches the requested version"`
	Timeout      string   `json:"timeout,omitempty" jsonschema:"override the helm rollout wait for this deploy as a Go duration (e.g. 8m); empty uses the env's deploy.timeout or the 5m default. The deploy keeps waiting while an image is still pulling and aborts early on a real container failure"`
	Preview      bool     `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity    int      `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func deployTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, DeployInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input DeployInput) (*mcp.CallToolResult, CommandOutput, error) {
		if strings.TrimSpace(input.Version) == "" {
			return nil, CommandOutput{}, errMissingDeployVersion
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
				Tenant:               strings.TrimSpace(runtime.Context.Tenant),
				Environment:          strings.TrimSpace(runtime.Context.Environment),
				RepoPath:             workDir,
				VersionOverride:      strings.TrimSpace(input.Version),
				RuntimeImageOverride: strings.TrimSpace(input.RuntimeImage),
				Components:           input.Components,
				Force:                input.Force,
				RolloutTimeout:       strings.TrimSpace(input.Timeout),
			}

			component := strings.TrimSpace(input.Component)
			if component != "" {
				execution, err := eruncommon.ResolveDeploySpec(runCtx, runtime.Store, findProjectRoot, resolveBuildContext, resolveDeployContext, nil, target, component, strings.TrimSpace(input.Version))
				if err != nil {
					return err
				}
				if err := eruncommon.RunDeploySpec(runCtx, execution, runtime.DeployHelmChart); err != nil {
					return err
				}
				return eruncommon.PersistRuntimeVersionFromDeploySpecs(runCtx, []eruncommon.DeploySpec{execution}, runtime.Store.SaveEnvConfig, eruncommon.ResolveDeployedHelmReleaseVersion)
			}

			executions, err := eruncommon.ResolveCurrentDeploySpecs(runCtx, runtime.Store, findProjectRoot, resolveBuildContext, resolveDeployContext, nil, target)
			if err != nil {
				return err
			}
			if err := eruncommon.RunDeploySpecs(runCtx, executions, runtime.DeployHelmChart); err != nil {
				return err
			}
			return eruncommon.PersistRuntimeVersionFromDeploySpecs(runCtx, executions, runtime.Store.SaveEnvConfig, eruncommon.ResolveDeployedHelmReleaseVersion)
		})
		return nil, output, err
	}
}
