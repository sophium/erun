package dockerbuild

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config/containerregistry"
	"github.com/sophium/erun/erun-common/base/config/projectpaths"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
)

// ResolveDockerBuildRegistryForEnvironment's no-registry error must read the
// same in dry-run and real runs.
func ResolveDockerBuildRegistryForEnvironment(ctx core.Context, projectRoot, environment string) (string, bool, error) {
	list, err := containerregistry.EffectiveContainerRegistries(projectRoot, environment)
	if err != nil {
		return "", false, err
	}
	// Empty context: in-cluster config in-pod, current-context on the host.
	list, err = kube.ConcretizeRegistriesForContext(ctx, "", list)
	if err != nil {
		return "", false, err
	}
	registry, ok := list.BuildRegistry()
	if !ok {
		return "", false, fmt.Errorf("environment %q has no build registry; mark a registry with the build role in .erun/config.yaml before building", strings.TrimSpace(environment))
	}
	return registry, list.BuildRegistryInsecure(), nil
}

const (
	dockerContextRepoRoot  = "repo-root"
	dockerContextComponent = "component"
)

// ResolveDockerBuildContextDirForProject lets a configured dockercontext win
// over the positional heuristic.
func ResolveDockerBuildContextDirForProject(buildDir, projectRoot, selectedComponent string) (string, error) {
	mode, ok, err := resolveComponentAwareDockerContext(projectRoot, selectedComponent)
	if err != nil {
		return "", err
	}
	if ok {
		switch mode {
		case dockerContextRepoRoot:
			return projectRoot, nil
		case dockerContextComponent:
			return buildDir, nil
		}
	}
	if shouldUseProjectRootAsDockerContext(buildDir, projectRoot) {
		return projectRoot, nil
	}
	return buildDir, nil
}

// configuredDockerContext fails on an invalid value rather than fall back silently.
func configuredDockerContext(projectRoot string) (string, bool, error) {
	paths, err := projectpaths.LoadProjectPaths(projectRoot)
	if err != nil {
		return "", false, err
	}
	return validateDockerContextMode(paths.DockerContext, "paths.dockercontext")
}

func resolveComponentAwareDockerContext(projectRoot, selectedComponent string) (string, bool, error) {
	name, paths, ok, err := projectpaths.ResolveProjectComponent(projectRoot, selectedComponent)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return configuredDockerContext(projectRoot)
	}
	return validateDockerContextMode(paths.DockerContext, fmt.Sprintf("components.%s.dockercontext", name))
}

func validateDockerContextMode(mode, configKey string) (string, bool, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "", false, nil
	}
	switch mode {
	case dockerContextRepoRoot, dockerContextComponent:
		return mode, true, nil
	default:
		return "", false, fmt.Errorf("invalid docker context %q (.erun/config.yaml %s): expected %q or %q", mode, configKey, dockerContextRepoRoot, dockerContextComponent)
	}
}

func formatLocalSnapshotVersion(version string, now time.Time) string {
	return fmt.Sprintf("%s-snapshot-%s", strings.TrimSpace(version), now.UTC().Format(registrypkg.LocalSnapshotTimestampFormat))
}

func shouldUseProjectRootAsDockerContext(buildDir, projectRoot string) bool {
	if projectRoot == "" {
		return false
	}

	relative, err := filepath.Rel(projectRoot, buildDir)
	if err != nil {
		return false
	}

	parts := strings.Split(filepath.ToSlash(filepath.Clean(relative)), "/")
	return len(parts) >= 3 && parts[1] == "docker"
}
