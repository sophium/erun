package shared

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

type KubernetesDeployContext struct {
	Dir           string
	ComponentName string
	ChartPath     string
}

func ResolveKubernetesDeployContextsAtDir(dir string) ([]KubernetesDeployContext, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || filepath.Base(dir) != "k8s" {
		return nil, fmt.Errorf("helm chart not found in current directory")
	}

	deployContexts, err := KubernetesDeployContextsUnderDir(dir)
	if err != nil {
		return nil, err
	}
	if len(deployContexts) == 0 {
		return nil, fmt.Errorf("helm chart not found in current directory")
	}

	return deployContexts, nil
}

func KubernetesDeployContextsUnderDir(dir string) ([]KubernetesDeployContext, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	deployContexts := make([]KubernetesDeployContext, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		chartPath := filepath.Join(dir, entry.Name())
		if !HasHelmChart(filepath.Join(chartPath, "Chart.yaml")) {
			continue
		}

		deployContexts = append(deployContexts, KubernetesDeployContext{
			Dir:           dir,
			ComponentName: entry.Name(),
			ChartPath:     chartPath,
		})
	}

	return deployContexts, nil
}

func ResolveCurrentDevopsK8sDir(findProjectRoot ProjectFinderFunc, dir, projectRootOverride string) (string, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" {
		return "", false, nil
	}

	// A configured paths.k8s override is authoritative over the -devops
	// convention discovery, and applies from any cwd inside the project.
	if k8sDir, ok, err := configuredK8sDirForDeploy(findProjectRoot, projectRootOverride); err != nil || ok {
		return k8sDir, ok, err
	}

	return resolveConventionDevopsK8sDir(findProjectRoot, dir, projectRootOverride)
}

// resolveConventionDevopsK8sDir is the convention discovery used when no
// paths.k8s override applies: the cwd -devops dir, an ancestor -devops dir, then
// the project-root <tenant>-devops/k8s scan.
func resolveConventionDevopsK8sDir(findProjectRoot ProjectFinderFunc, dir, projectRootOverride string) (string, bool, error) {
	if k8sDir, ok, err := resolveDirDevopsK8sDir(dir); err != nil || ok {
		return k8sDir, ok, err
	}

	if k8sDir, ok, err := resolveAncestorDevopsK8sDir(dir); err != nil || ok {
		return k8sDir, ok, err
	}

	if projectRoot := strings.TrimSpace(projectRootOverride); projectRoot != "" {
		return ResolveProjectRootDevopsK8sDir(findProjectRoot, projectRoot)
	}

	projectRoot, err := ResolveDockerBuildProjectRoot(findProjectRoot, DockerCommandTarget{})
	if err != nil {
		return "", false, err
	}
	if projectRoot == "" || dir != filepath.Clean(projectRoot) {
		return "", false, nil
	}

	return ResolveProjectRootDevopsK8sDir(findProjectRoot, projectRoot)
}

// configuredK8sDirForDeploy resolves the project root from the deploy chain's
// override (or the git root) and returns the configured paths.k8s directory
// when set. It is the deploy-side entry to the shared configuredK8sDir check.
func configuredK8sDirForDeploy(findProjectRoot ProjectFinderFunc, projectRootOverride string) (string, bool, error) {
	projectRoot := strings.TrimSpace(projectRootOverride)
	if projectRoot == "" {
		resolved, err := ResolveDockerBuildProjectRoot(findProjectRoot, DockerCommandTarget{})
		if err != nil {
			return "", false, err
		}
		projectRoot = resolved
	}
	if projectRoot == "" {
		return "", false, nil
	}
	return ConfiguredK8sDir(projectRoot)
}

