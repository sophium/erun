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
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type PushInput struct {
	Component string `json:"component,omitempty" jsonschema:"optional component name to push; required when the runtime repo root is not itself a Docker build context"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

func buildTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, BuildInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input BuildInput) (*mcp.CallToolResult, CommandOutput, error) {
		output, err := runRuntimeCommand(runtime.Context, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			execution, err := resolveRuntimeBuildExecution(runtime, workDir, strings.TrimSpace(input.Component))
			if err != nil {
				return err
			}
			return eruncommon.RunBuildExecution(runCtx, execution, runtime.BuildScriptRunner, runtime.BuildDockerImage, runtimePushFunc(runtime))
		})
		return nil, output, err
	}
}

func pushTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PushInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PushInput) (*mcp.CallToolResult, CommandOutput, error) {
		output, err := runRuntimeCommand(runtime.Context, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			execution, err := resolveRuntimePushExecution(runtime, workDir, strings.TrimSpace(input.Component))
			if err != nil {
				return err
			}
			return eruncommon.RunDockerPushExecution(runCtx, execution, runtime.BuildDockerImage, runtimePushFunc(runtime))
		})
		return nil, output, err
	}
}

func resolveRuntimeBuildExecution(runtime RuntimeConfig, projectRoot, component string) (eruncommon.BuildExecutionSpec, error) {
	environment := strings.TrimSpace(runtime.Context.Environment)
	target := eruncommon.DockerCommandTarget{
		ProjectRoot: projectRoot,
		Environment: environment,
	}
	findProjectRoot := func() (string, string, error) {
		return runtimeFindProjectRoot(runtime.Context, projectRoot)
	}
	resolveBuildContext := func() (eruncommon.DockerBuildContext, error) {
		return eruncommon.DockerBuildContextAtDir(projectRoot)
	}

	if component != "" {
		buildContext, ok, err := eruncommon.FindComponentDockerBuildContext(projectRoot, component)
		if err != nil {
			return eruncommon.BuildExecutionSpec{}, err
		}
		if !ok {
			return eruncommon.BuildExecutionSpec{}, fmt.Errorf("docker build context not found for component %q", component)
		}
		imageRef, err := eruncommon.ResolveDockerImageReference(runtime.Store, findProjectRoot, resolveBuildContext, nil, buildContext.Dir, eruncommon.DockerCommandTarget{
			ProjectRoot: projectRoot,
			Environment: environment,
		})
		if err != nil {
			return eruncommon.BuildExecutionSpec{}, err
		}
		return eruncommon.BuildExecutionSpecFromDockerBuilds([]eruncommon.DockerBuildSpec{{
			ContextDir:     eruncommon.ResolveDockerBuildContextDirForProject(buildContext.Dir, projectRoot),
			DockerfilePath: buildContext.DockerfilePath,
			Image:          imageRef,
		}}), nil
	}

	return eruncommon.ResolveBuildExecution(runtime.Store, findProjectRoot, resolveBuildContext, nil, target)
}

func resolveRuntimePushExecution(runtime RuntimeConfig, projectRoot, component string) (eruncommon.DockerPushExecutionSpec, error) {
	target := eruncommon.DockerCommandTarget{
		ProjectRoot: projectRoot,
		Environment: strings.TrimSpace(runtime.Context.Environment),
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
		build, err := eruncommon.ResolveDockerBuildForComponent(runtime.Store, findProjectRoot, resolveBuildContext, nil, projectRoot, target.Environment, component)
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
