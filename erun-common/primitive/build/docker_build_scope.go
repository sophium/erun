package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func ResolveCurrentDockerBuildContexts(findProjectRoot shared.ProjectFinderFunc, resolveBuildContext shared.BuildContextResolverFunc, target shared.DockerCommandTarget) ([]shared.DockerBuildContext, error) {
	if resolveBuildContext == nil {
		resolveBuildContext = shared.ResolveDockerBuildContext
	}

	buildContext, err := resolveBuildContext()
	if err != nil {
		return nil, err
	}
	if shared.IsLinuxPackageDockerfile(buildContext.DockerfilePath) {
		// A linux package directory has a Dockerfile but no image to build.
		return nil, shared.ErrDockerBuildContextNotFound
	}
	if strings.TrimSpace(buildContext.DockerfilePath) != "" {
		return []shared.DockerBuildContext{buildContext}, nil
	}

	if buildContexts, err := shared.ResolveDockerBuildContextsAtDir(buildContext.Dir); err == nil {
		return buildContexts, nil
	}

	dockerDir, ok, err := resolveCurrentDevopsDockerDir(findProjectRoot, buildContext.Dir, target)
	if err != nil {
		return nil, err
	}
	if ok {
		return shared.ResolveDockerBuildContextsAtDir(dockerDir)
	}

	return nil, shared.ErrDockerBuildContextNotFound
}

func resolveCurrentDevopsDockerDir(findProjectRoot shared.ProjectFinderFunc, dir string, target shared.DockerCommandTarget) (string, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" {
		return "", false, nil
	}

	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return "", false, err
	}

	// A configured paths.docker override (or a selected components: entry) is
	// authoritative: it wins over the -devops cwd shortcut and the project-root
	// convention scan, and applies from any cwd inside the project.
	if projectRoot != "" {
		if configured, ok, err := ResolveComponentAwareDockerDir(projectRoot, target.Component); err != nil || ok {
			return configured, ok, err
		}
	}

	return resolveConventionDevopsDockerDir(findProjectRoot, dir, projectRoot)
}

// resolveConventionDevopsDockerDir is the convention discovery used when no
// paths.docker override applies: the cwd -devops shortcut, then the project-root
// <tenant>-devops/docker scan (only when cwd is the project root).
func resolveConventionDevopsDockerDir(findProjectRoot shared.ProjectFinderFunc, dir, projectRoot string) (string, bool, error) {
	dockerDir := filepath.Join(dir, "docker")
	if strings.HasSuffix(filepath.Base(dir), "-devops") {
		if ok, err := shared.IsDockerBuildModuleDir(dockerDir); err != nil {
			return "", false, err
		} else if ok {
			return dockerDir, true, nil
		}
	}

	if projectRoot == "" || dir != filepath.Clean(projectRoot) {
		return "", false, nil
	}

	return ResolveProjectRootDevopsDockerDir(findProjectRoot, projectRoot)
}

// ResolveComponentAwareDockerDir resolves the docker build root a build/push
// command should use: the selected components: entry when the project
// declares one or more (see resolveProjectComponent), otherwise the unchanged
// paths.docker override via configuredDockerDir.
func ResolveComponentAwareDockerDir(projectRoot, selectedComponent string) (string, bool, error) {
	name, paths, ok, err := config.ResolveProjectComponent(projectRoot, selectedComponent)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return shared.ConfiguredDockerDir(projectRoot)
	}
	return shared.ResolveAndValidateDockerDir(projectRoot, paths.Docker, fmt.Sprintf("components.%s.docker", name))
}

func ResolveProjectRootDevopsDockerDir(findProjectRoot shared.ProjectFinderFunc, projectRoot string) (string, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if projectRoot == "" {
		return "", false, nil
	}

	dockerDir, ok, err := detectedProjectRootDevopsDockerDir(findProjectRoot, projectRoot)
	if err != nil || ok {
		return dockerDir, ok, err
	}

	candidates, err := findDevopsDockerDirs(projectRoot)
	if err != nil {
		return "", false, err
	}
	switch len(candidates) {
	case 0:
		return "", false, nil
	case 1:
		return candidates[0], true, nil
	default:
		return "", false, fmt.Errorf("multiple devops docker directories found under project root")
	}
}

func detectedProjectRootDevopsDockerDir(findProjectRoot shared.ProjectFinderFunc, projectRoot string) (string, bool, error) {
	tenant, ok := shared.DetectedProjectTenant(findProjectRoot, projectRoot)
	if !ok {
		return "", false, nil
	}
	dockerDir := filepath.Join(projectRoot, config.RuntimeReleaseName(tenant), "docker")
	if ok, err := shared.IsDockerBuildModuleDir(dockerDir); err != nil {
		return "", false, err
	} else if ok {
		return dockerDir, true, nil
	}
	return "", false, nil
}

func findDevopsDockerDirs(projectRoot string) ([]string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil, err
	}
	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}
		dockerDir := filepath.Join(projectRoot, entry.Name(), "docker")
		ok, err := shared.IsDockerBuildModuleDir(dockerDir)
		if err != nil {
			return nil, err
		}
		if ok {
			candidates = append(candidates, dockerDir)
		}
	}
	return candidates, nil
}

// projectHasDevopsFolder is a bare presence check — it does not require a
// buildable docker module — so the build-env advisory fires only when there is
// no devops module at all, not when one exists but is still being set up.
func projectHasDevopsFolder(projectRoot string) (bool, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return false, nil
	}
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), "-devops") {
			return true, nil
		}
	}
	return false, nil
}

// ResolveDockerBuildEnvConfig returns the saved EnvConfig a build resolves to,
// or nil when none matches. The build path is otherwise env-agnostic; this is
// the seam for reading per-env build toggles such as DisableBuildScript.
// Best-effort: any store error or a miss yields nil so the build proceeds with
// default behaviour.
func ResolveDockerBuildEnvConfig(store shared.DockerStore, findProjectRoot shared.ProjectFinderFunc, target shared.DockerCommandTarget) *config.EnvConfig {
	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil || strings.TrimSpace(projectRoot) == "" {
		return shared.InjectedDockerBuildEnvConfig(nil)
	}
	return shared.ResolveDockerBuildEnvConfigForProject(store, projectRoot, target.Environment)
}
