package erunmcp

import (
	"context"
	"fmt"
	"path/filepath"
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
			builds, err := resolveRuntimeBuilds(runtime, workDir, strings.TrimSpace(input.Component))
			if err != nil {
				return err
			}
			return eruncommon.RunDockerBuilds(runCtx, builds, runtime.BuildDockerImage)
		})
		return nil, output, err
	}
}

func pushTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PushInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PushInput) (*mcp.CallToolResult, CommandOutput, error) {
		output, err := runRuntimeCommand(runtime.Context, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			pushInput, buildInput, err := resolveRuntimePushExecution(runtime, workDir, strings.TrimSpace(input.Component))
			if err != nil {
				return err
			}
			return eruncommon.RunDockerPushSpec(
				runCtx,
				pushInput,
				buildInput,
				runtime.BuildDockerImage,
				runtimePushFunc(runtime),
			)
		})
		return nil, output, err
	}
}

func resolveRuntimeBuilds(runtime RuntimeConfig, projectRoot, component string) ([]eruncommon.DockerBuildSpec, error) {
	environment := strings.TrimSpace(runtime.Context.Environment)
	findProjectRoot := func() (string, string, error) {
		return runtimeFindProjectRoot(runtime.Context, projectRoot)
	}
	resolveBuildContext := func() (eruncommon.DockerBuildContext, error) {
		return eruncommon.DockerBuildContextAtDir(projectRoot)
	}

	if component != "" {
		buildContext, ok, err := eruncommon.FindComponentDockerBuildContext(projectRoot, component)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("docker build context not found for component %q", component)
		}
		imageRef, err := eruncommon.ResolveDockerImageReference(runtime.Store, findProjectRoot, resolveBuildContext, nil, buildContext.Dir, eruncommon.DockerCommandTarget{
			ProjectRoot: projectRoot,
			Environment: environment,
		})
		if err != nil {
			return nil, err
		}
		return []eruncommon.DockerBuildSpec{{
			ContextDir:     eruncommon.ResolveDockerBuildContextDirForProject(buildContext.Dir, projectRoot),
			DockerfilePath: buildContext.DockerfilePath,
			Image:          imageRef,
		}}, nil
	}

	rootBuildContext, err := eruncommon.DockerBuildContextAtDir(projectRoot)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rootBuildContext.DockerfilePath) != "" {
		return eruncommon.ResolveCurrentDockerBuildSpecs(runtime.Store, findProjectRoot, resolveBuildContext, nil, eruncommon.DockerCommandTarget{
			ProjectRoot: projectRoot,
			Environment: environment,
		})
	}

	dockerModuleDir := filepath.Join(projectRoot, "docker")
	resolveDockerModuleContext := func() (eruncommon.DockerBuildContext, error) {
		return eruncommon.DockerBuildContext{Dir: dockerModuleDir}, nil
	}
	return eruncommon.ResolveCurrentDockerBuildSpecs(runtime.Store, findProjectRoot, resolveDockerModuleContext, nil, eruncommon.DockerCommandTarget{
		ProjectRoot: projectRoot,
		Environment: environment,
	})
}

func resolveRuntimePushExecution(runtime RuntimeConfig, projectRoot, component string) (eruncommon.DockerPushSpec, *eruncommon.DockerBuildSpec, error) {
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

	rootBuildContext, err := eruncommon.DockerBuildContextAtDir(projectRoot)
	if err != nil {
		return eruncommon.DockerPushSpec{}, nil, err
	}

	if component == "" {
		if strings.TrimSpace(rootBuildContext.DockerfilePath) == "" {
			return eruncommon.DockerPushSpec{}, nil, fmt.Errorf("component is required when the runtime repo root is not a Docker build context")
		}
		return eruncommon.ResolveDockerPushSpec(runtime.Store, findProjectRoot, resolveBuildContext, nil, target)
	}

	buildContext, ok, err := eruncommon.FindComponentDockerBuildContext(projectRoot, component)
	if err != nil {
		return eruncommon.DockerPushSpec{}, nil, err
	}
	if !ok {
		return eruncommon.DockerPushSpec{}, nil, fmt.Errorf("docker build context not found for component %q", component)
	}

	imageRef, err := eruncommon.ResolveDockerImageReference(runtime.Store, findProjectRoot, resolveBuildContext, nil, buildContext.Dir, target)
	if err != nil {
		return eruncommon.DockerPushSpec{}, nil, err
	}

	var buildInput *eruncommon.DockerBuildSpec
	if imageRef.IsLocalBuild {
		build, err := eruncommon.ResolveDockerBuildForComponent(runtime.Store, findProjectRoot, resolveBuildContext, nil, projectRoot, target.Environment, component)
		if err != nil {
			return eruncommon.DockerPushSpec{}, nil, err
		}
		if build == nil {
			return eruncommon.DockerPushSpec{}, nil, fmt.Errorf("docker build context not found for component %q", component)
		}
		buildInput = build
		imageRef = build.Image
	}

	return eruncommon.NewDockerPushSpec(projectRoot, imageRef), buildInput, nil
}
