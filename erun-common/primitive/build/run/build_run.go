package run

import (
	"io"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
	"github.com/sophium/erun/erun-common/primitive/build/dockerimage"
	"github.com/sophium/erun/erun-common/primitive/build/order"
	"github.com/sophium/erun/erun-common/primitive/build/progress"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

func RunDockerBuild(ctx core.Context, buildInput imagebuild.DockerBuildSpec, build imagebuild.DockerImageBuilderFunc) error {
	ctx, stopProgress := progress.WithBuildProgress(ctx)
	defer stopProgress()
	traceDockerBuild(ctx, buildInput)
	if ctx.DryRun {
		return nil
	}
	return executeDockerBuild(ctx, buildInput, build, ctx.Stdout, ctx.Stderr)
}

// traceDockerBuild emits one build's trace lines; the dry-run goldens require dependency order.
func traceDockerBuild(ctx core.Context, buildInput imagebuild.DockerBuildSpec) {
	buildInput.Verbosity = ctx.Verbosity
	traceIncrementalDecision(ctx, buildInput)
	for _, command := range dockerspec.DockerBuildTraceCommands(buildInput) {
		ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	}
}

// executeDockerBuild runs one build and reports its test outcome right after its output.
func executeDockerBuild(ctx core.Context, buildInput imagebuild.DockerBuildSpec, build imagebuild.DockerImageBuilderFunc, stdout, stderr io.Writer) error {
	outcome, err := runDockerBuildStep(ctx, buildInput, build, stdout, stderr)
	if err == nil {
		reportDockerBuildTestOutcome(ctx, buildInput, outcome)
	}
	return err
}

// runDockerBuildStep runs one build against the given streams, so concurrent builds can buffer
// per image. The returned test outcome is meaningful only when err is nil.
func runDockerBuildStep(ctx core.Context, buildInput imagebuild.DockerBuildSpec, build imagebuild.DockerImageBuilderFunc, stdout, stderr io.Writer) (string, error) {
	if build == nil {
		build = dockerimage.DockerImageBuilder
	}
	buildInput.Verbosity = ctx.Verbosity
	stepCtx, finish := ctx.StartTimingStep(dockerBuildStepName(buildInput))
	var cache *core.CacheDecision
	if hit, applicable, reason := incrementalCacheDecision(buildInput); applicable {
		cache = &core.CacheDecision{Hit: hit, MissReason: reason}
		stepCtx.RecordTimingCache(cache)
	}
	// The builder is the only place that learns a promote fell back to a rebuild (settlePromoteFailure).
	buildInput.Cache = cache
	buildInput.PlatformObserver = stepCtx.TimingPlatformObserver(cache)
	// Stdout/stderr may be per-image buffers; a delayed liveness line would report nothing.
	doneBuilding := ctx.Progress.Begin(dockerBuildStepName(buildInput))
	err := build(buildInput, stdout, stderr)
	doneBuilding()
	finish(err)
	return dockerBuildTestOutcome(buildInput, cache), err
}

func reportDockerBuildTestOutcome(ctx core.Context, buildInput imagebuild.DockerBuildSpec, outcome string) {
	ctx.Info(dockerBuildStepName(buildInput) + ": test: " + outcome)
}

// dockerBuildTestOutcome names what a successful build established about the component's tests.
// "passed" may be served from BuildKit's layer cache, which is equally valid.
func dockerBuildTestOutcome(buildInput imagebuild.DockerBuildSpec, cache *core.CacheDecision) string {
	if !dockerspec.DockerfileDeclaresTestStage(buildInput.DockerfilePath) {
		return "none declared"
	}
	if cache != nil {
		if hit, _ := cache.Decision(); hit {
			return "reused (fingerprint " + dockerspec.FingerprintTagPrefix + buildInput.Fingerprint + ")"
		}
	}
	return "passed"
}

// traceIncrementalDecision re-emits the fingerprint inspect and names the concrete rebuild trigger.
func traceIncrementalDecision(ctx core.Context, buildInput imagebuild.DockerBuildSpec) {
	if buildInput.Fingerprint == "" {
		return
	}
	missing := missingFingerprintPlatformSet(buildInput)
	for _, platform := range buildInput.Platforms {
		fpTag := dockerspec.FingerprintTag(buildInput.Image, buildInput.Fingerprint, platform)
		ctx.TraceCommand("", "docker", "image", "inspect", fpTag)
		if _, isMissing := missing[platform]; isMissing {
			ctx.Trace("fingerprint image not found locally: " + fpTag)
		} else {
			ctx.Trace("fingerprint image present locally: " + fpTag)
		}
	}
	tag := strings.TrimSpace(buildInput.Image.Tag)
	switch {
	case buildInput.Promote:
		ctx.Trace("promoting from cached fingerprint image: " + tag)
	case strings.TrimSpace(buildInput.CascadeRebuildFromTag) != "":
		ctx.Trace("rebuilding " + tag + " because dependency " + strings.TrimSpace(buildInput.CascadeRebuildFromTag) + " is rebuilding")
	case len(buildInput.MissingFingerprintPlatforms) > 0:
		ctx.Trace("rebuilding " + tag + " because fingerprint image is missing for " + describeMissingPlatforms(buildInput.MissingFingerprintPlatforms))
	default:
		ctx.Trace("rebuilding " + tag + " (no cached fingerprint image)")
	}
}

func missingFingerprintPlatformSet(build imagebuild.DockerBuildSpec) map[string]struct{} {
	if len(build.MissingFingerprintPlatforms) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(build.MissingFingerprintPlatforms))
	for _, platform := range build.MissingFingerprintPlatforms {
		out[platform] = struct{}{}
	}
	return out
}

