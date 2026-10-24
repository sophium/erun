package build

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/push"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func DockerPushExecutionSpecFromSpecs(builds []shared.DockerBuildSpec, pushes []shared.DockerPushSpec) shared.DockerPushExecutionSpec {
	return shared.DockerPushExecutionSpec{Builds: builds, Pushes: pushes}
}

// resolveComponentChartSpecs discovers every Helm chart under a k8s/ directory in
// the project and resolves a publish spec for each at the build's version and
// registry. Charts are module-level source: a chart publishes whether or not a
// same-named image exists. The version and registry come from the resolved builds
// (componentChartPublishVersion / the first build's registry+root); a project with
// charts but no image build yields no specs.
func resolveComponentChartSpecs(builds []shared.DockerBuildSpec) ([]helm.HelmChartPublishSpec, error) {
	version := push.ComponentChartPublishVersion(builds)
	registry, projectRoot := componentChartRegistryAndRoot(builds)
	if version == "" || registry == "" || projectRoot == "" {
		return nil, nil
	}
	contexts, err := deploy.DiscoverComponentChartDirs(projectRoot)
	if err != nil {
		return nil, err
	}
	specs := make([]helm.HelmChartPublishSpec, 0, len(contexts))
	for _, context := range contexts {
		spec, err := helm.ResolveHelmChartPublishSpec(context.ChartPath, version, registry)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].ChartName < specs[j].ChartName })
	return specs, nil
}

func componentChartRegistryAndRoot(builds []shared.DockerBuildSpec) (registry, projectRoot string) {
	for _, build := range builds {
		if registry == "" {
			registry = strings.TrimSpace(build.Image.Registry)
		}
		if projectRoot == "" {
			projectRoot = strings.TrimSpace(build.Image.ProjectRoot)
		}
	}
	return registry, projectRoot
}

// publishDockerTarget pins a push target to every platform erun supports,
// before any build spec resolves. A push is the step that puts a version's
// artifacts in the registry, so the .erun/config.yaml docker.platforms pin --
// which exists so a machine confirmed to be one architecture stops paying for
// emulated cross-builds on every local build -- must not narrow what is
// published. It is the same override ResolveDockerBuildTarget applies to a
// release build; without it here, the pin reaches the registry and a version
// ships a manifest the fleet's other architecture cannot pull.
func publishDockerTarget(target shared.DockerCommandTarget) shared.DockerCommandTarget {
	target.Platforms = slices.Clone(shared.MultiPlatformDockerBuilds)
	return target
}

func ResolveDockerPushExecution(ctx core.Context, store shared.DockerStore, findProjectRoot shared.ProjectFinderFunc, resolveBuildContext shared.BuildContextResolverFunc, now shared.NowFunc, target shared.DockerCommandTarget) (shared.DockerPushExecutionSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = shared.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)
	target = publishDockerTarget(target)

	buildContexts, err := buildpkg.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return shared.DockerPushExecutionSpec{}, err
	}

	builds := make([]shared.DockerBuildSpec, 0, len(buildContexts))
	pushes := make([]shared.DockerPushSpec, 0, len(buildContexts))
	for _, buildContext := range buildContexts {
		// A multi-arch image has no arch-less version tag to `docker push`, so the
		// build itself publishes the per-arch manifest list; the separate push
		// entry is skipped once the build is marked pushed.
		build, err := buildpkg.ResolveDockerBuildSpec(ctx, store, findProjectRoot, resolveBuildContext, now, buildContext, target)
		if err != nil {
			return shared.DockerPushExecutionSpec{}, err
		}
		build.Push = true
		builds = append(builds, build)
		pushes = append(pushes, buildpkg.NewDockerPushSpec(buildContext.Dir, build.Image))
	}

	builds, err = buildpkg.ApplyIncrementalToDockerBuilds(ctx, builds, target.NoIncremental)
	if err != nil {
		return shared.DockerPushExecutionSpec{}, err
	}

	charts, err := resolveComponentChartSpecs(builds)
	if err != nil {
		return shared.DockerPushExecutionSpec{}, err
	}

	return shared.DockerPushExecutionSpec{Builds: builds, Pushes: pushes, ComponentCharts: charts}, nil
}

func ResolveDockerPushSpec(ctx core.Context, store shared.DockerStore, findProjectRoot shared.ProjectFinderFunc, resolveBuildContext shared.BuildContextResolverFunc, now shared.NowFunc, target shared.DockerCommandTarget) (shared.DockerPushSpec, *shared.DockerBuildSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = shared.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)
	target = publishDockerTarget(target)

	buildContext, err := resolveBuildContext()
	if err != nil {
		return shared.DockerPushSpec{}, nil, err
	}
	if strings.TrimSpace(buildContext.DockerfilePath) == "" {
		return shared.DockerPushSpec{}, nil, fmt.Errorf("dockerfile not found in current directory")
	}

	// A multi-arch image has no arch-less version tag to `docker push`, so the
	// build itself publishes the manifest; the single-tag push is skipped once
	// Push is set.
	resolvedBuild, err := buildpkg.ResolveDockerBuildSpec(ctx, store, findProjectRoot, resolveBuildContext, now, buildContext, target)
	if err != nil {
		return shared.DockerPushSpec{}, nil, err
	}
	resolvedBuild.Push = true
	incremental, err := buildpkg.ApplyIncrementalToDockerBuilds(ctx, []shared.DockerBuildSpec{resolvedBuild}, target.NoIncremental)
	if err != nil {
		return shared.DockerPushSpec{}, nil, err
	}
	resolvedBuild = incremental[0]

	return buildpkg.NewDockerPushSpec(buildContext.Dir, resolvedBuild.Image), &resolvedBuild, nil
}
