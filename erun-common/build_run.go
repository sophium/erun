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

// traceIncrementalDecision emits the "<inspect> + <reason>" trace pattern for
// the fingerprint-based incremental path. The inspect was already executed
// during resolution (applyIncrementalPromotion), but emitting it here keeps
// dry-run output complete. Builds without a fingerprint (incremental disabled,
// or no fp tag was looked up) produce no extra trace.
//
// The trace is intentionally explicit: each fp-tag inspect line is followed
// by a "found"/"missing" result line, and the summary line names the actual
// trigger (specific platforms missing, or a cascading dependency rebuild).
// "rebuilding because cached fingerprint image is missing or stale" was too
// vague to debug — a maintainer could not tell from the trace which fp-tag
// failed to look up or whether the rebuild was driven by a FROM dependency.
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
	for _, buildInput := range orderedDockerBuildSpecs(builds) {
		if err := RunDockerBuild(ctx, buildInput, build); err != nil {
			return err
		}
	}
	return nil
}

// traceBuildUmbrella emits the `==> Building` start marker and returns a finish
// func to defer with the run's error pointer; the finish func emits
// `==> Built in N` or `==> Build failed after N`. These are the umbrella traces
// the desktop's activity-queue parser keys off (mirrors RunHelmDeploy's
// `==> Deploying` / `==> Deployed`). Real run only: dry-run performs no work so
// there is nothing to put a spinner on, and skipping these lines in dry-run
// also keeps the dry-run integration goldens stable.
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
		// Call the unexported runReleaseSpec, not the exported
		// RunReleaseSpec: the release phase here is already wrapped by
		// the `==> Building` umbrella above, so emitting RunReleaseSpec's
		// own `==> Releasing` markers would register a redundant second
		// activity entry for the same `erun build --release` invocation.
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
	// The build phase above built and pushed the images — and push publishes
	// the runtime chart in lockstep with the runtime image (including for
	// --release). The deploy specs are pure (they reference those images via
	// ImageOverride) so deploy only runs helm here.
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
		err := RunDockerPushExecution(ctx, DockerPushExecutionSpec{builds: execution.dockerBuilds, pushes: execution.dockerPushes}, build, push)
		if err != nil {
			return pushedTags, err
		}
		return recordDockerPushTags(pushedTags, execution.dockerPushes), nil
	}
	if len(deploySpecs) > 0 {
		return pushedTags, buildAndPushDeployDockerImages(ctx, execution.dockerBuilds, build, push, pushedTags)
	}
	return pushedTags, RunDockerBuilds(ctx, execution.dockerBuilds, build)
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

// RunPushCommand wraps a standalone `erun push` operation with the
// `==> Pushing` / `==> Pushed in N` / `==> Push failed after N` umbrella
// traces the desktop's activity-queue parser keys off so the sidebar
// spinner lights while the push runs (mirrors runBuildExecution's
// `==> Building`). Real run only: dry-run does no work.
//
// Only the standalone push entrypoints (the CLI `push` command and the
// MCP push tool) route through here. Pushes that happen inside
// `erun build`/`erun build --deploy` are already covered by the
// `==> Building` umbrella, and the shared push executors
// (RunDockerPushExecution / RunDockerPushSpec) run per-image inside that
// flow — bracketing them directly would emit a marker per image and
// double-fire under `==> Building`.
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
			// The multi-arch build already pushed the image; publish its chart.
			return publishChartForPushedImage(ctx, buildInput.Image)
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
	return publishChartForPushedImage(ctx, pushInput.Image)
}

func RunDockerPushExecution(ctx Context, execution DockerPushExecutionSpec, build DockerImageBuilderFunc, push DockerPushFunc) error {
	if err := RunDockerBuilds(ctx, execution.builds, build); err != nil {
		return err
	}
	builtAndPushedTags := make(map[string]struct{}, len(execution.builds))
	for _, buildInput := range execution.builds {
		if !buildInput.Push {
			continue
		}
		builtAndPushedTags[buildInput.Image.Tag] = struct{}{}
		// The multi-arch build pushed this image; publish its chart in lockstep.
		if err := publishChartForPushedImage(ctx, buildInput.Image); err != nil {
			return err
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
	return nil
}
