package helm

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

func FindComponentHelmChartPath(projectRoot, componentName string) (string, error) {
	componentName = strings.TrimSpace(componentName)
	if componentName == "" {
		return "", fmt.Errorf("component name is required")
	}

	// A declared component scopes the search to its own k8s root, so same-named
	// charts elsewhere cannot match.
	searchRoot := projectRoot
	declared, ok, err := configpkg.DeclaredComponentPaths(projectRoot, componentName)
	if err != nil {
		return "", err
	}
	if ok {
		if k8sRoot := configpkg.ResolveProjectPath(projectRoot, declared.K8s); k8sRoot != "" {
			searchRoot = k8sRoot
		}
	}

	matches := make([]string, 0, 1)
	err = filepath.WalkDir(searchRoot, func(path string, d fs.DirEntry, err error) error {
		chartPath, ok, walkErr := componentHelmChartCandidate(path, d, componentName, err)
		if ok {
			matches = append(matches, chartPath)
		}
		return walkErr
	})
	if err != nil {
		return "", err
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("helm chart not found for component %q", componentName)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple Helm charts found for component %q", componentName)
	}
	return matches[0], nil
}

func componentHelmChartCandidate(path string, d fs.DirEntry, componentName string, err error) (string, bool, error) {
	if err != nil {
		return "", false, err
	}
	if d.IsDir() {
		if d.Name() == ".git" {
			return "", false, fs.SkipDir
		}
		return "", false, nil
	}
	if d.Name() != "Chart.yaml" {
		return "", false, nil
	}
	chartPath := filepath.Dir(path)
	if filepath.Base(chartPath) != componentName || filepath.Base(filepath.Dir(chartPath)) != "k8s" {
		return "", false, nil
	}
	return chartPath, true, nil
}
