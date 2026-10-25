package linuxpkg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/buildtarget"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

// A linux package component is never an image: a plain build runs its `test` stage to a cache-only
// output, and only `erun build --release` exports and uploads its `dist` .deb files.

// ResolveCurrentLinuxPackageContexts resolves the linux package components the current directory scopes a build to.
func ResolveCurrentLinuxPackageContexts(findProjectRoot projectroot.ProjectFinderFunc, resolveBuildContext buildtarget.BuildContextResolverFunc, target model.DockerCommandTarget) ([]dockerspec.LinuxPackageContext, error) {
	if resolveBuildContext == nil {
		resolveBuildContext = dockerspec.ResolveDockerBuildContext
	}

	buildContext, err := resolveBuildContext()
	if err != nil {
		return nil, err
	}

	if context, ok, err := dockerspec.LinuxPackageContextAtDir(buildContext.Dir); err != nil {
		return nil, err
	} else if ok {
		return []dockerspec.LinuxPackageContext{context}, nil
	}

	if contexts, err := ResolveLinuxPackageContextsAtDir(buildContext.Dir); err == nil {
		return contexts, nil
	} else if !errors.Is(err, dockerspec.ErrLinuxPackageBuildNotFound) {
		return nil, err
	}

	linuxDir, ok, err := resolveCurrentDevopsLinuxDir(findProjectRoot, buildContext.Dir, target)
	if err != nil {
		return nil, err
	}
	if ok {
		return ResolveLinuxPackageContextsAtDir(linuxDir)
	}

	return nil, dockerspec.ErrLinuxPackageBuildNotFound
}

func resolveCurrentDevopsLinuxDir(findProjectRoot projectroot.ProjectFinderFunc, dir string, target model.DockerCommandTarget) (string, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" {
		return "", false, nil
	}

	linuxDir := filepath.Join(dir, "linux")
	if strings.HasSuffix(filepath.Base(dir), "-devops") {
		if ok, err := isLinuxPackageModuleDir(linuxDir); err != nil {
			return "", false, err
		} else if ok {
			return linuxDir, true, nil
		}
	}

	projectRoot, err := dockerbuild.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return "", false, err
	}
	if projectRoot == "" || dir != filepath.Clean(projectRoot) {
		return "", false, nil
	}

	return resolveProjectRootDevopsLinuxDir(findProjectRoot, projectRoot)
}

func resolveProjectRootDevopsLinuxDir(findProjectRoot projectroot.ProjectFinderFunc, projectRoot string) (string, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if projectRoot == "" {
		return "", false, nil
	}

	linuxDir, ok, err := detectedProjectRootDevopsLinuxDir(findProjectRoot, projectRoot)
	if err != nil || ok {
		return linuxDir, ok, err
	}

	candidates, err := findDevopsLinuxDirs(projectRoot)
	if err != nil {
		return "", false, err
	}
	switch len(candidates) {
	case 0:
		return "", false, nil
	case 1:
		return candidates[0], true, nil
	default:
		return "", false, fmt.Errorf("multiple devops linux directories found under project root")
	}
}

func detectedProjectRootDevopsLinuxDir(findProjectRoot projectroot.ProjectFinderFunc, projectRoot string) (string, bool, error) {
	tenant, ok := dockerbuild.DetectedProjectTenant(findProjectRoot, projectRoot)
	if !ok {
		return "", false, nil
	}
	linuxDir := filepath.Join(projectRoot, config.RuntimeReleaseName(tenant), "linux")
	if ok, err := isLinuxPackageModuleDir(linuxDir); err != nil {
		return "", false, err
	} else if ok {
		return linuxDir, true, nil
	}
	return "", false, nil
}

func findDevopsLinuxDirs(projectRoot string) ([]string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil, err
	}
	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}
		linuxDir := filepath.Join(projectRoot, entry.Name(), "linux")
		ok, err := isLinuxPackageModuleDir(linuxDir)
		if err != nil {
			return nil, err
		}
		if ok {
			candidates = append(candidates, linuxDir)
		}
	}
	return candidates, nil
}

func isLinuxPackageModuleDir(dir string) (bool, error) {
	contexts, err := ResolveLinuxPackageContextsAtDir(dir)
	if err != nil {
		if errors.Is(err, dockerspec.ErrLinuxPackageBuildNotFound) {
			return false, nil
		}
		return false, err
	}
	return len(contexts) > 0, nil
}

func ResolveLinuxPackageContextsAtDir(dir string) ([]dockerspec.LinuxPackageContext, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || filepath.Base(dir) != "linux" {
		return nil, dockerspec.ErrLinuxPackageBuildNotFound
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, dockerspec.ErrLinuxPackageBuildNotFound
		}
		return nil, err
	}

	contexts := make([]dockerspec.LinuxPackageContext, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		context, ok, err := dockerspec.LinuxPackageContextAtDir(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		contexts = append(contexts, context)
	}
	if len(contexts) == 0 {
		return nil, dockerspec.ErrLinuxPackageBuildNotFound
	}
	return contexts, nil
}
