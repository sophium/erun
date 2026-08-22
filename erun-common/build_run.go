package eruncommon

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

func RunDockerBuild(ctx Context, buildInput DockerBuildSpec, build DockerImageBuilderFunc) error {
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
func traceDockerBuild(ctx Context, buildInput DockerBuildSpec) {
	buildInput.Verbosity = ctx.Verbosity
	traceIncrementalDecision(ctx, buildInput)
	for _, command := range buildInput.traceCommands() {
		ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	}
}

// executeDockerBuild runs one build against the given streams. The streams are
// a parameter rather than ctx's own so a concurrent wave can buffer each image
// separately and replay them in a fixed order.
//
// It also starts this image's step-timing child (a no-op when no timing root
// is active — see startTimingStep) and wires PlatformObserver so the builder
// reports each architecture's duration into it, tagged with the same cache
// decision the trace already names.
func executeDockerBuild(ctx Context, buildInput DockerBuildSpec, build DockerImageBuilderFunc, stdout, stderr io.Writer) error {
	if build == nil {
		build = DockerImageBuilder
	}
	buildInput.Verbosity = ctx.Verbosity
	stepCtx, finish := ctx.startTimingStep(dockerBuildStepName(buildInput))
	var cache *cacheDecision
	if hit, applicable, reason := incrementalCacheDecision(buildInput); applicable {
		cache = &cacheDecision{hit: hit, missReason: reason}
		stepCtx.recordTimingCache(hit, reason)
	}
	buildInput.PlatformObserver = stepCtx.timingPlatformObserver(cache)
	err := build(buildInput, stdout, stderr)
	finish(err)
	return err
}

// traceIncrementalDecision re-emits the fingerprint inspect already run during
// resolution so dry-run output stays complete, then names the concrete rebuild
// trigger. The per-platform detail is deliberate: a single vague "missing or
// stale" line could not tell a maintainer which fp-tag or dependency forced the
// rebuild.
func traceIncrementalDecision(ctx Context, buildInput DockerBuildSpec) {
	if buildInput.Fingerprint == "" {
		return
	}
	missing := missingFingerprintPlatformSet(buildInput)
	for _, platform := range buildInput.Platforms {
		fpTag := fingerprintTag(buildInput.Image, buildInput.Fingerprint, platform)
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

func missingFingerprintPlatformSet(build DockerBuildSpec) map[string]struct{} {
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
func RunDockerBuilds(ctx Context, builds []DockerBuildSpec, build DockerImageBuilderFunc) error {
	ordered := markLocalBaseImageBuilds(orderedDockerBuildSpecs(builds))
	waves, err := resolveBuildWaves(ordered)
	if err != nil {
		return err
	}
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
	traceBuildWavePlan(ctx, waves)
	for _, buildInput := range ordered {
		traceDockerBuild(ctx, buildInput)
	}
	if ctx.DryRun {
		return nil
	}
	return runBuildWaves(ctx, waves, build, jobs)
}

// runDockerBuildsSequentially is the same two phases with the schedule pinned to
// one at a time. The push and deploy paths use it: those builds push images and
// assemble multi-arch manifests as they go, and the release path shares a single
// in-pod docker daemon, so their concurrency is a separate question from this
// one and is deliberately not answered here.
func runDockerBuildsSequentially(ctx Context, builds []DockerBuildSpec, build DockerImageBuilderFunc) error {
	ctx.BuildJobs = 1
	return RunDockerBuilds(ctx, builds, build)
}

// traceBuildUmbrella brackets a build with the `==> Building` / `==> Built`
// markers the desktop's activity-queue parser keys off to drive its spinner,
// and starts the step-timing root reported (as a duration-ordered table plus
// a JSON record) when the bracket closes. Skipped in dry-run, which does no
// work and must keep the integration goldens stable.
func traceBuildUmbrella(ctx Context) (Context, func(*error)) {
	if ctx.DryRun {
		return ctx, func(*error) {}
	}
	started := time.Now()
	ctx.Info("==> Building")
	root := newStepTiming("build", nil)
	ctx.timing = root
	return ctx, func(errp *error) {
		var err error
		if errp != nil {
			err = *errp
		}
		root.finish(err)
		elapsed := time.Since(started).Round(time.Second)
		if err != nil {
			ctx.Info("==> Build failed after " + elapsed.String())
		} else {
			ctx.Info("==> Built in " + elapsed.String())
		}
		reportStepTiming(ctx, "build", root)
	}
}

func RunBuildExecution(ctx Context, execution BuildExecutionSpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc) (err error) {
	ctx, finish := traceBuildUmbrella(ctx)
	defer finish(&err)
	return runBuildExecution(ctx, execution, nil, nil, runScript, build, push, nil)
}

func RunBuildExecutionAndDeploy(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc, deploy HelmChartDeployerFunc) (err error) {
	ctx, finish := traceBuildUmbrella(ctx)
	defer finish(&err)
	return runBuildExecution(ctx, execution, deploySpecs, nil, runScript, build, push, deploy)
}

// RunReleaseExecution is a standalone `erun release`: the same build → publish →
// tag orchestration `erun build --release` performs, under the `==> Releasing`
// umbrella the desktop activity queue expects for a release rather than the
// `==> Building` one. Both entrypoints share one execution so the flow cannot
// drift between them.
func RunReleaseExecution(ctx Context, execution BuildExecutionSpec, runGit GitCommandRunnerFunc, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc) (err error) {
	ctx, finish := traceReleaseUmbrella(ctx, releaseExecutionVersion(execution))
	defer finish(&err)
	return runBuildExecution(ctx, execution, nil, runGit, runScript, build, push, nil)
}

func runBuildExecution(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, runGit GitCommandRunnerFunc, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc, deploy HelmChartDeployerFunc) error {
	if execution.release != nil {
		// The release owns the publish rather than following it: its stages run
		// around the build+push so the version's images and charts exist, and
		// resolve, before the tag and branch pushes make it public.
		publisher := newReleasePublisher(execution, deploySpecs, runScript, build, push)
		if err := runReleaseSpec(ctx, *execution.release, runGit, runScript, nil, publisher); err != nil {
			return err
		}
	} else {
		if execution.skippedLinux {
			ctx.Trace("skipping linux package scripts: host is not Linux or dpkg-deb is unavailable")
		}
		if _, err := runBuildExecutionBuilds(ctx, execution, deploySpecs, runScript, build, push); err != nil {
			return err
		}
	}
	// build+push above already published the images and runtime chart, so
	// these pure deploy specs only run helm.
	for _, deploySpec := range deploySpecs {
		if err := RunDeploySpec(ctx, deploySpec, deploy); err != nil {
			return err
		}
	}
	if execution.release != nil {
		ctx.Info("release version: " + execution.release.Version)
	}
	if version := deployedVersionForSpecs(deploySpecs); version != "" {
		ctx.Info("deployed version: " + version)
	}
	return nil
}

// BuildExecutionReleaseSpec exposes the release a build execution carries, for
// transports that report the resolved plan alongside the run.
func BuildExecutionReleaseSpec(execution BuildExecutionSpec) (ReleaseSpec, bool) {
	if execution.release == nil {
		return ReleaseSpec{}, false
	}
	return *execution.release, true
}

func releaseExecutionVersion(execution BuildExecutionSpec) string {
	if execution.release == nil {
		return ""
	}
	return execution.release.Version
}

func runBuildExecutionBuilds(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc) (map[string]struct{}, error) {
	pushedTags := make(map[string]struct{}, len(execution.dockerBuilds)+len(execution.dockerPushes))
	if execution.script != nil {
		if len(deploySpecs) > 0 {
			return nil, fmt.Errorf("build deploy is not supported for project build scripts")
		}
		return pushedTags, runScriptSpec(ctx, *execution.script, runScript)
	}
	if err := runScriptSpecs(ctx, execution.linuxBuilds, runScript); err != nil {
		return nil, err
	}
	return runDockerBuildExecutionPhase(ctx, execution, deploySpecs, build, push, pushedTags)
}

func runDockerBuildExecutionPhase(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, build DockerImageBuilderFunc, push DockerPushFunc, pushedTags map[string]struct{}) (map[string]struct{}, error) {
	if len(execution.dockerPushes) > 0 {
		err := RunDockerPushExecution(ctx, DockerPushExecutionSpec{builds: execution.dockerBuilds, pushes: execution.dockerPushes, componentCharts: execution.componentCharts}, build, push)
		if err != nil {
			return pushedTags, err
		}
		return recordDockerPushTags(pushedTags, execution.dockerPushes), nil
	}
	if len(deploySpecs) > 0 {
		if err := buildAndPushDeployDockerImages(ctx, execution.dockerBuilds, build, push, pushedTags); err != nil {
			return pushedTags, err
		}
		// A builds-here --deploy pushes images but deploys charts from the working
		// tree, so package (validate) the charts rather than publish them.
		return pushedTags, packageComponentCharts(ctx, execution.componentCharts)
	}
	if err := RunDockerBuilds(ctx, execution.dockerBuilds, build); err != nil {
		return pushedTags, err
	}
	return pushedTags, packageComponentCharts(ctx, execution.componentCharts)
}

func recordDockerPushTags(tags map[string]struct{}, pushes []DockerPushSpec) map[string]struct{} {
	for _, pushInput := range pushes {
		tags[pushInput.Image.Tag] = struct{}{}
	}
	return tags
}

func buildAndPushDeployDockerImages(ctx Context, builds []DockerBuildSpec, build DockerImageBuilderFunc, push DockerPushFunc, pushedTags map[string]struct{}) error {
	if err := runDockerBuildsSequentially(ctx, builds, build); err != nil {
		return err
	}
	for _, buildInput := range builds {
		pushInput := NewDockerPushSpec(buildInput.ContextDir, buildInput.Image)
		if err := RunDockerPushSpec(ctx, pushInput, nil, build, push); err != nil {
			return err
		}
		pushedTags[pushInput.Image.Tag] = struct{}{}
	}
	return nil
}

func deployedVersionForSpecs(specs []DeploySpec) string {
	version := ""
	for _, spec := range specs {
		current := strings.TrimSpace(spec.Deploy.Version)
		if current == "" {
			return ""
		}
		if version == "" {
			version = current
			continue
		}
		if current != version {
			return ""
		}
	}
	return version
}

// RunPushCommand brackets a standalone `erun push` with the `==> Pushing` /
// `==> Pushed` markers the desktop's activity-queue parser keys off, and
// starts the step-timing root reported when the bracket closes. Only the
// standalone push entrypoints route through here: pushes inside `erun build`
// already sit under the `==> Building` umbrella, and the per-image push
// executors would fire a marker per image and double-count if bracketed here.
// op receives the timing-scoped context so the images and charts it pushes
// attach their own step-timing children; dry-run does no work and skips
// timing, same as the other three umbrellas.
func RunPushCommand(ctx Context, op func(Context) error) (err error) {
	if ctx.DryRun {
		return op(ctx)
	}
	started := time.Now()
	ctx.Info("==> Pushing")
	root := newStepTiming("push", nil)
	ctx.timing = root
	defer func() {
		root.finish(err)
		elapsed := time.Since(started).Round(time.Second)
		if err != nil {
			ctx.Info("==> Push failed after " + elapsed.String())
		} else {
			ctx.Info("==> Pushed in " + elapsed.String())
		}
		reportStepTiming(ctx, "push", root)
	}()
	return op(ctx)
}

func RunDockerPush(ctx Context, pushInput DockerPushSpec, push DockerImagePusherFunc) error {
	if push == nil {
		push = DockerImagePusher
	}
	pushInput.Verbosity = ctx.Verbosity
	command := pushInput.command()
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	if ctx.DryRun {
		return nil
	}
	return push(pushInput.Image.Tag, ctx.Verbosity, ctx.Stdout, ctx.Stderr)
}

func RunDockerPushSpec(ctx Context, pushInput DockerPushSpec, buildInput *DockerBuildSpec, build DockerImageBuilderFunc, push DockerPushFunc) error {
	if buildInput != nil {
		if err := RunDockerBuild(ctx, *buildInput, build); err != nil {
			return err
		}
		if buildInput.Push {
			// The multi-arch build already pushed the image, so only the chart
			// remains; a single-image push aligns chart and image versions.
			return publishComponentChart(ctx, buildInput.Image, buildInput.Image.Version)
		}
	}
	if push == nil {
		push = func(ctx Context, pushInput DockerPushSpec) error {
			return RunDockerPush(ctx, pushInput, nil)
		}
	}
	if err := push(ctx, pushInput); err != nil {
		return err
	}
	return publishComponentChart(ctx, pushInput.Image, pushInput.Image.Version)
}

func RunDockerPushExecution(ctx Context, execution DockerPushExecutionSpec, build DockerImageBuilderFunc, push DockerPushFunc) error {
	if err := preflightRegistryPushAccess(ctx, execution); err != nil {
		return err
	}
	if err := runDockerBuildsSequentially(ctx, execution.builds, build); err != nil {
		return err
	}
	builtAndPushedTags := make(map[string]struct{}, len(execution.builds))
	for _, buildInput := range execution.builds {
		if buildInput.Push {
			builtAndPushedTags[buildInput.Image.Tag] = struct{}{}
		}
	}
	for _, pushInput := range execution.pushes {
		if _, ok := builtAndPushedTags[pushInput.Image.Tag]; ok {
			continue
		}
		if err := RunDockerPushSpec(ctx, pushInput, nil, build, push); err != nil {
			return err
		}
	}
	// Publish every chart under <tenant>-devops/k8s/* at the push/release version,
	// discovered by directory scan rather than keyed to same-named images, so
	// image-less component charts (a tenant's wrappers) publish too.
	return publishComponentCharts(ctx, execution.componentCharts)
}

// preflightRegistryPushAccess refuses a publish that is already known to be
// impossible, before it spends the build.
//
// A release that cannot push otherwise discovers it at the push, after every
// image has been built for every architecture, and then offers an interactive
// login that a detached job or an agent run can never answer. The registry
// credential is knowable up front, so it is checked up front — the same class of
// upfront refusal as a release whose images are not covered by a build it will
// publish.
//
// Two checks run here, and they answer different questions: VerifyGHCRPushScope
// is a per-registry check of the credential's own write:packages scope (cheap,
// one request per registry); VerifyGHCRCanPushImage/VerifyGHCRCanPushChart is a
// per-artifact check of whether the registry would actually grant push for that
// specific repository, including creating it for the first time — a token can
// have write:packages and still be denied create_package by org policy on a
// component nothing has ever published before, which the scope check alone
// cannot see.
//
// Skipped in dry-run, which does no work and must keep the integration traces
// stable.
func preflightRegistryPushAccess(ctx Context, execution DockerPushExecutionSpec) error {
	if ctx.DryRun {
		return nil
	}
	checker := &registryPushAccessChecker{
		checkedRegistries: make(map[string]struct{}, 2),
		checkedTags:       make(map[string]struct{}, len(execution.builds)+len(execution.pushes)),
	}
	// A build that pushes inline (buildInput.Push) and a promoted fingerprint
	// push (execution.pushes) are both real pushes this run will make — the
	// same union RunDockerPushExecution treats as "will be pushed" just below
	// this preflight, so both need the same check.
	for _, buildInput := range execution.builds {
		if !buildInput.Push {
			continue
		}
		if err := checker.checkImageTag(buildInput.Image.Tag); err != nil {
			return err
		}
	}
	for _, pushInput := range execution.pushes {
		if err := checker.checkImageTag(pushInput.Image.Tag); err != nil {
			return err
		}
	}
	for _, chart := range execution.componentCharts {
		if err := VerifyGHCRCanPushChart(context.Background(), nil, chart.OCIRepo, chart.ChartName); err != nil {
			return err
		}
	}
	return nil
}

// registryPushAccessChecker dedups the two per-image checks preflightRegistryPushAccess
// runs, so a version with the same tag built for multiple platforms is only
// checked once.
type registryPushAccessChecker struct {
	checkedRegistries map[string]struct{}
	checkedTags       map[string]struct{}
}

func (c *registryPushAccessChecker) checkImageTag(tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil
	}
	if _, seen := c.checkedTags[tag]; seen {
		return nil
	}
	c.checkedTags[tag] = struct{}{}
	if err := c.checkRegistryScopeOnce(tag); err != nil {
		return err
	}
	return VerifyGHCRCanPushImage(context.Background(), nil, tag)
}

func (c *registryPushAccessChecker) checkRegistryScopeOnce(tag string) error {
	registry := dockerRegistryFromImageTag(tag)
	if registry == "" {
		return nil
	}
	if _, seen := c.checkedRegistries[registry]; seen {
		return nil
	}
	c.checkedRegistries[registry] = struct{}{}
	return VerifyGHCRPushScope(context.Background(), nil, tag)
}

// publishComponentCharts packages+pushes then verifies each resolved chart.
func publishComponentCharts(ctx Context, specs []HelmChartPublishSpec) error {
	published := make([]string, 0, len(specs))
	for i, spec := range specs {
		spec.Verbosity = ctx.Verbosity
		stepCtx, finish := ctx.startTimingStep("chart " + spec.ChartName)
		err := publishAndVerifyHelmChart(stepCtx, spec)
		finish(err)
		if err != nil {
			return newPartialChartPublishError(spec, published, specs[i+1:], err)
		}
		published = append(published, spec.ChartName)
	}
	return nil
}

func publishAndVerifyHelmChart(ctx Context, spec HelmChartPublishSpec) error {
	if err := RunHelmChartPublish(ctx, spec); err != nil {
		return err
	}
	return VerifyPublishedHelmChart(ctx, spec.OCIRepo, spec.ChartName, spec.Version)
}

// newPartialChartPublishError reports a chart publish that stopped mid-set. By
// the time charts publish, the version's images — and, for a release, its git tag
// and GitHub release — are already public, so aborting here leaves a version whose
// consumers cannot resolve every chart. The operator needs the exact split to
// recover, and re-running push is the recovery: publishing a chart already
// published is harmless.
func newPartialChartPublishError(failed HelmChartPublishSpec, published []string, remaining []HelmChartPublishSpec, err error) error {
	notAttempted := make([]string, 0, len(remaining))
	for _, spec := range remaining {
		notAttempted = append(notAttempted, spec.ChartName)
	}
	return fmt.Errorf("%w\npublishing charts at %s stopped at %s, so version %s is only partially published:\n  published: %s\n  failed: %s\n  not attempted: %s\nre-run `erun push --version %s` to publish the rest; republishing an already-published chart is safe",
		err,
		failed.Version, failed.ChartName, failed.Version,
		describeChartNames(published),
		failed.ChartName,
		describeChartNames(notAttempted),
		failed.Version)
}

func describeChartNames(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// packageComponentCharts packages each resolved chart locally (validate + record
// identity) without publishing — the build-only counterpart of
// publishComponentCharts.
func packageComponentCharts(ctx Context, specs []HelmChartPublishSpec) error {
	for _, spec := range specs {
		if err := PackageResolvedChart(ctx, spec); err != nil {
			return err
		}
	}
	return nil
}

// componentChartPublishVersion is the release version every component chart
// publishes at. Version-pinned bases (VersionFromBuildDir, e.g. erun-powerdns,
// erun-backend-postgres) keep their upstream image pin and are skipped; the
// always-present, non-pinned runtime (erun-devops) guarantees a match. An empty
// result lets each chart fall back to its own image version.
func componentChartPublishVersion(builds []DockerBuildSpec) string {
	for _, buildInput := range builds {
		if buildInput.Image.VersionFromBuildDir {
			continue
		}
		if version := strings.TrimSpace(buildInput.Image.Version); version != "" {
			return version
		}
	}
	return ""
}
