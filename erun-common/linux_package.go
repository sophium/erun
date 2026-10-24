package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

// A linux package component is <tenant>-devops/linux/<name>/Dockerfile. It is
// never an image: a plain build runs it through its `test` stage to a
// cache-only output, and only `erun build --release` exports its `dist` stage
// and uploads the .deb files it holds (linux_package_build.go).

// ResolveCurrentLinuxPackageContexts resolves the linux package components the
// current directory scopes a build to: the package directory itself, every
// package under a linux/ directory, or every package of the project's
// <tenant>-devops/linux module from the devops directory or the project root.
func ResolveCurrentLinuxPackageContexts(findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, target DockerCommandTarget) ([]LinuxPackageContext, error) {
	if resolveBuildContext == nil {
		resolveBuildContext = ResolveDockerBuildContext
	}

	buildContext, err := resolveBuildContext()
	if err != nil {
		return nil, err
	}

	if context, ok, err := LinuxPackageContextAtDir(buildContext.Dir); err != nil {
		return nil, err
	} else if ok {
		return []LinuxPackageContext{context}, nil
	}

	if contexts, err := ResolveLinuxPackageContextsAtDir(buildContext.Dir); err == nil {
		return contexts, nil
	} else if !errors.Is(err, ErrLinuxPackageBuildNotFound) {
		return nil, err
	}

	linuxDir, ok, err := resolveCurrentDevopsLinuxDir(findProjectRoot, buildContext.Dir, target)
	if err != nil {
		return nil, err
	}
	if ok {
		return ResolveLinuxPackageContextsAtDir(linuxDir)
	}

	return nil, ErrLinuxPackageBuildNotFound
}

// LinuxPackageContextAtDir reports the linux package component at dir: a
// Dockerfile in a directory whose parent is named linux.
func LinuxPackageContextAtDir(dir string) (LinuxPackageContext, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || filepath.Base(filepath.Dir(dir)) != "linux" {
		return LinuxPackageContext{}, false, nil
	}

	dockerfilePath := filepath.Join(dir, "Dockerfile")
	info, err := os.Stat(dockerfilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return LinuxPackageContext{}, false, nil
		}
		return LinuxPackageContext{}, false, err
	}
	if info.IsDir() {
		return LinuxPackageContext{}, false, nil
	}
	return LinuxPackageContext{Dir: dir, DockerfilePath: dockerfilePath}, true, nil
}

func ResolveLinuxPackageContextsAtDir(dir string) ([]LinuxPackageContext, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || filepath.Base(dir) != "linux" {
		return nil, ErrLinuxPackageBuildNotFound
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrLinuxPackageBuildNotFound
		}
		return nil, err
	}

	contexts := make([]LinuxPackageContext, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		context, ok, err := LinuxPackageContextAtDir(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		contexts = append(contexts, context)
	}
	if len(contexts) == 0 {
		return nil, ErrLinuxPackageBuildNotFound
	}
	return contexts, nil
}

// isLinuxPackageDockerfile reports whether a Dockerfile is a linux package
// component's, which no image path may build, tag or push.
func isLinuxPackageDockerfile(dockerfilePath string) bool {
	dockerfilePath = strings.TrimSpace(dockerfilePath)
	if dockerfilePath == "" {
		return false
	}
	_, ok, err := LinuxPackageContextAtDir(filepath.Dir(filepath.Clean(dockerfilePath)))
	return err == nil && ok
}

// checkNotLinuxPackageDockerfile refuses to resolve a linux package component
// as an image. Discovery never offers one -- it walks docker/ directories --
// so this guards the explicit-context paths that hand a Dockerfile in directly.
func checkNotLinuxPackageDockerfile(dockerfilePath string) error {
	if !isLinuxPackageDockerfile(dockerfilePath) {
		return nil
	}
	return fmt.Errorf("%s is a linux package, not an image: `erun build` runs its test stage and `erun build --release` publishes its .deb files to the GitHub release; it is never tagged or pushed", dockerfilePath)
}

func resolveCurrentDevopsLinuxDir(findProjectRoot ProjectFinderFunc, dir string, target DockerCommandTarget) (string, bool, error) {
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

	projectRoot, err := ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return "", false, err
	}
	if projectRoot == "" || dir != filepath.Clean(projectRoot) {
		return "", false, nil
	}

	return resolveProjectRootDevopsLinuxDir(findProjectRoot, projectRoot)
}

func resolveProjectRootDevopsLinuxDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
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

func detectedProjectRootDevopsLinuxDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
	tenant, detectedProjectRoot, err := findProjectRoot()
	if err != nil || filepath.Clean(strings.TrimSpace(detectedProjectRoot)) != projectRoot || strings.TrimSpace(tenant) == "" {
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
		if errors.Is(err, ErrLinuxPackageBuildNotFound) {
			return false, nil
		}
		return false, err
	}
	return len(contexts) > 0, nil
}
