package build

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/helm"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/push"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
)

func DockerPushExecutionSpecFromSpecs(builds []dockerspec.DockerBuildSpec, pushes []dockerspec.DockerPushSpec) dockerspec.DockerPushExecutionSpec {
	return dockerspec.DockerPushExecutionSpec{Builds: builds, Pushes: pushes}
}

// resolveComponentChartSpecs resolves a publish spec for every chart under k8s/,
// whether or not a same-named image exists.
func resolveComponentChartSpecs(builds []dockerspec.DockerBuildSpec) ([]helm.HelmChartPublishSpec, error) {
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

func componentChartRegistryAndRoot(builds []dockerspec.DockerBuildSpec) (registry, projectRoot string) {
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

// publishDockerTarget pins a push to every supported platform: the
// docker.platforms pin narrows local builds only, never what is published.
func publishDockerTarget(target model.DockerCommandTarget) model.DockerCommandTarget {
	target.Platforms = slices.Clone(dockerspec.MultiPlatformDockerBuilds)
	return target
}

func ResolveDockerPushExecution(ctx core.Context, store projectseam.DockerStore, findProjectRoot projectseam.ProjectFinderFunc, resolveBuildContext projectseam.BuildContextResolverFunc, now projectseam.NowFunc, target model.DockerCommandTarget) (dockerspec.DockerPushExecutionSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = dockerbuild.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)
	target = publishDockerTarget(target)

	buildContexts, err := buildpkg.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return dockerspec.DockerPushExecutionSpec{}, err
	}

	builds := make([]dockerspec.DockerBuildSpec, 0, len(buildContexts))
	pushes := make([]dockerspec.DockerPushSpec, 0, len(buildContexts))
	for _, buildContext := range buildContexts {
		// A multi-arch image has no arch-less tag to `docker push`, so the build publishes it.
		build, err := buildpkg.ResolveDockerBuildSpec(ctx, store, findProjectRoot, resolveBuildContext, now, buildContext, target)
		if err != nil {
			return dockerspec.DockerPushExecutionSpec{}, err
		}
		build.Push = true
		builds = append(builds, build)
		pushes = append(pushes, buildpkg.NewDockerPushSpec(buildContext.Dir, build.Image))
	}

	builds, err = buildpkg.ApplyIncrementalToDockerBuilds(ctx, builds, target.NoIncremental)
	if err != nil {
		return dockerspec.DockerPushExecutionSpec{}, err
	}

	charts, err := resolveComponentChartSpecs(builds)
	if err != nil {
		return dockerspec.DockerPushExecutionSpec{}, err
	}

	return dockerspec.DockerPushExecutionSpec{Builds: builds, Pushes: pushes, ComponentCharts: charts}, nil
}

func ResolveDockerPushSpec(ctx core.Context, store projectseam.DockerStore, findProjectRoot projectseam.ProjectFinderFunc, resolveBuildContext projectseam.BuildContextResolverFunc, now projectseam.NowFunc, target model.DockerCommandTarget) (dockerspec.DockerPushSpec, *dockerspec.DockerBuildSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = dockerbuild.NormalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)
	target = publishDockerTarget(target)

	buildContext, err := resolveBuildContext()
	if err != nil {
		return dockerspec.DockerPushSpec{}, nil, err
	}
	if strings.TrimSpace(buildContext.DockerfilePath) == "" {
		return dockerspec.DockerPushSpec{}, nil, fmt.Errorf("dockerfile not found in current directory")
	}

	resolvedBuild, err := buildpkg.ResolveDockerBuildSpec(ctx, store, findProjectRoot, resolveBuildContext, now, buildContext, target)
	if err != nil {
		return dockerspec.DockerPushSpec{}, nil, err
	}
	resolvedBuild.Push = true
	incremental, err := buildpkg.ApplyIncrementalToDockerBuilds(ctx, []dockerspec.DockerBuildSpec{resolvedBuild}, target.NoIncremental)
	if err != nil {
		return dockerspec.DockerPushSpec{}, nil, err
	}
	resolvedBuild = incremental[0]

	return buildpkg.NewDockerPushSpec(buildContext.Dir, resolvedBuild.Image), &resolvedBuild, nil
}
