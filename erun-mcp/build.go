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
	Jobs          int    `json:"jobs,omitempty" jsonschema:"build this many images at once; 0 resolves from the machine and 1 is sequential. Independent images build concurrently; an image that FROMs a sibling still waits for it"`
	Verbosity     int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Wait          *bool  `json:"wait,omitempty" jsonschema:"when true (the default this release), run synchronously and return the full result inline, exactly as before this input existed. Set false to start the work as a background job and get back {jobId, state: running} immediately instead -- poll exec_job_status/exec_job_await/exec_job_output for the outcome. This default flips to false in a future release, with true kept callable for one more release as the compatibility switch"`
}

type PushInput struct {
	Component string `json:"component,omitempty" jsonschema:"optional component name to push; required when the runtime repo root is not itself a Docker build context"`
	Version   string `json:"version" jsonschema:"required version to publish (produced by the build tool); push publishes this version's images and chart and never mints one"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, resolve and print the planned actions without executing them"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Wait      *bool  `json:"wait,omitempty" jsonschema:"when true (the default this release), run synchronously and return the full result inline, exactly as before this input existed. Set false to start the work as a background job and get back {jobId, state: running} immediately instead -- poll exec_job_status/exec_job_await/exec_job_output for the outcome. This default flips to false in a future release, with true kept callable for one more release as the compatibility switch"`
}

var errMissingPushVersion = fmt.Errorf("push requires a version: it publishes a built version's images and chart (capture the version from the build tool's result) and never mints one — set the version input")

func buildTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, BuildInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input BuildInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		execute := func(preview bool) (CommandOutput, error) {
			var result *eruncommon.BuildResult
			output, err := runRuntimeCommand(runtime, preview, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
				runCtx.BuildJobs = input.Jobs
				component := strings.TrimSpace(input.Component)
				version := strings.TrimSpace(input.Version)
				execution, err := resolveRuntimeBuildExecution(runCtx, runtime, workDir, component, version, input.Release, input.NoIncremental)
				if err != nil {
					return err
				}
				// build is a pure primitive that only builds and mints the version;
				// MCP composes primitives itself rather than exposing a deploy switch.
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
			return output, err
		}
		envelope, err := runJobEnvelope(runtime, "build", input.Wait, input.Preview, execute)
		return nil, envelope, err
	}
}

func pushTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, PushInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input PushInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		if strings.TrimSpace(input.Version) == "" {
			return nil, JobEnvelopeOutput{}, errMissingPushVersion
		}
		execute := simpleJobExecute(runtime, input.Verbosity, func(runCtx eruncommon.Context, workDir string) error {
			execution, err := resolveRuntimePushExecution(runCtx, runtime, workDir, strings.TrimSpace(input.Component), strings.TrimSpace(input.Version))
			if err != nil {
				return err
			}
			return eruncommon.RunPushCommand(runCtx, func(runCtx eruncommon.Context) error {
				return eruncommon.RunDockerPushExecution(runCtx, execution, runtime.BuildDockerImage, runtimePushFunc(runtime))
			})
		})
		envelope, err := runJobEnvelope(runtime, "push", input.Wait, input.Preview, execute)
		return nil, envelope, err
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

		build, err := eruncommon.ResolveDockerBuildForComponent(ctx, runtime.Store, findProjectRoot, resolveBuildContext, nil, projectRoot, environment, component, target.VersionOverride)
		if err != nil {
			return eruncommon.BuildExecutionSpec{}, err
		}
		if build == nil {
			return eruncommon.BuildExecutionSpec{}, fmt.Errorf("docker build context not found for component %q", component)
		}
		execution := eruncommon.BuildExecutionSpecFromDockerBuilds([]eruncommon.DockerBuildSpec{*build})
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

	build, err := eruncommon.ResolveDockerBuildForComponent(ctx, runtime.Store, findProjectRoot, resolveBuildContext, nil, projectRoot, target.Environment, component, strings.TrimSpace(target.VersionOverride))
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
