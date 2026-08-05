package eruncommon

import (
	"fmt"
	"strings"
	"time"
)

func RunDockerBuild(ctx Context, buildInput DockerBuildSpec, build DockerImageBuilderFunc) error {
	if build == nil {
		build = DockerImageBuilder
	}
	buildInput.Verbosity = ctx.Verbosity
	traceIncrementalDecision(ctx, buildInput)
	for _, command := range buildInput.traceCommands() {
		ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	}
	if ctx.DryRun {
		return nil
	}
	return build(buildInput, ctx.Stdout, ctx.Stderr)
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

func RunDockerBuilds(ctx Context, builds []DockerBuildSpec, build DockerImageBuilderFunc) error {
	for _, buildInput := range markLocalBaseImageBuilds(orderedDockerBuildSpecs(builds)) {
		if err := RunDockerBuild(ctx, buildInput, build); err != nil {
			return err
		}
	}
	return nil
}

// traceBuildUmbrella brackets a build with the `==> Building` / `==> Built`
// markers the desktop's activity-queue parser keys off to drive its spinner.
// Skipped in dry-run, which does no work and must keep the integration goldens
// stable.
func traceBuildUmbrella(ctx Context) func(*error) {
	if ctx.DryRun {
		return func(*error) {}
	}
	started := time.Now()
	ctx.Info("==> Building")
	return func(errp *error) {
		elapsed := time.Since(started).Round(time.Second)
		if errp != nil && *errp != nil {
			ctx.Info("==> Build failed after " + elapsed.String())
			return
		}
		ctx.Info("==> Built in " + elapsed.String())
	}
}

func RunBuildExecution(ctx Context, execution BuildExecutionSpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc) error {
	return runBuildExecution(ctx, execution, nil, runScript, build, push, nil)
}

func RunBuildExecutionAndDeploy(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc, deploy HelmChartDeployerFunc) error {
	return runBuildExecution(ctx, execution, deploySpecs, runScript, build, push, deploy)
}

func runBuildExecution(ctx Context, execution BuildExecutionSpec, deploySpecs []DeploySpec, runScript BuildScriptRunnerFunc, build DockerImageBuilderFunc, push DockerPushFunc, deploy HelmChartDeployerFunc) (err error) {
	defer traceBuildUmbrella(ctx)(&err)
	if execution.release != nil {
		// The exported RunReleaseSpec would emit its own `==> Releasing`
		// markers, double-registering activity under the `==> Building`
		// umbrella already active for this `erun build --release`.
		if err = runReleaseSpec(ctx, *execution.release, nil, runScript, nil); err != nil {
			return err
		}
	}
	if execution.skippedLinux {
		ctx.Trace("skipping linux package scripts: host is not Linux or dpkg-deb is unavailable")
	}

	if _, err = runBuildExecutionBuilds(ctx, execution, deploySpecs, runScript, build, push); err != nil {
		return err
	}
	// build+push above already published the images and runtime chart, so
	// these pure deploy specs only run helm.
	for _, deploySpec := range deploySpecs {
		if err = RunDeploySpec(ctx, deploySpec, deploy); err != nil {
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
	if err := RunDockerBuilds(ctx, builds, build); err != nil {
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
// `==> Pushed` markers the desktop's activity-queue parser keys off. Only the
// standalone push entrypoints route through here: pushes inside `erun build`
// already sit under the `==> Building` umbrella, and the per-image push
// executors would fire a marker per image and double-count if bracketed here.
// Dry-run does no work.
func RunPushCommand(ctx Context, op func() error) (err error) {
	if ctx.DryRun {
		return op()
	}
	started := time.Now()
	ctx.Info("==> Pushing")
	defer func() {
		elapsed := time.Since(started).Round(time.Second)
		if err != nil {
			ctx.Info("==> Push failed after " + elapsed.String())
			return
		}
		ctx.Info("==> Pushed in " + elapsed.String())
	}()
	return op()
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
	if err := RunDockerBuilds(ctx, execution.builds, build); err != nil {
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

// publishComponentCharts packages+pushes then verifies each resolved chart.
func publishComponentCharts(ctx Context, specs []HelmChartPublishSpec) error {
	published := make([]string, 0, len(specs))
	for i, spec := range specs {
		spec.Verbosity = ctx.Verbosity
		if err := RunHelmChartPublish(ctx, spec); err != nil {
			return newPartialChartPublishError(spec, published, specs[i+1:], err)
		}
		if err := VerifyPublishedHelmChart(ctx, spec.OCIRepo, spec.ChartName, spec.Version); err != nil {
			return newPartialChartPublishError(spec, published, specs[i+1:], err)
		}
		published = append(published, spec.ChartName)
	}
	return nil
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
