package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type BuildInput struct {
	Component string `json:"component,omitempty" jsonschema:"optional component name to build from the runtime repo root; when empty, build all Docker component images"`
	Version   string `json:"version,omitempty" jsonschema:"optional explicit image version override; disables local snapshot tagging when set"`
	Deploy    bool   `json:"deploy,omitempty" jsonschema:"when true, push the built images and deploy the resolved Helm chart(s) using the built version"`
	Release   bool   `json:"release,omitempty" jsonschema:"when true, run release first and publish the resolved release-tagged images"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type PushInput struct {
	Component string `json:"component,omitempty" jsonschema:"optional component name to push; required when the runtime repo root is not itself a Docker build context"`
	Version   string `json:"version,omitempty" jsonschema:"optional explicit image version override; disables local snapshot tagging when set"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func buildTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, BuildInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input BuildInput) (*mcp.CallToolResult, CommandOutput, error) {
		output, err := runRuntimeCommand(runtime.Context, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			component := strings.TrimSpace(input.Component)
			version := strings.TrimSpace(input.Version)
			execution, err := resolveRuntimeBuildExecution(runtime, workDir, component, version, input.Release)
			if err != nil {
				return err
			}
			if !input.Deploy {
				return eruncommon.RunBuildExecution(runCtx, execution, runtime.BuildScriptRunner, runtime.BuildDockerImage, runtimePushFunc(runtime))
			}
			if eruncommon.BuildExecutionUsesBuildScript(execution) {
				return fmt.Errorf("build deploy is not supported for project build scripts")
			}

			deploySpecs, err := resolveRuntimeBuildDeploySpecs(runtime, workDir, component, version, input.Release)
			if err != nil {
				return err
			}
			return eruncommon.RunBuildExecutionAndDeploy(runCtx, execution, deploySpecs, runtime.BuildScriptRunner, runtime.BuildDockerImage, runtimePushFunc(runtime), runtime.DeployHelmChart)
		})
		return nil, output, err
	}
}

func pushTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PushInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PushInput) (*mcp.CallToolResult, CommandOutput, error) {
		output, err := runRuntimeCommand(runtime.Context, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			execution, err := resolveRuntimePushExecution(runtime, workDir, strings.TrimSpace(input.Component), strings.TrimSpace(input.Version))
			if err != nil {
				return err
			}
			return eruncommon.RunDockerPushExecution(runCtx, execution, runtime.BuildDockerImage, runtimePushFunc(runtime))
		})
		return nil, output, err
	}
}

func resolveRuntimeBuildExecution(runtime RuntimeConfig, projectRoot, component, versionOverride string, release bool) (eruncommon.BuildExecutionSpec, error) {
	environment := strings.TrimSpace(runtime.Context.Environment)
	target := eruncommon.DockerCommandTarget{
		ProjectRoot:     projectRoot,
		Environment:     environment,
		VersionOverride: versionOverride,
		Release:         release,
	}
	findProjectRoot := func() (string, string, error) {
		return runtimeFindProjectRoot(runtime.Context, projectRoot)
	}
	resolveBuildContext := func() (eruncommon.DockerBuildContext, error) {
		return eruncommon.DockerBuildContextAtDir(projectRoot)
	}

	if component != "" {
		target, releaseSpec, err := eruncommon.ResolveDockerBuildTarget(findProjectRoot, target)
		if err != nil {
			return eruncommon.BuildExecutionSpec{}, err
		}

		buildContext, ok, err := eruncommon.FindComponentDockerBuildContext(projectRoot, component)
		if err != nil {
			return eruncommon.BuildExecutionSpec{}, err
		}
		if !ok {
			return eruncommon.BuildExecutionSpec{}, fmt.Errorf("docker build context not found for component %q", component)
		}
		imageRef, err := eruncommon.ResolveDockerImageReference(runtime.Store, findProjectRoot, resolveBuildContext, nil, buildContext.Dir, target)
		if err != nil {
			return eruncommon.BuildExecutionSpec{}, err
		}
		execution := eruncommon.BuildExecutionSpecFromDockerBuilds([]eruncommon.DockerBuildSpec{{
			ContextDir:     eruncommon.ResolveDockerBuildContextDirForProject(buildContext.Dir, projectRoot),
			DockerfilePath: buildContext.DockerfilePath,
			Image:          imageRef,
		}})
		if releaseSpec != nil {
			return eruncommon.BuildExecutionSpecWithRelease(execution, *releaseSpec), nil
		}
		return execution, nil
	}

	return eruncommon.ResolveBuildExecution(runtime.Store, findProjectRoot, resolveBuildContext, nil, target)
}

