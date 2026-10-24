package build

import (
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// resolveProjectDockerModuleDir resolves the project's docker module independently
// of the current directory.
func resolveProjectDockerModuleDir(findProjectRoot shared.ProjectFinderFunc, target model.DockerCommandTarget) (string, bool, error) {
	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil || strings.TrimSpace(projectRoot) == "" {
		return "", false, err
	}
	if dockerDir, ok, err := build.ResolveComponentAwareDockerDir(projectRoot, target.Component); err != nil || ok {
		return dockerDir, ok, err
	}
	return build.ResolveProjectRootDevopsDockerDir(findProjectRoot, filepath.Clean(projectRoot))
}
