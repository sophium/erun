package build

import (
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/primitive/build/spec"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
)

// resolveProjectDockerModuleDir resolves the project's docker module independently
// of the current directory.
func resolveProjectDockerModuleDir(findProjectRoot projectroot.ProjectFinderFunc, target model.DockerCommandTarget) (string, bool, error) {
	projectRoot, err := dockerbuild.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil || strings.TrimSpace(projectRoot) == "" {
		return "", false, err
	}
	if dockerDir, ok, err := spec.ResolveComponentAwareDockerDir(projectRoot, target.Component); err != nil || ok {
		return dockerDir, ok, err
	}
	return spec.ResolveProjectRootDevopsDockerDir(findProjectRoot, filepath.Clean(projectRoot))
}
