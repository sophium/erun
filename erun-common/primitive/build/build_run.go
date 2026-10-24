package build

import (
	"io"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func RunDockerBuild(ctx core.Context, buildInput shared.DockerBuildSpec, build DockerImageBuilderFunc) error {
	ctx, stopProgress := withBuildProgress(ctx)
	defer stopProgress()
	traceDockerBuild(ctx, buildInput)
	if ctx.DryRun {
		return nil
	}
	return executeDockerBuild(ctx, buildInput, build, ctx.Stdout, ctx.Stderr)
}

// traceDockerBuild emits everything one build would announce, and does nothing
// else. It is separate from execution because the trace is a public contract —
// the dry-run goldens — and must stay in dependency order no matter how the
// builds themselves are scheduled.
func traceDockerBuild(ctx core.Context, buildInput shared.DockerBuildSpec) {
	buildInput.Verbosity = ctx.Verbosity
	traceIncrementalDecision(ctx, buildInput)
	for _, command := range buildInput.TraceCommands() {
		ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	}
}

// executeDockerBuild runs one build against the given streams and, once it
// succeeds, reports the component's test outcome. It is the sequential form:
// the outcome line follows the build's own output directly. The concurrent
// dispatch runs runDockerBuildStep instead and reports every outcome after the
// builds, in build order, so the stream does not depend on which image
// finished first.
func executeDockerBuild(ctx core.Context, buildInput shared.DockerBuildSpec, build DockerImageBuilderFunc, stdout, stderr io.Writer) error {
	outcome, err := runDockerBuildStep(ctx, buildInput, build, stdout, stderr)
	if err == nil {
		reportDockerBuildTestOutcome(ctx, buildInput, outcome)
	}
	return err
}

// runDockerBuildStep runs one build against the given streams. The streams are
// a parameter rather than ctx's own so a concurrent build can buffer each image
// separately and replay them in a fixed order. It returns the build's test
// outcome (see dockerBuildTestOutcome), meaningful only when err is nil.
//
// It also starts this image's step-timing child (a no-op when no timing root
// is active — see startTimingStep) and wires PlatformObserver so the builder
// reports each architecture's duration into it, tagged with the same cache
// decision the trace already names.
//
// It is also where an image joins the run's heartbeat for as long as its build
// takes, which is the only signal a run has that a long build is still working —
// see build_heartbeat.go.
func runDockerBuildStep(ctx core.Context, buildInput shared.DockerBuildSpec, build DockerImageBuilderFunc, stdout, stderr io.Writer) (string, error) {
	if build == nil {
		build = DockerImageBuilder
	}
	buildInput.Verbosity = ctx.Verbosity
	stepCtx, finish := ctx.StartTimingStep(dockerBuildStepName(buildInput))
	var cache *core.CacheDecision
	if hit, applicable, reason := incrementalCacheDecision(buildInput); applicable {
		cache = &core.CacheDecision{Hit: hit, MissReason: reason}
		stepCtx.RecordTimingCache(cache)
	}
	// The same decision reaches the builder, which is the only place that learns
	// a promote was unusable and rebuilt instead -- see settlePromoteFailure.
	buildInput.Cache = cache
	buildInput.PlatformObserver = stepCtx.TimingPlatformObserver(cache)
	// The heartbeat goes to the run's own log stream, not to stdout/stderr: those
	// are per-image buffers under a concurrent build, and a liveness line flushed
	// after the build it describes finished would report nothing.
	doneBuilding := ctx.Progress.Begin(dockerBuildStepName(buildInput))
	err := build(buildInput, stdout, stderr)
	doneBuilding()
	finish(err)
	return dockerBuildTestOutcome(buildInput, cache), err
}

func reportDockerBuildTestOutcome(ctx core.Context, buildInput shared.DockerBuildSpec, outcome string) {
	ctx.Info(dockerBuildStepName(buildInput) + ": test: " + outcome)
}

// dockerBuildTestOutcome names what a successful build established about the
// component's tests, keeping a fresh run distinct from reuse of an earlier one.
// It is read after the build, so a promote that fell back to a rebuild (see
// settlePromoteFailure) reports the tests it really ran. "passed" means the
// test stage was part of a successful docker build; BuildKit may have served
// it from its layer cache on byte-identical inputs, which is equally valid.
func dockerBuildTestOutcome(buildInput shared.DockerBuildSpec, cache *core.CacheDecision) string {
	if !shared.DockerfileDeclaresTestStage(buildInput.DockerfilePath) {
		return "none declared"
	}
	if cache != nil {
		if hit, _ := cache.Decision(); hit {
			return "reused (fingerprint " + shared.FingerprintTagPrefix + buildInput.Fingerprint + ")"
		}
	}
	return "passed"
}

// traceIncrementalDecision re-emits the fingerprint inspect already run during
// resolution so dry-run output stays complete, then names the concrete rebuild
// trigger. The per-platform detail is deliberate: a single vague "missing or
// stale" line could not tell a maintainer which fp-tag or dependency forced the
// rebuild.
func traceIncrementalDecision(ctx core.Context, buildInput shared.DockerBuildSpec) {
	if buildInput.Fingerprint == "" {
		return
	}
	missing := missingFingerprintPlatformSet(buildInput)
	for _, platform := range buildInput.Platforms {
		fpTag := shared.FingerprintTag(buildInput.Image, buildInput.Fingerprint, platform)
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

func missingFingerprintPlatformSet(build shared.DockerBuildSpec) map[string]struct{} {
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

// RunDockerBuilds builds every discovered image, running independent ones
// concurrently. Most images in a multi-image project have no edge between them,
// so the sequential loop this replaced spent most of its wall-clock waiting.
//
// Two phases, and the split is the point: every trace line is emitted first, in
// dependency order, before anything builds. That keeps dry-run output and the
// decision lines identical whatever the scheduling, so only timing changes.
func RunDockerBuilds(ctx core.Context, builds []shared.DockerBuildSpec, build DockerImageBuilderFunc) error {
	ordered := markLocalBaseImageBuilds(orderedDockerBuildSpecs(builds))
	dependencies, err := buildDependencies(ordered)
	if err != nil {
		return err
	}
	// One heartbeat for the whole run, installed before any image starts so both
	// the sequential loop below and the concurrent dispatch share it.
	ctx, stopProgress := withBuildProgress(ctx)
	defer stopProgress()
	stopDaemonEndpoint, err := ensureBuildDaemonEndpoint(ctx, ordered)
	if err != nil {
		return err
	}
	defer stopDaemonEndpoint()
	jobs := resolveBuildJobs(ctx, len(ordered))
	if jobs <= 1 {
		// Sequential keeps each image's decision lines next to its own build
		// output. Hoisting the traces here would separate a failure from the
		// image that produced it, which is a real loss and buys nothing when only
		// one thing runs at a time.
		for _, buildInput := range ordered {
			if err := RunDockerBuild(ctx, buildInput, build); err != nil {
				return err
			}
		}
		return nil
	}
	// Concurrent: the traces are hoisted ahead of every build, in dependency
	// order, because interleaved output from images racing each other would be
	// neither readable nor reproducible.
	traceBuildDependencyPlan(ctx, ordered, dependencies)
	for _, buildInput := range ordered {
		traceDockerBuild(ctx, buildInput)
	}
	if ctx.DryRun {
		return nil
	}
	return runBuildDependencies(ctx, ordered, dependencies, build, jobs)
}

// RunDockerBuildsSequentially is the same two phases with the schedule pinned to
// one at a time. The push and deploy paths use it: those builds push images and
// assemble multi-arch manifests as they go, and the release path shares a single
// in-pod docker daemon, so their concurrency is a separate question from this
// one and is deliberately not answered here.
func RunDockerBuildsSequentially(ctx core.Context, builds []shared.DockerBuildSpec, build DockerImageBuilderFunc) error {
	ctx.BuildJobs = 1
	return RunDockerBuilds(ctx, builds, build)
}

// TraceBuildUmbrella brackets a build with the `==> Building` / `==> Built`
// markers the desktop's activity-queue parser keys off to drive its spinner,
// and starts the step-timing root reported (as a duration-ordered table plus
// a JSON record) when the bracket closes. Skipped in dry-run, which does no
// work and must keep the integration goldens stable.
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