func resolveRuntimePushExecution(runtime RuntimeConfig, projectRoot, component, versionOverride string) (eruncommon.DockerPushExecutionSpec, error) {
	target := eruncommon.DockerCommandTarget{
		ProjectRoot:     projectRoot,
		Environment:     strings.TrimSpace(runtime.Context.Environment),
		VersionOverride: versionOverride,
	}
	findProjectRoot := func() (string, string, error) {
		return runtimeFindProjectRoot(runtime.Context, projectRoot)
	}
	resolveBuildContext := func() (eruncommon.DockerBuildContext, error) {
		return eruncommon.DockerBuildContextAtDir(projectRoot)
	}

	if component == "" {
		pushInput, buildInput, err := eruncommon.ResolveDockerPushSpec(runtime.Store, findProjectRoot, resolveBuildContext, nil, target)
		if err != nil {
			return eruncommon.DockerPushExecutionSpec{}, err
		}
		builds := make([]eruncommon.DockerBuildSpec, 0, 1)
		if buildInput != nil {
			builds = append(builds, *buildInput)
		}
		return eruncommon.DockerPushExecutionSpecFromSpecs(builds, []eruncommon.DockerPushSpec{pushInput}), nil
	}

	buildContext, ok, err := eruncommon.FindComponentDockerBuildContext(projectRoot, component)
	if err != nil {
		return eruncommon.DockerPushExecutionSpec{}, err
	}
	if !ok {
		return eruncommon.DockerPushExecutionSpec{}, fmt.Errorf("docker build context not found for component %q", component)
	}

	imageRef, err := eruncommon.ResolveDockerImageReference(runtime.Store, findProjectRoot, resolveBuildContext, nil, buildContext.Dir, target)
	if err != nil {
		return eruncommon.DockerPushExecutionSpec{}, err
	}

	builds := make([]eruncommon.DockerBuildSpec, 0, 1)
	if imageRef.IsLocalBuild {
		build, err := eruncommon.ResolveDockerBuildForComponent(runtime.Store, findProjectRoot, resolveBuildContext, nil, projectRoot, target.Environment, component, strings.TrimSpace(target.VersionOverride))
		if err != nil {
			return eruncommon.DockerPushExecutionSpec{}, err
		}
		if build == nil {
			return eruncommon.DockerPushExecutionSpec{}, fmt.Errorf("docker build context not found for component %q", component)
		}
		builds = append(builds, *build)
		imageRef = build.Image
	}

	return eruncommon.DockerPushExecutionSpecFromSpecs(builds, []eruncommon.DockerPushSpec{
		eruncommon.NewDockerPushSpec(projectRoot, imageRef),
	}), nil
}

func resolveRuntimeBuildDeploySpecs(runtime RuntimeConfig, projectRoot, component, versionOverride string, release bool) ([]eruncommon.DeploySpec, error) {
	target := eruncommon.DockerCommandTarget{
		ProjectRoot:     projectRoot,
		Environment:     strings.TrimSpace(runtime.Context.Environment),
		VersionOverride: versionOverride,
		Release:         release,
	}
	findProjectRoot := func() (string, string, error) {
		return runtimeFindProjectRoot(runtime.Context, projectRoot)
	}
	resolveBuildContext := func() (eruncommon.DockerBuildContext, error) {
		return eruncommon.DockerBuildContextAtDir(projectRoot)
	}
	resolveDeployContext := func() (eruncommon.KubernetesDeployContext, error) {
		return eruncommon.KubernetesDeployContextAtDir(projectRoot), nil
	}

	if component != "" {
		spec, err := eruncommon.ResolveDeploySpecForDockerTarget(runtime.Store, findProjectRoot, resolveBuildContext, resolveDeployContext, nil, target, component)
		if err != nil {
			return nil, err
		}
		return []eruncommon.DeploySpec{spec}, nil
	}

	return eruncommon.ResolveCurrentDeploySpecsForDockerTarget(runtime.Store, findProjectRoot, resolveBuildContext, resolveDeployContext, nil, target)
}