func describeMissingPlatforms(platforms []string) string {
	labels := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		if strings.TrimSpace(platform) == "" {
			labels = append(labels, "<no platform>")
			continue
		}
		labels = append(labels, platform)
	}
	if len(labels) == 1 {
		return "platform " + labels[0]
	}
	return "platforms [" + strings.Join(labels, ", ") + "]"
}

// RunDockerBuilds builds every discovered image, independent ones concurrently. Trace lines are
// emitted in dependency order before anything builds, so only timing varies.
func RunDockerBuilds(ctx core.Context, builds []imagebuild.DockerBuildSpec, build imagebuild.DockerImageBuilderFunc) error {
	ordered := order.MarkLocalBaseImageBuilds(order.OrderedDockerBuildSpecs(builds))
	dependencies, err := buildDependencies(ordered)
	if err != nil {
		return err
	}
	ctx, stopProgress := progress.WithBuildProgress(ctx)
	defer stopProgress()
	stopDaemonEndpoint, err := ensureBuildDaemonEndpoint(ctx, ordered)
	if err != nil {
		return err
	}
	defer stopDaemonEndpoint()
	jobs := resolveBuildJobs(ctx, len(ordered))
	if jobs <= 1 {
		// Sequential keeps each image's decision lines next to its own build output.
		for _, buildInput := range ordered {
			if err := RunDockerBuild(ctx, buildInput, build); err != nil {
				return err
			}
		}
		return nil
	}
	traceBuildDependencyPlan(ctx, ordered, dependencies)
	for _, buildInput := range ordered {
		traceDockerBuild(ctx, buildInput)
	}
	if ctx.DryRun {
		return nil
	}
	return runBuildDependencies(ctx, ordered, dependencies, build, jobs)
}

// RunDockerBuildsSequentially builds one image at a time, for the push and deploy paths that
// push and assemble manifests as they go.
func RunDockerBuildsSequentially(ctx core.Context, builds []imagebuild.DockerBuildSpec, build imagebuild.DockerImageBuilderFunc) error {
	ctx.BuildJobs = 1
	return RunDockerBuilds(ctx, builds, build)
}

// TraceBuildUmbrella brackets a build with the `==> Building` / `==> Built` markers the desktop
// parses, and starts the step-timing root. Skipped in dry-run.
func TraceBuildUmbrella(ctx core.Context) (core.Context, func(*error)) {
	if ctx.DryRun {
		return ctx, func(*error) {}
	}
	started := time.Now()
	ctx.Info("==> Building")
	root := core.NewStepTiming("build", nil)
	ctx.Timing = root
	return ctx, func(errp *error) {
		var err error
		if errp != nil {
			err = *errp
		}
		root.Finish(err)
		elapsed := time.Since(started).Round(time.Second)
		if err != nil {
			ctx.Info("==> Build failed after " + elapsed.String())
		} else {
			ctx.Info("==> Built in " + elapsed.String())
		}
		core.ReportStepTiming(ctx, "build", root)
	}
}
