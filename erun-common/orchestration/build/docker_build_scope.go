package build

import (
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// resolveProjectDockerModuleDir resolves the docker build module the project
// itself declares, independently of the current directory: the configured
// paths.docker / selected components entry when there is one, otherwise the
// project root's <tenant>-devops/docker convention. A caller uses it to ask
// whether the project has images at all, which is a different question from
// whether the current directory resolves any.
func resolveProjectDockerModuleDir(findProjectRoot shared.ProjectFinderFunc, target shared.DockerCommandTarget) (string, bool, error) {
	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil || strings.TrimSpace(projectRoot) == "" {
		return "", false, err
	}
	if dockerDir, ok, err := build.ResolveComponentAwareDockerDir(projectRoot, target.Component); err != nil || ok {
		return dockerDir, ok, err
	}
	return build.ResolveProjectRootDevopsDockerDir(findProjectRoot, filepath.Clean(projectRoot))
}
