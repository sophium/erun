package erunmcp

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/orchestration"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/push"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

type BuildInput struct {
	Component     string   `json:"component,omitempty" jsonschema:"optional component name to build from the runtime repo root; when empty, build all Docker component images and test the linux packages"`
	Version       string   `json:"version,omitempty" jsonschema:"optional explicit image version override; disables local snapshot tagging when set"`
	Release       bool     `json:"release,omitempty" jsonschema:"when true, run release first, publish the resolved release-tagged images, and attach the linux packages' .deb files to the GitHub release for the tag"`
	NoIncremental bool     `json:"noIncremental,omitempty" jsonschema:"when true, disable fingerprint-based build caching and rebuild every image from scratch"`
	Platforms     []string `json:"platforms,omitempty" jsonschema:"optional docker --platform overrides (e.g. [\"linux/amd64\"]) for an environment that can only ever run one architecture; takes precedence over the project's configured docker.platforms (project-wide or per-environment). Mutually exclusive with release, which always publishes every platform erun supports"`
	Preview       bool     `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Jobs          int      `json:"jobs,omitempty" jsonschema:"build this many images at once; 0 resolves from the machine and 1 is sequential. Independent images build concurrently; an image that FROMs a sibling still waits for it"`
	Verbosity     int      `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	JobEnvelopeInput
}

type PushInput struct {
	Component string `json:"component,omitempty" jsonschema:"optional component name to push; required when the runtime repo root is not itself a Docker build context"`
	Version   string `json:"version" jsonschema:"required version to publish (produced by the build tool); push publishes this version's images and chart and never mints one"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	JobEnvelopeInput
}

var errMissingPushVersion = fmt.Errorf("push requires a version: it publishes a built version's images and chart (capture the version from the build tool's result) and never mints one — set the version input")

func buildTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, BuildInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input BuildInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		execute := func(preview bool, log io.Writer) (CommandOutput, error) {
			var result *core.BuildResult
			output, err := runRuntimeCommand(runtime, preview, input.Verbosity, log, func(runCtx core.Context, workDir string) error {
				runCtx.BuildJobs = input.Jobs
				runCtx.MCPTool = "build"
				component := strings.TrimSpace(input.Component)
				version := strings.TrimSpace(input.Version)
				execution, err := resolveRuntimeBuildExecution(runCtx, runtime, workDir, component, version, input.Release, input.NoIncremental, input.Platforms)
				if err != nil {
					return err
				}
				// build is a pure primitive that only builds and mints the version;
				// MCP composes primitives itself rather than exposing a deploy switch.
				if err := orchestration.RunBuildExecution(runCtx, execution, runtime.BuildScriptRunner, runtime.BuildDockerImage, runtimePushFunc(runtime), runtime.Store, cloudDependencies()); err != nil {
					return err
				}
				built := orchestration.NewBuildResult(execution)
				result = &built
				return nil
			})
			if err == nil {
				output.Build = result
			}
			return output, err
		}
		envelope, err := runJobEnvelope(runtime, "build", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}

func pushTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PushInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PushInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		if strings.TrimSpace(input.Version) == "" {
			return nil, JobEnvelopeOutput{}, errMissingPushVersion
		}
		execute := simpleJobExecute(runtime, input.Verbosity, func(runCtx core.Context, workDir string) error {
			execution, err := resolveRuntimePushExecution(runCtx, runtime, workDir, strings.TrimSpace(input.Component), strings.TrimSpace(input.Version))
			if err != nil {
				return err
			}
			return push.RunPushCommand(runCtx, func(runCtx core.Context) error {
				return orchestration.RunDockerPushExecution(runCtx, execution, runtime.BuildDockerImage, runtimePushFunc(runtime))
			})
		})
		envelope, err := runJobEnvelope(runtime, "push", input.JobEnvelopeInput, input.Preview, execute)
		return nil, envelope, err
	}
}

func resolveRuntimeBuildExecution(ctx core.Context, runtime RuntimeConfig, projectRoot, component, versionOverride string, release, noIncremental bool, platforms []string) (shared.BuildExecutionSpec, error) {
	environment := strings.TrimSpace(runtime.Context.Environment)
	target := shared.DockerCommandTarget{
		ProjectRoot:     projectRoot,
		Environment:     environment,
		VersionOverride: versionOverride,
		Release:         release,
		NoIncremental:   noIncremental,
		Platforms:       platforms,
	}
	findProjectRoot := func() (string, string, error) {
		return runtimeFindProjectRoot(runtime.Context, projectRoot)
	}
	resolveBuildContext := func() (shared.DockerBuildContext, error) {
		return shared.DockerBuildContextAtDir(projectRoot)
	}

	if component != "" {
		target, releaseSpec, err := buildpkg.ResolveDockerBuildTarget(findProjectRoot, target)
		if err != nil {
			return shared.BuildExecutionSpec{}, err
		}

		build, err := buildpkg.ResolveDockerBuildForComponent(ctx, runtime.Store, findProjectRoot, resolveBuildContext, nil, projectRoot, environment, component, target.VersionOverride, target.Platforms)
		if err != nil {
			return shared.BuildExecutionSpec{}, err
		}
		if build == nil {
			return shared.BuildExecutionSpec{}, fmt.Errorf("docker build context not found for component %q", component)
		}
		execution := buildpkg.BuildExecutionSpecFromDockerBuilds([]shared.DockerBuildSpec{*build})
		if releaseSpec != nil {
			execution = orchestration.BuildExecutionSpecWithRelease(execution, *releaseSpec)
		}
		return buildpkg.ApplyIncrementalToBuildExecution(ctx, execution, noIncremental)
	}

	return orchestration.ResolveBuildExecution(ctx, runtime.Store, findProjectRoot, resolveBuildContext, nil, target)
}

func resolveRuntimePushExecution(ctx core.Context, runtime RuntimeConfig, projectRoot, component, versionOverride string) (shared.DockerPushExecutionSpec, error) {
	target := shared.DockerCommandTarget{
		ProjectRoot:     projectRoot,
		Environment:     strings.TrimSpace(runtime.Context.Environment),
		VersionOverride: versionOverride,
	}
	findProjectRoot := func() (string, string, error) {
		return runtimeFindProjectRoot(runtime.Context, projectRoot)
	}
	resolveBuildContext := func() (shared.DockerBuildContext, error) {
		return shared.DockerBuildContextAtDir(projectRoot)
	}

	if component == "" {
		pushInput, buildInput, err := orchestration.ResolveDockerPushSpec(ctx, runtime.Store, findProjectRoot, resolveBuildContext, nil, target)
		if err != nil {
			return shared.DockerPushExecutionSpec{}, err
		}
		builds := make([]shared.DockerBuildSpec, 0, 1)
		if buildInput != nil {
			builds = append(builds, *buildInput)
		}
		return orchestration.DockerPushExecutionSpecFromSpecs(builds, []shared.DockerPushSpec{pushInput}), nil
	}

	build, err := buildpkg.ResolveDockerBuildForComponent(ctx, runtime.Store, findProjectRoot, resolveBuildContext, nil, projectRoot, target.Environment, component, strings.TrimSpace(target.VersionOverride), target.Platforms)
	if err != nil {
		return shared.DockerPushExecutionSpec{}, err
	}
	if build == nil {
		return shared.DockerPushExecutionSpec{}, fmt.Errorf("docker build context not found for component %q", component)
	}
	build.Push = true

	return orchestration.DockerPushExecutionSpecFromSpecs([]shared.DockerBuildSpec{*build}, []shared.DockerPushSpec{
		buildpkg.NewDockerPushSpec(projectRoot, build.Image),
	}), nil
}
