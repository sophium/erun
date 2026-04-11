package eruncommon

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed assets/default-devops-chart/Chart.yaml assets/default-devops-chart/values.local.yaml assets/default-devops-chart/templates/service.yaml
var defaultDevopsChartFiles embed.FS

func resolveOpenRuntimeDeploySpec(store DeployStore, findProjectRoot ProjectFinderFunc, resolveDockerBuildContext BuildContextResolverFunc, resolveKubernetesDeployContext DeployContextResolverFunc, now NowFunc, target OpenResult) (DeploySpec, error) {
	for _, componentName := range openRuntimeComponentNames(target.Tenant) {
		spec, err := ResolveDeploySpecForOpenResult(store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, componentName, "")
		if err == nil {
			spec.Deploy.ReleaseName = RuntimeReleaseName(target.Tenant)
			return spec, nil
		}
		if !isHelmChartNotFoundForComponent(err) {
			return DeploySpec{}, err
		}
	}

	return resolveDefaultDevopsDeploySpec(target)
}

func openRuntimeComponentNames(tenant string) []string {
	names := []string{DevopsComponentName}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return names
	}

	tenantComponent := tenant + "-devops"
	if tenantComponent == DevopsComponentName {
		return names
	}
	return append([]string{tenantComponent}, names...)
}

func isHelmChartNotFoundForComponent(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "helm chart not found for component ")
}

func resolveDefaultDevopsDeploySpec(target OpenResult) (DeploySpec, error) {
	chartPath, err := materializeDefaultDevopsChart()
	if err != nil {
		return DeploySpec{}, err
	}
	if err := ensureDefaultDevopsValuesFile(chartPath, target.Environment); err != nil {
		return DeploySpec{}, err
	}

	deployContext := KubernetesDeployContext{
		Dir:           chartPath,
		ComponentName: DevopsComponentName,
		ChartPath:     chartPath,
	}
	deployInput, err := newHelmDeploySpec(target, deployContext, "")
	if err != nil {
		return DeploySpec{}, err
	}
	deployInput.ReleaseName = RuntimeReleaseName(target.Tenant)

	return DeploySpec{
		Target:        target,
		DeployContext: deployContext,
		Deploy:        deployInput,
	}, nil
}

func materializeDefaultDevopsChart() (string, error) {
	hash, err := defaultDevopsChartHash()
	if err != nil {
		return "", err
	}

	chartPath := filepath.Join(os.TempDir(), "erun-default-devops-chart-"+hash)
	if err := os.MkdirAll(chartPath, 0o755); err != nil {
		return "", err
	}

	entries := []string{
		"assets/default-devops-chart/Chart.yaml",
		"assets/default-devops-chart/values.local.yaml",
		"assets/default-devops-chart/templates/service.yaml",
	}
	for _, name := range entries {
		data, err := defaultDevopsChartFiles.ReadFile(name)
		if err != nil {
			return "", err
		}

		relativePath := strings.TrimPrefix(name, "assets/default-devops-chart/")
		targetPath := filepath.Join(chartPath, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(targetPath, data, 0o644); err != nil {
			return "", err
		}
	}

	return chartPath, nil
}

func defaultDevopsChartHash() (string, error) {
	names := []string{}
	if err := fs.WalkDir(defaultDevopsChartFiles, "assets/default-devops-chart", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		names = append(names, path)
		return nil
	}); err != nil {
		return "", err
	}

	sort.Strings(names)
	sum := sha256.New()
	for _, name := range names {
		data, err := defaultDevopsChartFiles.ReadFile(name)
		if err != nil {
			return "", err
		}
		_, _ = sum.Write([]byte(name))
		_, _ = sum.Write(data)
	}

	return hex.EncodeToString(sum.Sum(nil))[:16], nil
}

func ensureDefaultDevopsValuesFile(chartPath, environment string) error {
	environment = strings.ToLower(strings.TrimSpace(environment))
	if environment == "" || environment == "local" {
		return nil
	}

	valuesFilePath := filepath.Join(chartPath, "values."+environment+".yaml")
	if _, err := os.Stat(valuesFilePath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	return os.WriteFile(valuesFilePath, nil, 0o644)
}
