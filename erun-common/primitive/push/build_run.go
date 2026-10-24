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

// RunPushCommand brackets a standalone `erun push` with the `==> Pushing` /
// `==> Pushed` markers the desktop's activity-queue parser keys off, and
// starts the step-timing root reported when the bracket closes. Only the
// standalone push entrypoints route through here: pushes inside `erun build`
// already sit under the `==> Building` umbrella, and the per-image push
// executors would fire a marker per image and double-count if bracketed here.
// op receives the timing-scoped context so the images and charts it pushes
// attach their own step-timing children; dry-run does no work and skips
// timing, same as the other three umbrellas.
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

// PreflightRegistryPushAccess refuses a publish that is already known to be
// impossible, before it spends the build.
//
// A release that cannot push otherwise discovers it at the push, after every
// image has been built for every architecture, and then offers an interactive
// login that a detached job or an agent run can never answer. The registry
// credential is knowable up front, so it is checked up front — the same class of
// upfront refusal as a release whose images are not covered by a build it will
// publish.
//
// Three checks run here, cheapest and most certain first: VerifyGHCRCredentialConfigured
// answers whether any credential resolves at all -- an environment that has
// never authenticated to ghcr.io is certain to fail at push, never merely
// inconclusive. VerifyGHCRPushScope is a per-registry check of a
// resolved credential's own write:packages scope (cheap, one request per
// registry). VerifyGHCRCanPushImage/VerifyGHCRCanPushChart is a per-artifact
// check of whether the registry would actually grant push for that specific
// repository, including creating it for the first time — a token can have
// write:packages and still be denied create_package by org policy on a
// component nothing has ever published before, which the scope check alone
// cannot see.
//
// Skipped in dry-run, which does no work and must keep the integration traces
// stable.
func PreflightRegistryPushAccess(ctx core.Context, execution shared.DockerPushExecutionSpec) error {
	if ctx.DryRun {
		return nil
	}
	checker := &registryPushAccessChecker{
		checkedRegistries: make(map[string]struct{}, 2),
		checkedTags:       make(map[string]struct{}, len(execution.Builds)+len(execution.Pushes)),
	}
	// A build that pushes inline (buildInput.Push) and a promoted fingerprint
	// push (execution.pushes) are both real pushes this run will make — the
	// same union RunDockerPushExecution treats as "will be pushed" just below
	// this preflight, so both need the same check.
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

// PublishComponentCharts packages+pushes then verifies each resolved chart.
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

// newPartialChartPublishError reports a chart publish that stopped mid-set. By
// the time charts publish, the version's images — and, for a release, its git tag
// and GitHub release — are already public, so aborting here leaves a version whose
// consumers cannot resolve every chart. The operator needs the exact split to
// recover, and re-running push is the recovery: publishing a chart already
// published is harmless.
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

// PackageComponentCharts packages each resolved chart locally (validate + record
// identity) without publishing — the build-only counterpart of
// publishComponentCharts.
func PackageComponentCharts(ctx core.Context, specs []helm.HelmChartPublishSpec) error {
	for _, spec := range specs {
		if err := helm.PackageResolvedChart(ctx, spec); err != nil {
			return err
		}
	}
	return nil
}

// ComponentChartPublishVersion is the release version every component chart
// publishes at. Version-pinned bases (VersionFromBuildDir, e.g. erun-powerdns,
// erun-backend-postgres) keep their upstream image pin and are skipped; the
// always-present, non-pinned runtime (erun-devops) guarantees a match. An empty
// result lets each chart fall back to its own image version.
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
