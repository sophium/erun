package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type BuildInput struct {
	Component     string `json:"component,omitempty" jsonschema:"optional component name to build from the runtime repo root; when empty, build all Docker component images"`
	Version       string `json:"version,omitempty" jsonschema:"optional explicit image version override; disables local snapshot tagging when set"`
	Release       bool   `json:"release,omitempty" jsonschema:"when true, run release first and publish the resolved release-tagged images"`
	NoIncremental bool   `json:"noIncremental,omitempty" jsonschema:"when true, disable fingerprint-based build caching and rebuild every image from scratch"`
	Preview       bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity     int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

type PushInput struct {
	Component string `json:"component,omitempty" jsonschema:"optional component name to push; required when the runtime repo root is not itself a Docker build context"`
	Version   string `json:"version" jsonschema:"required version to publish (produced by the build tool); push publishes this version's images and chart and never mints one"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

// errMissingPushVersion is returned when the push tool is called without a
// version. push is a pure primitive: it publishes the content identity the
// build tool minted and never mints one (root AGENTS.md § "Command primitives
// vs orchestration"; erun-mcp/AGENTS.md). An Agent captures the version from
// the build tool's result and threads it here.
var errMissingPushVersion = fmt.Errorf("push requires a version: it publishes a built version's images and chart (capture the version from the build tool's result) and never mints one — set the version input")

func buildTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, BuildInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input BuildInput) (*mcp.CallToolResult, CommandOutput, error) {
		var result *eruncommon.BuildResult
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			component := strings.TrimSpace(input.Component)
			version := strings.TrimSpace(input.Version)
			execution, err := resolveRuntimeBuildExecution(runCtx, runtime, workDir, component, version, input.Release, input.NoIncremental)
			if err != nil {
				return err
			}
			// build is a pure primitive: it builds images and mints the version,
			// nothing more. MCP is a programmatic orchestration layer (erun-mcp
			// /AGENTS.md), so an agent that wants a deploy composes the primitives
			// itself — it captures the minted version from this tool's result
			// (output.Build.version), then calls push and deploy with it, rather
			// than a one-shot convenience switch.
			if err := eruncommon.RunBuildExecution(runCtx, execution, runtime.BuildScriptRunner, runtime.BuildDockerImage, runtimePushFunc(runtime)); err != nil {
				return err
			}
			built := eruncommon.NewBuildResult(execution)
			result = &built
			return nil
		})
		if err == nil {
			output.Build = result
		}
		return nil, output, err
	}
}

func pushTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PushInput) (*mcp.CallToolResult, CommandOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PushInput) (*mcp.CallToolResult, CommandOutput, error) {
		if strings.TrimSpace(input.Version) == "" {
			return nil, CommandOutput{}, errMissingPushVersion
		}
		output, err := runRuntimeCommand(runtime, input.Preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			execution, err := resolveRuntimePushExecution(runCtx, runtime, workDir, strings.TrimSpace(input.Component), strings.TrimSpace(input.Version))
			if err != nil {
				return err
			}
			return eruncommon.RunPushCommand(runCtx, func() error {
				return eruncommon.RunDockerPushExecution(runCtx, execution, runtime.BuildDockerImage, runtimePushFunc(runtime))
			})
		})
		return nil, output, err
	}
}

func resolveRuntimeBuildExecution(ctx eruncommon.Context, runtime RuntimeConfig, projectRoot, component, versionOverride string, release, noIncremental bool) (eruncommon.BuildExecutionSpec, error) {
	environment := strings.TrimSpace(runtime.Context.Environment)
	target := eruncommon.DockerCommandTarget{
		ProjectRoot:     projectRoot,
		Environment:     environment,
		VersionOverride: versionOverride,
		Release:         release,
		NoIncremental:   noIncremental,
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
			execution = eruncommon.BuildExecutionSpecWithRelease(execution, *releaseSpec)
		}
		return eruncommon.ApplyIncrementalToBuildExecution(ctx, execution, noIncremental)
	}

	return eruncommon.ResolveBuildExecution(ctx, runtime.Store, findProjectRoot, resolveBuildContext, nil, target)
}

func resolveRuntimePushExecution(ctx eruncommon.Context, runtime RuntimeConfig, projectRoot, component, versionOverride string) (eruncommon.DockerPushExecutionSpec, error) {
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
		pushInput, buildInput, err := eruncommon.ResolveDockerPushSpec(ctx, runtime.Store, findProjectRoot, resolveBuildContext, nil, target)
		if err != nil {
			return eruncommon.DockerPushExecutionSpec{}, err
		}
		builds := make([]eruncommon.DockerBuildSpec, 0, 1)
		if buildInput != nil {
			builds = append(builds, *buildInput)
		}
		return eruncommon.DockerPushExecutionSpecFromSpecs(builds, []eruncommon.DockerPushSpec{pushInput}), nil
	}

	// push builds the component image from source and pushes its multi-arch
	// manifest; it does not push a bare prebuilt tag.
	build, err := eruncommon.ResolveDockerBuildForComponent(runtime.Store, findProjectRoot, resolveBuildContext, nil, projectRoot, target.Environment, component, strings.TrimSpace(target.VersionOverride))
	if err != nil {
		return eruncommon.DockerPushExecutionSpec{}, err
	}
	if build == nil {
		return eruncommon.DockerPushExecutionSpec{}, fmt.Errorf("docker build context not found for component %q", component)
	}
	build.Push = true

	return eruncommon.DockerPushExecutionSpecFromSpecs([]eruncommon.DockerBuildSpec{*build}, []eruncommon.DockerPushSpec{
		eruncommon.NewDockerPushSpec(projectRoot, build.Image),
	}), nil
}

