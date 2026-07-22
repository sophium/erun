package eruncommon

import (
	"fmt"
	"sort"
	"strings"
)

func DockerPushExecutionSpecFromSpecs(builds []DockerBuildSpec, pushes []DockerPushSpec) DockerPushExecutionSpec {
	return DockerPushExecutionSpec{builds: builds, pushes: pushes}
}

// resolveComponentChartSpecs discovers every Helm chart under a k8s/ directory in
// the project and resolves a publish spec for each at the build's version and
// registry. Charts are module-level source: a chart publishes whether or not a
// same-named image exists. The version and registry come from the resolved builds
// (componentChartPublishVersion / the first build's registry+root); a project with
// charts but no image build yields no specs.
func resolveComponentChartSpecs(builds []DockerBuildSpec) ([]HelmChartPublishSpec, error) {
	version := componentChartPublishVersion(builds)
	registry, projectRoot := componentChartRegistryAndRoot(builds)
	if version == "" || registry == "" || projectRoot == "" {
		return nil, nil
	}
	contexts, err := discoverComponentChartDirs(projectRoot)
	if err != nil {
		return nil, err
	}
	specs := make([]HelmChartPublishSpec, 0, len(contexts))
	for _, context := range contexts {
		spec, err := resolveHelmChartPublishSpec(context.ChartPath, version, registry)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].ChartName < specs[j].ChartName })
	return specs, nil
}

func componentChartRegistryAndRoot(builds []DockerBuildSpec) (registry, projectRoot string) {
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

func ResolveDockerPushExecution(ctx Context, store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) (DockerPushExecutionSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildContexts, err := ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err != nil {
		return DockerPushExecutionSpec{}, err
	}

	builds := make([]DockerBuildSpec, 0, len(buildContexts))
	pushes := make([]DockerPushSpec, 0, len(buildContexts))
	for _, buildContext := range buildContexts {
		// A multi-arch image has no arch-less version tag to `docker push`, so the
		// build itself publishes the per-arch manifest list; the separate push
		// entry is skipped once the build is marked pushed.
		build, err := resolveDockerBuildSpec(ctx, store, findProjectRoot, resolveBuildContext, now, buildContext, target)
		if err != nil {
			return DockerPushExecutionSpec{}, err
		}
		build.Push = true
		builds = append(builds, build)
		pushes = append(pushes, NewDockerPushSpec(buildContext.Dir, build.Image))
	}

	builds, err = ApplyIncrementalToDockerBuilds(ctx, builds, target.NoIncremental)
	if err != nil {
		return DockerPushExecutionSpec{}, err
	}

	charts, err := resolveComponentChartSpecs(builds)
	if err != nil {
		return DockerPushExecutionSpec{}, err
	}

	return DockerPushExecutionSpec{builds: builds, pushes: pushes, componentCharts: charts}, nil
}

func ResolveDockerPushSpec(ctx Context, store DockerStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target DockerCommandTarget) (DockerPushSpec, *DockerBuildSpec, error) {
	store, findProjectRoot, resolveBuildContext, now = normalizeDockerDependencies(store, findProjectRoot, resolveBuildContext, now)

	buildContext, err := resolveBuildContext()
	if err != nil {
		return DockerPushSpec{}, nil, err
	}
	if strings.TrimSpace(buildContext.DockerfilePath) == "" {
		return DockerPushSpec{}, nil, fmt.Errorf("dockerfile not found in current directory")
	}

	// A multi-arch image has no arch-less version tag to `docker push`, so the
	// build itself publishes the manifest; the single-tag push is skipped once
	// Push is set.
	resolvedBuild, err := resolveDockerBuildSpec(ctx, store, findProjectRoot, resolveBuildContext, now, buildContext, target)
	if err != nil {
		return DockerPushSpec{}, nil, err
	}
	resolvedBuild.Push = true
	incremental, err := ApplyIncrementalToDockerBuilds(ctx, []DockerBuildSpec{resolvedBuild}, target.NoIncremental)
	if err != nil {
		return DockerPushSpec{}, nil, err
	}
	resolvedBuild = incremental[0]

	return NewDockerPushSpec(buildContext.Dir, resolvedBuild.Image), &resolvedBuild, nil
}
