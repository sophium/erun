package push

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// RunPushCommand brackets standalone pushes only with the markers the desktop
// parses; pushes inside `erun build` already sit under its umbrella.
func RunPushCommand(ctx core.Context, op func(core.Context) error) (err error) {
	if ctx.DryRun {
		return op(ctx)
	}
	started := time.Now()
	ctx.Info("==> Pushing")
	root := core.NewStepTiming("push", nil)
	ctx.Timing = root
	defer func() {
		root.Finish(err)
		elapsed := time.Since(started).Round(time.Second)
		if err != nil {
			ctx.Info("==> Push failed after " + elapsed.String())
		} else {
			ctx.Info("==> Pushed in " + elapsed.String())
		}
		core.ReportStepTiming(ctx, "push", root)
	}()
	return op(ctx)
}

func RunDockerPush(ctx core.Context, pushInput shared.DockerPushSpec, push shared.DockerImagePusherFunc) error {
	if push == nil {
		push = shared.DockerImagePusher
	}
	pushInput.Verbosity = ctx.Verbosity
	command := pushInput.Command()
	ctx.TraceCommand(command.Dir, command.Name, command.Args...)
	if ctx.DryRun {
		return nil
	}
	return push(pushInput.Image.Tag, ctx.Verbosity, ctx.Stdout, ctx.Stderr)
}

// PreflightRegistryPushAccess refuses an impossible publish before the build,
// which would otherwise end at an interactive login a job cannot answer. The
// per-artifact check catches org policy denying create_package despite scope.
func PreflightRegistryPushAccess(ctx core.Context, execution shared.DockerPushExecutionSpec) error {
	if ctx.DryRun {
		return nil
	}
	checker := &registryPushAccessChecker{
		checkedRegistries: make(map[string]struct{}, 2),
		checkedTags:       make(map[string]struct{}, len(execution.Builds)+len(execution.Pushes)),
	}
	// Same union RunDockerPushExecution treats as "will be pushed".
	for _, buildInput := range execution.Builds {
		if !buildInput.Push {
			continue
		}
		if err := checker.checkImageTag(buildInput.Image.Tag); err != nil {
			return err
		}
	}
	for _, pushInput := range execution.Pushes {
		if err := checker.checkImageTag(pushInput.Image.Tag); err != nil {
			return err
		}
	}
	for _, chart := range execution.ComponentCharts {
		if err := registrypkg.VerifyGHCRChartCredentialConfigured(chart.OCIRepo); err != nil {
			return err
		}
		if err := registrypkg.VerifyGHCRCanPushChart(context.Background(), nil, chart.OCIRepo, chart.ChartName); err != nil {
			return err
		}
	}
	return nil
}

// registryPushAccessChecker checks a multi-platform tag once.
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
	return registrypkg.VerifyGHCRCanPushImage(context.Background(), nil, tag)
}

func (c *registryPushAccessChecker) checkRegistryScopeOnce(tag string) error {
	registry := registrypkg.DockerRegistryFromImageTag(tag)
	if registry == "" {
		return nil
	}
	if _, seen := c.checkedRegistries[registry]; seen {
		return nil
	}
	c.checkedRegistries[registry] = struct{}{}
	if err := registrypkg.VerifyGHCRCredentialConfigured(tag); err != nil {
		return err
	}
	return registrypkg.VerifyGHCRPushScope(context.Background(), nil, tag)
}

func PublishComponentCharts(ctx core.Context, specs []helm.HelmChartPublishSpec) error {
	published := make([]string, 0, len(specs))
	for i, spec := range specs {
		spec.Verbosity = ctx.Verbosity
		stepCtx, finish := ctx.StartTimingStep("chart " + spec.ChartName)
		err := publishAndVerifyHelmChart(stepCtx, spec)
		finish(err)
		if err != nil {
			return newPartialChartPublishError(spec, published, specs[i+1:], err)
		}
		published = append(published, spec.ChartName)
	}
	return nil
}

func publishAndVerifyHelmChart(ctx core.Context, spec helm.HelmChartPublishSpec) error {
	if err := helm.RunHelmChartPublish(ctx, spec); err != nil {
		return err
	}
	return helm.VerifyPublishedHelmChart(ctx, spec.OCIRepo, spec.ChartName, spec.Version)
}

// newPartialChartPublishError names the split because images and tags are
// already public; re-running push recovers.
func newPartialChartPublishError(failed helm.HelmChartPublishSpec, published []string, remaining []helm.HelmChartPublishSpec, err error) error {
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

// PackageComponentCharts packages each chart locally without publishing.
func PackageComponentCharts(ctx core.Context, specs []helm.HelmChartPublishSpec) error {
	for _, spec := range specs {
		if err := helm.PackageResolvedChart(ctx, spec); err != nil {
			return err
		}
	}
	return nil
}

// ComponentChartPublishVersion skips version-pinned bases; empty lets each chart
// use its own image version.
func ComponentChartPublishVersion(builds []shared.DockerBuildSpec) string {
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