// ConfiguredK8sDir resolves the project-global paths.k8s override. It returns
// (dir, true, nil) when configured and a usable Helm chart module, (,false,nil)
// when unset, and an error when configured but not a chart module — a
// misconfigured override fails loudly rather than silently falling back to
// convention.
func ConfiguredK8sDir(projectRoot string) (string, bool, error) {
	paths, err := configpkg.LoadProjectPaths(projectRoot)
	if err != nil {
		return "", false, err
	}
	k8sDir := configpkg.ResolveProjectPath(projectRoot, paths.K8s)
	if k8sDir == "" {
		return "", false, nil
	}
	ok, err := IsKubernetesDeployModuleDir(k8sDir)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, fmt.Errorf("configured k8s path %q (.erun/config.yaml paths.k8s) is not a Helm chart module: expected a directory named \"k8s\" holding per-component charts", strings.TrimSpace(paths.K8s))
	}
	return k8sDir, true, nil
}

// resolveDirDevopsK8sDir returns dir/k8s when dir is itself a "-devops" module
// directory whose k8s subdir holds helm charts.
func resolveDirDevopsK8sDir(dir string) (string, bool, error) {
	if !strings.HasSuffix(filepath.Base(dir), "-devops") {
		return "", false, nil
	}
	k8sDir := filepath.Join(dir, "k8s")
	ok, err := IsKubernetesDeployModuleDir(k8sDir)
	if err != nil {
		return "", false, err
	}
	if ok {
		return k8sDir, true, nil
	}
	return "", false, nil
}

func resolveAncestorDevopsK8sDir(dir string) (string, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" {
		return "", false, nil
	}

	for current := dir; ; {
		parent := filepath.Dir(current)
		if parent == current {
			return "", false, nil
		}
		current = parent
		if !strings.HasSuffix(filepath.Base(current), "-devops") {
			continue
		}

		k8sDir := filepath.Join(current, "k8s")
		ok, err := IsKubernetesDeployModuleDir(k8sDir)
		if err != nil || ok {
			return k8sDir, ok, err
		}
	}
}

func ResolveProjectRootDevopsK8sDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if projectRoot == "" {
		return "", false, nil
	}

	k8sDir, ok, err := detectedProjectRootDevopsK8sDir(findProjectRoot, projectRoot)
	if err != nil || ok {
		return k8sDir, ok, err
	}

	candidates, err := findDevopsK8sDirs(projectRoot)
	if err != nil {
		return "", false, err
	}
	switch len(candidates) {
	case 0:
		return "", false, nil
	case 1:
		return candidates[0], true, nil
	default:
		return "", false, fmt.Errorf("multiple devops k8s directories found under project root")
	}
}

func detectedProjectRootDevopsK8sDir(findProjectRoot ProjectFinderFunc, projectRoot string) (string, bool, error) {
	tenant, detectedProjectRoot, err := findProjectRoot()
	if err != nil || filepath.Clean(strings.TrimSpace(detectedProjectRoot)) != projectRoot || strings.TrimSpace(tenant) == "" {
		return "", false, nil
	}
	k8sDir := filepath.Join(projectRoot, configpkg.RuntimeReleaseName(tenant), "k8s")
	if ok, err := IsKubernetesDeployModuleDir(k8sDir); err != nil {
		return "", false, err
	} else if ok {
		return k8sDir, true, nil
	}
	return "", false, nil
}

func findDevopsK8sDirs(projectRoot string) ([]string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil, err
	}

	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}

		k8sDir := filepath.Join(projectRoot, entry.Name(), "k8s")
		ok, err := IsKubernetesDeployModuleDir(k8sDir)
		if err != nil {
			return nil, err
		}
		if ok {
			candidates = append(candidates, k8sDir)
		}
	}
	return candidates, nil
}

func IsKubernetesDeployModuleDir(dir string) (bool, error) {
	deployContexts, err := ResolveKubernetesDeployContextsAtDir(dir)
	if err != nil {
		if err.Error() == "helm chart not found in current directory" {
			return false, nil
		}
		// An absent directory is simply not a deploy-module dir. Every caller
		// probes a conventional <…>-devops/k8s location that need not exist,
		// so a missing path answers the predicate rather than failing it.
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return len(deployContexts) > 0, nil
}

func HasHelmChart(chartFilePath string) bool {
	info, err := os.Stat(chartFilePath)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
