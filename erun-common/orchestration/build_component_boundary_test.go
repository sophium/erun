package orchestration

import (
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func boundaryResolvers(projectRoot, cwd string) (shared.ProjectFinderFunc, shared.BuildContextResolverFunc) {
	findProjectRoot := func() (string, string, error) { return "team", projectRoot, nil }
	resolveBuildContext := func() (shared.DockerBuildContext, error) { return shared.DockerBuildContextAtDir(cwd) }
	return findProjectRoot, resolveBuildContext
}

func boundaryTarget(projectRoot string) shared.DockerCommandTarget {
	return shared.DockerCommandTarget{ProjectRoot: projectRoot, Environment: "dev", VersionOverride: "1.0.0", NoIncremental: true}
}
